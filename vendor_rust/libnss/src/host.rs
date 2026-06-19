use crate::interop::{CBuffer, Response, ToC};
use std::mem;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};
#[derive(Clone, Debug, PartialEq)]
pub struct Host {
    pub name: String,
    pub aliases: Vec<String>,
    pub addresses: Addresses,
}

#[derive(PartialEq)]
pub enum AddressFamily {
    IPv4,
    IPv6,
    Unspecified,
}
#[derive(Clone, Debug, PartialEq)]
pub enum Addresses {
    V4(Vec<Ipv4Addr>),
    V6(Vec<Ipv6Addr>),
}

impl ToC<CHost> for Host {
    unsafe fn to_c(&self, hostent: *mut CHost, buffer: &mut CBuffer) -> std::io::Result<()> {
        (*hostent).name = buffer.write_str(&self.name)?;
        (*hostent).h_aliases = buffer.write_strs(&self.aliases[..])?;

        let (addr_len, count) = match &self.addresses {
            Addresses::V4(addrs) => {
                (*hostent).h_addrtype = libc::AF_INET;
                (*hostent).h_length = 4;

                (4, addrs.len())
            }
            Addresses::V6(addrs) => {
                (*hostent).h_addrtype = libc::AF_INET6;
                (*hostent).h_length = 16;

                (16, addrs.len())
            }
        };

        let ptr_size = mem::size_of::<*mut libc::c_char>() as isize;
        let mut array_pos =
            buffer.reserve(ptr_size * (count as isize + 1))? as *mut *mut libc::c_char;
        (*hostent).h_addr_list = array_pos;

        match &self.addresses {
            Addresses::V4(addrs) => {
                for a in addrs {
                    let ptr = buffer.reserve(addr_len)?;

                    let o = a.octets();
                    libc::memcpy(
                        ptr as *mut libc::c_void,
                        o.as_ptr() as *mut libc::c_void,
                        addr_len as usize,
                    );

                    array_pos.write(ptr);
                    array_pos = array_pos.offset(1);
                }
            }
            Addresses::V6(addrs) => {
                for a in addrs {
                    let ptr = buffer.reserve(addr_len)?;

                    let o = a.octets();
                    libc::memcpy(
                        ptr as *mut libc::c_void,
                        o.as_ptr() as *mut libc::c_void,
                        addr_len as usize,
                    );

                    array_pos.write(ptr);
                    array_pos = array_pos.offset(1);
                }
            }
        }

        // Write null termination
        libc::memset(array_pos as *mut libc::c_void, 0, 1);
        Ok(())
    }
}

pub trait HostHooks {
    fn get_all_entries() -> Response<Vec<Host>>;

    fn get_host_by_name(name: &str, family: AddressFamily) -> Response<Host>;

    fn get_host_by_addr(addr: IpAddr) -> Response<Host>;
}

/// NSS C Host object
/// https://ftp.gnu.org/old-gnu/Manuals/glibc-2.2.3/html_chapter/libc_16.html#SEC318
#[repr(C)]
#[allow(missing_copy_implementations)]
#[derive(Debug)]
pub struct CHost {
    pub name: *mut libc::c_char,
    pub h_aliases: *mut *mut libc::c_char,
    pub h_addrtype: libc::c_int,
    pub h_length: libc::c_int,
    pub h_addr_list: *mut *mut libc::c_char,
}

