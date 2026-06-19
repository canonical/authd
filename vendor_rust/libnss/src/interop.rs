use std::collections::VecDeque;
use std::ffi::CString;
use std::io;

pub trait ToC<C> {
    unsafe fn to_c(&self, result: *mut C, buffer: &mut CBuffer) -> std::io::Result<()>;
}

#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum NssStatus {
    TryAgain = -2,
    Unavail = -1,
    NotFound = 0,
    Success = 1,
    Return = 2,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Response<R> {
    TryAgain,
    Unavail,
    NotFound,
    Success(R),
    Return,
}

impl<R> Response<R> {
    pub fn to_status(&self) -> NssStatus {
        use NssStatus::*;
        match self {
            Self::Success(..) => Success,
            Self::TryAgain => TryAgain,
            Self::Unavail => Unavail,
            Self::NotFound => NotFound,
            Self::Return => Return,
        }
    }

    pub unsafe fn to_c<C>(
        &self,
        result: *mut C,
        buf: *mut libc::c_char,
        buflen: libc::size_t,
        errnop: *mut libc::c_int,
    ) -> NssStatus
    where
        R: ToC<C>,
    {
        if let Self::Success(entity) = self {
            let mut buffer = CBuffer::new(buf as *mut libc::c_void, buflen);
            buffer.clear();

            match entity.to_c(result, &mut buffer) {
                Ok(()) => {
                    *errnop = 0;
                    self.to_status()
                }
                Err(e) => match e.raw_os_error() {
                    Some(e) => {
                        *errnop = e;
                        Self::TryAgain.to_status()
                    }
                    None => {
                        *errnop = libc::ENOENT;
                        Self::Unavail.to_status()
                    }
                },
            }
        } else {
            self.to_status()
        }
    }
}

pub struct Iterator<T> {
    items: Option<VecDeque<T>>,
    index: usize,
}

impl<T: Clone> Iterator<T> {
    pub fn new() -> Self {
        Iterator { items: None, index: 0 }
    }
    pub fn open(&mut self, items: Vec<T>) -> NssStatus {
        self.items = Some(VecDeque::from(items));
        self.index = 0;
        NssStatus::Success
    }

    pub fn next(&mut self) -> Response<T> {
        let response = match self.items {
            Some(ref mut items) => match items.get(self.index) {
                Some(entity) => Response::Success(entity.clone()),
                None => Response::NotFound,
            },
            None => Response::Unavail,
        };
        self.index += 1;

        return response;
    }

    pub fn previous(&mut self) {
        if self.index > 0 {
            self.index -= 1;
        }
    }

    pub fn close(&mut self) -> NssStatus {
        self.items = None;
        self.index = 0;
        NssStatus::Success
    }
}

pub struct CBuffer {
    start: *mut libc::c_void,
    pos: *mut libc::c_void,
    free: libc::size_t,
    len: libc::size_t,
}

impl CBuffer {
    pub fn new(ptr: *mut libc::c_void, len: libc::size_t) -> Self {
        CBuffer {
            start: ptr,
            pos: ptr,
            free: len,
            len,
        }
    }

    pub unsafe fn clear(&mut self) {
        libc::memset(self.start, 0, self.len);
    }

    pub unsafe fn write_str(&mut self, string: &str) -> io::Result<*mut libc::c_char> {
        // Capture start address
        let str_start = self.pos;

        // Convert string
        let cstr = CString::new(string).expect("Failed to convert string");
        let ptr = cstr.as_ptr();
        let len = libc::strlen(ptr);

        // Ensure we have enough capacity
        if self.free < len + 1 {
            return Err(io::Error::from_raw_os_error(libc::ERANGE));
        }

        // Copy string
        libc::memcpy(self.pos, ptr as *mut libc::c_void, len);
        self.pos = self.pos.offset(len as isize + 1);
        self.free -= len as usize + 1;

        // Return start of string
        Ok(str_start as *mut libc::c_char)
    }

    pub unsafe fn write_strs<S: AsRef<str>>(
        &mut self,
        strings: &[S],
    ) -> io::Result<*mut *mut libc::c_char> {
        let ptr_size = std::mem::size_of::<*mut libc::c_char>() as isize;

        let vec_start =
            self.reserve(ptr_size * (strings.len() as isize + 1))? as *mut *mut libc::c_char;
        let mut pos = vec_start;

        // Write strings
        for s in strings {
            pos.write(self.write_str(s.as_ref())?);
            pos = pos.offset(1);
        }

        libc::memset(pos as *mut libc::c_void, 0, ptr_size as usize);

        Ok(vec_start)
    }

    pub unsafe fn reserve(&mut self, len: isize) -> io::Result<*mut libc::c_char> {
        let start = self.pos;

        // Ensure we have enough capacity
        if self.free < len as usize {
            return Err(io::Error::from_raw_os_error(libc::ERANGE));
        }

        // Reserve space
        self.pos = self.pos.offset(len as isize);
        self.free -= len as usize;

        Ok(start as *mut libc::c_char)
    }
}
