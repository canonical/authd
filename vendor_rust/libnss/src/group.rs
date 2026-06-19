use crate::interop::{CBuffer, Response, ToC};

#[derive(Clone)]
pub struct Group {
    pub name: String,
    pub passwd: String,
    pub gid: u32,
    pub members: Vec<String>,
}

impl ToC<CGroup> for Group {
    unsafe fn to_c(&self, result: *mut CGroup, buffer: &mut CBuffer) -> std::io::Result<()> {
        (*result).name = buffer.write_str(&self.name)?;
        (*result).passwd = buffer.write_str(&self.passwd)?;
        (*result).gid = self.gid as libc::gid_t;
        (*result).members = buffer.write_strs(&self.members)?;
        Ok(())
    }
}

pub trait GroupHooks {
    fn get_all_entries() -> Response<Vec<Group>>;

    fn get_entry_by_gid(gid: libc::gid_t) -> Response<Group>;

    fn get_entry_by_name(name: String) -> Response<Group>;
}

#[repr(C)]
#[allow(missing_copy_implementations)]
pub struct CGroup {
    pub name: *mut libc::c_char,
    pub passwd: *mut libc::c_char,
    pub gid: libc::gid_t,
    pub members: *mut *mut libc::c_char,
}

#[macro_export]
macro_rules! libnss_group_hooks {
($mod_ident:ident, $hooks_ident:ident) => (
    $crate::_macro_internal::paste! {
        pub use self::[<libnss_group_ $mod_ident _hooks_impl>]::*;
        mod [<libnss_group_ $mod_ident _hooks_impl>] {
            #![allow(non_upper_case_globals)]

            use libc::c_int;
            use std::ffi::CStr;
            use std::str;
            use std::sync::{Mutex, MutexGuard};
            use $crate::interop::{CBuffer, Iterator, Response, NssStatus};
            use $crate::group::{CGroup, GroupHooks, Group};

            $crate::_macro_internal::lazy_static! {
            static ref [<GROUP_ $mod_ident _ITERATOR>]: Mutex<Iterator<Group>> = Mutex::new(Iterator::<Group>::new());
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _setgrent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Group>> = [<GROUP_ $mod_ident _ITERATOR>].lock().unwrap();
                let status = match(<super::$hooks_ident as GroupHooks>::get_all_entries()) {
                    Response::Success(records) => iter.open(records),
                    response => response.to_status(),
                };
                status as c_int
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _endgrent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Group>> = [<GROUP_ $mod_ident _ITERATOR>].lock().unwrap();
                iter.close() as c_int
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _getgrent_r>](
                result: *mut CGroup,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int
            ) -> c_int {
                let mut iter: MutexGuard<Iterator<Group>> = [<GROUP_ $mod_ident _ITERATOR>].lock().unwrap();
                let code: c_int = iter.next().to_c(result, buf, buflen, errnop) as c_int;
                if code == NssStatus::TryAgain as c_int {
                    iter.previous();
                }
                return code;
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _getgrgid_r>](
                uid: libc::gid_t,
                result: *mut CGroup,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int
            ) -> c_int {
                <super::$hooks_ident as GroupHooks>::get_entry_by_gid(uid).to_c(
                    result,
                    buf,
                    buflen,
                    errnop
                ) as c_int
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _getgrnam_r>](
                name_: *const libc::c_char,
                result: *mut CGroup,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int
            ) -> c_int {
                let cstr = CStr::from_ptr(name_);

                match str::from_utf8(cstr.to_bytes()) {
                    Ok(name) => <super::$hooks_ident as GroupHooks>::get_entry_by_name(name.to_string()),
                    Err(_) => Response::NotFound
                }.to_c(result, buf, buflen, errnop) as c_int
            }
        }
    }
)
}
