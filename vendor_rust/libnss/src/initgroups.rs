use crate::group::Group;
use crate::interop::Response;

pub trait InitgroupsHooks {
    fn get_entries_by_user(user: String) -> Response<Vec<Group>>;
}

#[macro_export]
macro_rules! libnss_initgroups_hooks {
($mod_ident:ident, $hooks_ident:ident) => (
    $crate::_macro_internal::paste! {
        pub use self::[<libnss_initgroups_ $mod_ident _hooks_impl>]::*;
        mod [<libnss_initgroups_ $mod_ident _hooks_impl>] {
            #![allow(non_upper_case_globals)]

            use libc::{c_int, ENOENT};
            use std::ffi::CStr;
            use std::mem;
            use std::slice;
            use $crate::interop::{NssStatus, Response};
            use $crate::group::{CGroup, Group};
            use $crate::initgroups::InitgroupsHooks;

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _initgroups_dyn>](
                name: *const libc::c_char,
                skipgroup: libc::gid_t,
                start: *mut libc::size_t,
                size: *mut libc::size_t,
                mut groupsp: *mut *mut libc::gid_t,
                limit: libc::size_t,
                errnop: *mut c_int,
            ) -> c_int {
                let user = match std::str::from_utf8(CStr::from_ptr(name).to_bytes()) {
                    Ok(x) => x.to_owned(),
                    Err(_) => {
                        *errnop = ENOENT;
                        return NssStatus::NotFound as c_int;
                    }
                };

                let groups: Vec<Group> = match <super::$hooks_ident as InitgroupsHooks>::get_entries_by_user(user) {
                    Response::Success(records) => records,
                    response => {
                        *errnop = ENOENT;
                        return response.to_status() as c_int;
                    }
                };
                let groups = groups
                    .into_iter()
                    .filter_map(|x| {
                        if x.gid == skipgroup {
                            None
                        } else {
                            Some(x.gid as libc::gid_t)
                        }
                    })
                    .take(limit - *start)
                    .collect::<Vec<libc::gid_t>>();
                if groups.is_empty() {
                    return NssStatus::Success as c_int;
                }

                if *start + groups.len() != *size {
                    let new_size = *start + groups.len();
                    *groupsp = libc::realloc(
                        *groupsp as *mut libc::c_void,
                        new_size * mem::size_of::<libc::gid_t>(),
                    ) as *mut libc::gid_t;
                    *size = new_size;
                }

                let group_array: &mut [libc::gid_t] = slice::from_raw_parts_mut(*groupsp, *size);
                group_array[*start..*size].copy_from_slice(&groups);
                *start = group_array.len();

                NssStatus::Success as i32
            }
        }
    }
)
}
