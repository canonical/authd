#[derive(Debug)]
pub enum Error {
    Initialization(Box<dyn std::error::Error + Send + Sync>),
    Write(::std::io::Error),
    Io(::std::io::Error),
}

pub type Result<T> = std::result::Result<T, Error>;

impl std::error::Error for Error {
    fn cause(&self) -> Option<&dyn std::error::Error> {
        match *self {
            Error::Initialization(ref err) => Some(&**err),
            Error::Write(_) => None,
            Error::Io(ref err) => Some(err),
        }
    }
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
        match *self {
            Error::Initialization(ref err) => write!(f, "Initialization error: {}", err),
            Error::Write(ref err) => write!(f, "Write error: {}", err),
            Error::Io(ref err) => write!(f, "Io error: {}", err),
        }
    }
}

impl From<std::io::Error> for Error {
    fn from(err: std::io::Error) -> Error {
        Error::Io(err)
    }
}

#[test]
fn error_is_send_sync() {
    fn is_send_sync<T: Send + Sync>() {}
    is_send_sync::<Error>();
}
