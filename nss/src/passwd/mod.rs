use crate::{info, REQUEST_TIMEOUT};
use libc::uid_t;
use libnss::interop::Response;
use libnss::passwd::{Passwd, PasswdHooks};
use std::path::PathBuf;
use tokio::runtime::Builder;
use tonic::Request;

use crate::client::{self, authd};
use authd::User;

pub struct AuthdPasswdHooks;
impl PasswdHooks for AuthdPasswdHooks {
    /// get_all_entries returns all passwd entries.
    fn get_all_entries() -> Response<Vec<Passwd>> {
        get_all_entries()
    }

    /// get_entry_by_uid returns the passwd entry for the given uid.
    fn get_entry_by_uid(uid: uid_t) -> Response<Passwd> {
        get_entry_by_uid(uid)
    }

    /// get_entry_by_name returns the passwd entry for the given name.
    fn get_entry_by_name(name: String) -> Response<Passwd> {
        get_entry_by_name(name)
    }
}

/// get_all_entries connects to the grpc server and asks for all passwd entries.
fn get_all_entries() -> Response<Vec<Passwd>> {
    let rt = match Builder::new_current_thread().enable_all().build() {
        Ok(rt) => rt,
        Err(e) => {
            info!("could not create runtime for NSS: {}", e);
            return Response::Unavail;
        }
    };

    rt.block_on(async {
        let mut client = match client::new_client().await {
            Ok(c) => c,
            Err(e) => {
                info!("could not connect to gRPC server: {}", e);
                return Response::Unavail;
            }
        };

        let mut req = Request::new(authd::Empty {});
        req.set_timeout(REQUEST_TIMEOUT);
        match client.list_users(req).await {
            Ok(r) => Response::Success(users_to_passwd_entries(r.into_inner().users)),
            Err(e) => {
                info!("error when listing passwd: {}", e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// get_entry_by_uid connects to the grpc server and asks for the passwd entry with the given uid.
fn get_entry_by_uid(uid: uid_t) -> Response<Passwd> {
    let rt = match Builder::new_current_thread().enable_all().build() {
        Ok(rt) => rt,
        Err(e) => {
            info!("could not create runtime for NSS: {}", e);
            return Response::Unavail;
        }
    };

    rt.block_on(async {
        let mut client = match client::new_client().await {
            Ok(c) => c,
            Err(e) => {
                info!("could not connect to gRPC server: {}", e);
                return Response::Unavail;
            }
        };

        let mut req = Request::new(authd::GetUserByIdRequest { id: uid });
        req.set_timeout(REQUEST_TIMEOUT);
        match client.get_user_by_id(req).await {
            Ok(r) => Response::Success(user_to_passwd_entry(r.into_inner())),
            Err(e) => {
                info!("error when getting user by ID '{}': {}", uid, e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// get_entry_by_name connects to the grpc server and asks for the passwd entry with the given name.
fn get_entry_by_name(name: String) -> Response<Passwd> {
    let rt = match Builder::new_current_thread().enable_all().build() {
        Ok(rt) => rt,
        Err(e) => {
            info!("could not create runtime for NSS: {}", e);
            return Response::Unavail;
        }
    };

    rt.block_on(async {
        let mut client = match client::new_client().await {
            Ok(c) => c,
            Err(e) => {
                info!("could not connect to gRPC server: {}", e);
                return Response::Unavail;
            }
        };

        // This is a fake call done by PAM to avoid attacks, so we need to special case it to avoid spamming
        // logs with "Not Found" messages, as this call is done quite frequently.
        if name == "pam_unix_non_existent:" {
            return Response::NotFound;
        }

        #[cfg(feature = "integration_tests")]
        info!(
            "Get entry by name '{}' (pre-check: {})",
            name,
            should_pre_check()
        );

        let mut req = Request::new(authd::GetUserByNameRequest {
            name: name.clone(),
            should_pre_check: should_pre_check(),
        });
        req.set_timeout(REQUEST_TIMEOUT);
        match client.get_user_by_name(req).await {
            Ok(r) => Response::Success(user_to_passwd_entry(r.into_inner())),
            Err(e) => {
                info!("error when getting user by name '{}': {}", name, e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// user_to_passwd_entry converts a authd::User to a libnss::Passwd.
fn user_to_passwd_entry(entry: User) -> Passwd {
    Passwd {
        name: entry.name,
        passwd: "x".to_owned(),
        uid: entry.uid,
        gid: entry.gid,
        gecos: entry.gecos,
        dir: entry.homedir,
        shell: entry.shell,
    }
}

/// users_to_passwd_entries converts a Vec<authd::User> to a Vec<libnss::Passwd>.
fn users_to_passwd_entries(entries: Vec<User>) -> Vec<Passwd> {
    entries.into_iter().map(user_to_passwd_entry).collect()
}

static SSHD_BINARY_PATH: &str = "/usr/sbin/sshd";

fn is_proc_matching(pid: u32, name: &str) -> bool {
    let proc = procfs::process::Process::new(pid as i32);
    if proc.is_err() {
        return false;
    }

    let exe = proc.unwrap().exe();
    if exe.is_err() {
        return false;
    }

    let unwrapped_exe = exe.unwrap();

    #[cfg(feature = "integration_tests")]
    info!("Pre-check test: process '{}'", unwrapped_exe.display());

    matches!(unwrapped_exe, s if s == PathBuf::from(name))
}

/// should_pre_check returns true if the current process sshd or a child of sshd.
#[allow(unreachable_code)] // This function body is overridden in integration tests, so we need to ignore the warning.
fn should_pre_check() -> bool {
    #[cfg(feature = "should_pre_check_env")]
    return std::env::var("AUTHD_NSS_SHOULD_PRE_CHECK").is_ok();

    let pid = std::process::id();
    if is_proc_matching(pid, SSHD_BINARY_PATH) {
        return true;
    }

    is_proc_matching(std::os::unix::process::parent_id(), SSHD_BINARY_PATH)
}
