use std::time::Duration;

// used by libnss_*_hooks macros
use libnss::{interop::Response, libnss_group_hooks, libnss_passwd_hooks, libnss_shadow_hooks};

mod passwd;
use passwd::AuthdPasswdHooks;
libnss_passwd_hooks!(authd, AuthdPasswdHooks);

mod group;
use group::AuthdGroupHooks;
libnss_group_hooks!(authd, AuthdGroupHooks);

mod shadow;
use shadow::AuthdShadowHooks;
use tonic::{Code, Status};
libnss_shadow_hooks!(authd, AuthdShadowHooks);

mod logs;

mod client;

#[cfg(not(feature = "integration_tests"))]
const CONNECTION_TIMEOUT: Duration = Duration::from_secs(1);
#[cfg(not(feature = "integration_tests"))]
const REQUEST_TIMEOUT: Duration = Duration::from_secs(5);

#[cfg(feature = "integration_tests")]
const CONNECTION_TIMEOUT: Duration = Duration::from_secs(5);
#[cfg(feature = "integration_tests")]
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);

const DEFAULT_SOCKET_PATH: &str = "/run/authd.sock";

/// socket_path returns the socket path to connect to the gRPC server.
///
/// It uses the AUTHD_NSS_SOCKET env value if set and the custom_socket feature is enabled,
/// otherwise it uses the default path.
fn socket_path() -> String {
    #[cfg(feature = "custom_socket")]
    match std::env::var("AUTHD_NSS_SOCKET") {
        Ok(s) => return s,
        Err(err) => {
            info!(
                "AUTHD_NSS_SOCKET env variable not set, falling back to default socket path {DEFAULT_SOCKET_PATH}: {err}"
            );
        }
    }
    DEFAULT_SOCKET_PATH.to_string()
}

/// grpc_status_to_nss_response converts a gRPC status to a NSS response.
fn grpc_status_to_nss_response<T>(status: Status) -> Response<T> {
    match status.code() {
        Code::NotFound => Response::NotFound,
        _ => Response::Unavail,
    }
}

#[ctor::ctor]
/// init_logger is a constructor that ensures the logger object initialization only happens once per
/// library invocation in order to avoid races to the log file.
fn init_logger() {
    logs::init_logger();
}

#[cfg(feature = "integration_tests")]
#[ctor::ctor]
/// register_local_aad_nss_service_for_tests executes the C API to override the NSS lookup.
fn register_local_aad_nss_service_for_tests() {
    #[link(name = "db_override")]
    extern "C" {
        fn db_override();
    }

    unsafe {
        db_override();
    }
}
