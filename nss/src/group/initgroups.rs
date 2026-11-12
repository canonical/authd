use std::ffi::CStr;
use std::io;
use std::ptr;

use libc::{c_char, c_int, c_long, gid_t, uid_t};
use libnss::interop::{NssStatus, Response};
use tonic::Request;

use crate::client::{self, authd};
use crate::{info, REQUEST_TIMEOUT};

// libnss's initgroups macro does not check realloc failures or handle a zero limit.
#[no_mangle]
unsafe extern "C" fn _nss_authd_initgroups_dyn(
    name: *const c_char,
    primary_gid: gid_t,
    start: *mut c_long,
    size: *mut c_long,
    groupsp: *mut *mut gid_t,
    limit: c_long,
    errnop: *mut c_int,
) -> c_int {
    let name = match CStr::from_ptr(name).to_str() {
        Ok(name) => name,
        Err(_) => {
            *errnop = libc::ENOENT;
            return NssStatus::NotFound as c_int;
        }
    };

    let groups = match supplementary_groups(name) {
        Response::Success(groups) => groups,
        response => {
            *errnop = match response {
                Response::NotFound => libc::ENOENT,
                Response::TryAgain => libc::EAGAIN,
                _ => libc::EIO,
            };
            return response.to_status() as c_int;
        }
    };

    match append_groups(
        &groups,
        primary_gid,
        &mut *start,
        &mut *size,
        &mut *groupsp,
        limit,
    ) {
        Ok(()) => NssStatus::Success as c_int,
        Err(errno) => {
            *errnop = errno;
            NssStatus::TryAgain as c_int
        }
    }
}

fn supplementary_groups(name: &str) -> Response<Vec<gid_t>> {
    let Some(rt) = crate::build_runtime() else {
        return Response::Unavail;
    };

    let (user, groups) = match rt.block_on(async {
        let mut client = match client::new_client().await {
            Ok(client) => client,
            Err(err) => {
                info!("could not connect to gRPC server: {}", err);
                return Response::Unavail;
            }
        };
        let mut req = Request::new(authd::GetUserByNameRequest {
            name: name.to_owned(),
            should_pre_check: false,
        });
        req.set_timeout(REQUEST_TIMEOUT);
        let user = match client.get_user_by_name(req).await {
            Ok(reply) => reply.into_inner(),
            Err(err) => return crate::grpc_status_to_nss_response(err),
        };

        let mut req = Request::new(authd::Empty {});
        req.set_timeout(REQUEST_TIMEOUT);
        match client.list_groups(req).await {
            Ok(reply) => Response::Success((user, reply.into_inner().groups)),
            Err(err) => crate::grpc_status_to_nss_response(err),
        }
    }) {
        Response::Success(result) => result,
        Response::NotFound => return Response::NotFound,
        Response::TryAgain => return Response::TryAgain,
        Response::Unavail => return Response::Unavail,
        Response::Return => return Response::Return,
    };
    drop(rt);

    let gids: Vec<_> = groups
        .into_iter()
        .filter(|group| group.members.contains(&user.name))
        .map(|group| group.gid)
        .collect();

    if name != user.name {
        // Resolve through passwd NSS after leaving Tokio: another source may now own the alias.
        match selected_uid(name) {
            Ok(Some(uid)) if uid == user.uid => {}
            Ok(_) => return Response::NotFound,
            Err(err) => {
                info!("could not resolve passwd entry for '{}': {}", name, err);
                return Response::Unavail;
            }
        }
        // Let libc apply NSS ordering and return actions. Unix-name lookups skip this branch.
        return match groups_for_unix_name(&user.name, user.gid) {
            Ok(groups) => Response::Success(groups),
            Err(err) => {
                info!("could not get groups for '{}': {}", user.name, err);
                Response::Unavail
            }
        };
    }
    Response::Success(gids)
}

fn selected_uid(name: &str) -> io::Result<Option<uid_t>> {
    let name = std::ffi::CString::new(name)?;
    let mut buffer = vec![0; 1024];
    loop {
        let mut entry = std::mem::MaybeUninit::<libc::passwd>::uninit();
        let mut result = ptr::null_mut();
        let status = unsafe {
            libc::getpwnam_r(
                name.as_ptr(),
                entry.as_mut_ptr(),
                buffer.as_mut_ptr(),
                buffer.len(),
                &mut result,
            )
        };
        if status == libc::ERANGE {
            buffer.resize(
                buffer
                    .len()
                    .checked_mul(2)
                    .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOMEM))?,
                0,
            );
            continue;
        }
        if status != 0 {
            return Err(io::Error::from_raw_os_error(status));
        }
        return Ok(if result.is_null() {
            None
        } else {
            Some(unsafe { (*result).pw_uid })
        });
    }
}