#[macro_export]
macro_rules! libnss_host_hooks {
($mod_ident:ident, $hooks_ident:ident) => (
    $crate::_macro_internal::paste! {
        pub use self::[<libnss_host_ $mod_ident _hooks_impl>]::*;
        mod [<libnss_host_ $mod_ident _hooks_impl>] {
            #![allow(non_upper_case_globals)]

            use libc::c_int;
            use std::ffi::CStr;
            use std::str;
            use std::sync::{Mutex, MutexGuard};
            use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};
            use $crate::host::{CHost, HostHooks, Host, AddressFamily};
            use $crate::interop::{CBuffer, Response, NssStatus, Iterator};

            // https://code.woboq.org/userspace/glibc/resolv/netdb.h.html#62
            enum Herrno {
                NetDbInternal = -1,
                NetDbSuccess = 0,
                TryAgain = 2,
                NoRecovery = 3,
                NoData = 4,
            }

            $crate::_macro_internal::lazy_static! {
            static ref [<HOST_ $mod_ident _ITERATOR>]: Mutex<Iterator<Host>> = Mutex::new(Iterator::<Host>::new());
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _sethostent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Host>> = [<HOST_ $mod_ident _ITERATOR>].lock().unwrap();
                let status = match(<super::$hooks_ident as HostHooks>::get_all_entries()) {
                    Response::Success(entries) => iter.open(entries),
                    response => response.to_status()
                };
                status as c_int
            }

            #[no_mangle]
            extern "C" fn [<_nss_ $mod_ident _endhostent>]() -> c_int {
                let mut iter: MutexGuard<Iterator<Host>> = [<HOST_ $mod_ident _ITERATOR>].lock().unwrap();
                iter.close() as c_int
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _gethostent_r>](result: *mut CHost, buf: *mut libc::c_char, buflen: libc::size_t,
                                                                  errnop: *mut c_int) -> c_int {
                let mut iter: MutexGuard<Iterator<Host>> = [<HOST_ $mod_ident _ITERATOR>].lock().unwrap();
                let code: c_int = iter.next().to_c(result, buf, buflen, errnop) as c_int;
                if code == NssStatus::TryAgain as c_int {
                    iter.previous();
                }
                return code;
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _gethostbyaddr_r>](
                addr: *const libc::c_char,
                len: libc::size_t,
                format: c_int,
                result: *mut CHost,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut c_int,
                h_errnop: *mut c_int
            ) -> c_int {
                *h_errnop = Herrno::NetDbInternal as i32;

                // Convert address type
                let a = match (len, format) {
                    (4, libc::AF_INET) => {
                        let mut p = [0u8; 4];
                        libc::memcpy(p.as_ptr() as *mut libc::c_void, addr as *mut libc::c_void, 4);
                        IpAddr::V4(Ipv4Addr::from(p))
                    },
                    (16, libc::AF_INET6) => {
                        let mut p = [0u8; 16];
                        libc::memcpy(p.as_ptr() as *mut libc::c_void, addr as *mut libc::c_void, 16);
                        IpAddr::V6(Ipv6Addr::from(p))
                    },
                    _ => {
                        //error!("address length and format mismatch (length: {}, format: {})", len, format);
                        return NssStatus::NotFound as c_int;
                    }
                };

                match <super::$hooks_ident as HostHooks>::get_host_by_addr(a) {
                    response @ Response::Success(..) => {
                        *h_errnop = Herrno::NetDbSuccess as i32;
                        response
                    },
                    response => response
                }.to_c(result, buf, buflen, errnop) as c_int
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _gethostbyname_r>](
                name: *const libc::c_char,
                result: *mut CHost,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut libc::c_int,
                h_errnop: *mut libc::c_int
            ) -> libc::c_int {
                [<_nss_ $mod_ident _gethostbyname2_r>](name, libc::AF_UNSPEC, result, buf, buflen, errnop, h_errnop)
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _gethostbyname3_r>](
                name: *const libc::c_char,
                family: libc::c_int,
                result: *mut CHost,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut libc::c_int,
                h_errnop: *mut libc::c_int,
                ttlp: *mut i32,
                canonp: *mut *const libc::c_char
            ) -> libc::c_int {
                let name2_res = [<_nss_ $mod_ident _gethostbyname2_r>](name, family, result, buf, buflen, errnop, h_errnop);

                if ! ttlp.is_null() {
                    *ttlp = 0;
                }

                if ! canonp.is_null() {
                    *canonp = name;
                }

                name2_res
            }

            #[no_mangle]
            unsafe extern "C" fn [<_nss_ $mod_ident _gethostbyname2_r>](
                name: *const libc::c_char,
                family: libc::c_int,
                result: *mut CHost,
                buf: *mut libc::c_char,
                buflen: libc::size_t,
                errnop: *mut libc::c_int,
                h_errnop: *mut libc::c_int
            ) -> libc::c_int {

                let cstr = CStr::from_ptr(name);

                let status = match str::from_utf8(cstr.to_bytes()) {
                    Ok(name) => {
                        use super::$hooks_ident as hooks;
                        let status = match family {
                            libc::AF_INET => <hooks as HostHooks>::get_host_by_name(&name.to_string(), AddressFamily::IPv4),
                            libc::AF_INET6 => <hooks as HostHooks>::get_host_by_name(&name.to_string(), AddressFamily::IPv6),

                            // If unspecified, we are probably being called from gethostbyname_r so
                            // we will try IPv4 and if no results, then try IPv6
                            libc::AF_UNSPEC => match <hooks as HostHooks>::get_host_by_name(&name.to_string(), AddressFamily::IPv4) {
                                Response::NotFound => <hooks as HostHooks>::get_host_by_name(&name.to_string(), AddressFamily::IPv6),
                                val => val,
                            },
                            _ => {
                                *h_errnop = Herrno::NoRecovery as i32;
                                Response::Unavail
                            },
                        }.to_c(result, buf, buflen, errnop);

                        match status {
                            NssStatus::Success => {
                                *h_errnop = Herrno::NetDbSuccess as i32
                            }
                            NssStatus::TryAgain => {
                                *h_errnop = Herrno::TryAgain as i32
                            }
                            NssStatus::Unavail => {
                                *h_errnop = Herrno::NoRecovery as i32
                            }
                            NssStatus::NotFound => {
                                *h_errnop = Herrno::NoData as i32
                            }
                            _ => {
                                *h_errnop = Herrno::NetDbInternal as i32
                            }
                        };

                        status
                    }

                    Err(_) => NssStatus::NotFound
                };

                status as c_int
            }

        }
    }
)}
