use crate::interop::{CBuffer, Response, ToC};
#[derive(Clone)]
pub struct Shadow {
    pub name: String,
    pub passwd: String,
    pub last_change: isize,
    pub change_min_days: isize,
    pub change_max_days: isize,
    pub change_warn_days: isize,
    pub change_inactive_days: isize,
    pub expire_date: isize,
    pub reserved: usize,
}

impl ToC<CShadow> for Shadow {
    unsafe fn to_c(&self, result: *mut CShadow, buffer: &mut CBuffer) -> std::io::Result<()> {
        (*result).name = buffer.write_str(&self.name)?;
        (*result).passwd = buffer.write_str(&self.passwd)?;
        (*result).last_change = self.last_change as libc::c_long;
        (*result).change_min_days = self.change_min_days as libc::c_long;
        (*result).change_max_days = self.change_max_days as libc::c_long;
        (*result).change_warn_days = self.change_warn_days as libc::c_long;
        (*result).change_inactive_days = self.change_inactive_days as libc::c_long;
        (*result).expire_date = self.expire_date as libc::c_long;
        (*result).reserved = self.reserved as libc::c_ulong;
        Ok(())
    }
}

pub trait ShadowHooks {
    fn get_all_entries() -> Response<Vec<Shadow>>;

    fn get_entry_by_name(name: String) -> Response<Shadow>;
}

#[repr(C)]
#[allow(missing_copy_implementations)]
pub struct CShadow {
    pub name: *mut libc::c_char,
    pub passwd: *mut libc::c_char,
    pub last_change: libc::c_long,
    pub change_min_days: libc::c_long,
    pub change_max_days: libc::c_long,
    pub change_warn_days: libc::c_long,
    pub change_inactive_days: libc::c_long,
    pub expire_date: libc::c_long,
    pub reserved: libc::c_ulong,
}

#[macro_export]
macro_rules! libnss_shadow_hooks {
($mod_ident:ident, $hooks_ident:ident) => (
    $crate::_macro_internal::paste! {
        pub use self::[<libnss_shadow_ $mod_ident _hooks_impl>]::*;
        mod [<libnss_shadow_ $mod_ident _hooks_impl>] {
            #![allow(non_upper_case_globals)]

            use libc::c_int;
            use std::ffi::CStr;
            use std::str;
            use std::sync::{Mutex, MutexGuard};
            use $crate::interop::{CBuffer, Iterator, Response, NssStatus};
            use $crate::shadow::{CShadow, ShadowHooks, Shadow};

            $crate::_macro_internal::lazy_static! {
            static ref [<SHADOW_ $mod_ident _ITERATOR>]: Mutex<Iterator<Shadow>> = Mutex::new(Iterator::<Shadow>::new());
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _setspent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Shadow>> = [<SHADOW_ $mod_ident _ITERATOR>].lock().unwrap();
                let status = match(<super::$hooks_ident as ShadowHooks>::get_all_entries()) {
                    Response::Success(entries) => iter.open(entries),
                    response => response.to_status()
                };
                status as c_int
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _endspent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Shadow>> = [<SHADOW_ $mod_ident _ITERATOR>].lock().unwrap();
                iter.close() as c_int
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _getspent_r>](
                result: *mut CShadow,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int
            ) -> c_int {
                let mut iter: MutexGuard<Iterator<Shadow>> = [<SHADOW_ $mod_ident _ITERATOR>].lock().unwrap();
                let code: c_int = iter.next().to_c(result, buf, buflen, errnop) as c_int;
                if code == NssStatus::TryAgain as c_int {
                    iter.previous();
                }
                return code;
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _getspnam_r>](
                name_: *const libc::c_char,
                result: *mut CShadow,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int
            ) -> c_int {
                let cstr = CStr::from_ptr(name_);

                match str::from_utf8(cstr.to_bytes()) {
                    Ok(name) => <super::$hooks_ident as ShadowHooks>::get_entry_by_name(name.to_string()),
                    Err(_) => Response::NotFound
                }.to_c(result, buf, buflen, errnop) as c_int
            }
        }
    }
)
}