fn groups_for_unix_name(name: &str, primary_gid: gid_t) -> io::Result<Vec<gid_t>> {
    let name = std::ffi::CString::new(name)?;
    let mut groups = vec![0; 16];
    loop {
        let mut count = groups.len() as c_int;
        let status = unsafe {
            libc::getgrouplist(name.as_ptr(), primary_gid, groups.as_mut_ptr(), &mut count)
        };
        if status >= 0 {
            groups.truncate(count as usize);
            return Ok(groups);
        }
        if count <= groups.len() as c_int {
            return Err(io::Error::last_os_error());
        }
        groups.resize(count as usize, 0);
    }
}

unsafe fn append_groups(
    groups: &[gid_t],
    primary_gid: gid_t,
    start: &mut c_long,
    size: &mut c_long,
    groupsp: &mut *mut gid_t,
    limit: c_long,
) -> Result<(), c_int> {
    if *start < 0 || *size < *start || (*size > 0 && groupsp.is_null()) {
        return Err(libc::EINVAL);
    }
    let existing = if *start == 0 {
        &[]
    } else {
        std::slice::from_raw_parts(*groupsp, *start as usize)
    };
    let mut additional = Vec::new();
    for &gid in groups {
        if gid != primary_gid && !existing.contains(&gid) && !additional.contains(&gid) {
            additional.push(gid);
        }
    }
    if limit > 0 {
        additional.truncate(limit.saturating_sub(*start).max(0) as usize);
    }
    if additional.is_empty() {
        return Ok(());
    }

    let new_len = (*start as usize)
        .checked_add(additional.len())
        .filter(|len| *len <= c_long::MAX as usize)
        .ok_or(libc::ENOMEM)?;
    if new_len > *size as usize {
        let bytes = new_len
            .checked_mul(std::mem::size_of::<gid_t>())
            .filter(|bytes| *bytes <= isize::MAX as usize)
            .ok_or(libc::ENOMEM)?;
        let new_groups = libc::realloc((*groupsp).cast(), bytes).cast::<gid_t>();
        if new_groups.is_null() {
            return Err(libc::ENOMEM);
        }
        *groupsp = new_groups;
        *size = new_len as c_long;
    }
    ptr::copy_nonoverlapping(
        additional.as_ptr(),
        (*groupsp).add(*start as usize),
        additional.len(),
    );
    *start = new_len as c_long;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn appending_groups_preserves_existing_entries_and_respects_limits() {
        for (capacity, limit, expected) in [
            (2, -1, vec![10, 20, 30, 40]),
            (8, 0, vec![10, 20, 30, 40]),
            (2, 3, vec![10, 20, 30]),
            (2, 1, vec![10, 20]),
        ] {
            let mut start = 2;
            let mut size = capacity;
            unsafe {
                let mut groups =
                    libc::calloc(size as usize, std::mem::size_of::<gid_t>()).cast::<gid_t>();
                assert!(!groups.is_null());
                ptr::copy_nonoverlapping([10, 20].as_ptr(), groups, 2);
                let result = append_groups(
                    &[10, 20, 30, 30, 40, 50],
                    50,
                    &mut start,
                    &mut size,
                    &mut groups,
                    limit,
                );
                let got = std::slice::from_raw_parts(groups, start as usize).to_vec();
                libc::free(groups.cast());
                assert_eq!(result, Ok(()));
                assert_eq!(got, expected);
                assert_eq!(size, capacity.max(start), "spare capacity must not shrink");
            }
        }
    }

    #[test]
    fn appending_groups_allocates_an_empty_buffer() {
        let mut start = 0;
        let mut size = 0;
        let mut groups = ptr::null_mut();
        unsafe {
            let result = append_groups(&[10], 20, &mut start, &mut size, &mut groups, -1);
            assert_eq!(result, Ok(()));
            let got = std::slice::from_raw_parts(groups, start as usize).to_vec();
            libc::free(groups.cast());
            assert_eq!(got, [10]);
        }
    }

    #[test]
    fn appending_groups_rejects_an_invalid_buffer_without_changing_it() {
        let mut start = 1;
        let mut size = 1;
        let mut groups = ptr::null_mut();
        assert_eq!(
            unsafe { append_groups(&[10], 20, &mut start, &mut size, &mut groups, -1) },
            Err(libc::EINVAL)
        );
        assert_eq!(start, 1);
        assert_eq!(size, 1);
        assert!(groups.is_null());
    }
}
