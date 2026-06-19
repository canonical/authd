use crate::{info, REQUEST_TIMEOUT};
use libnss::interop::Response;
use libnss::shadow::{Shadow, ShadowHooks};
use tokio::runtime::Builder;
use tonic::Request;

use crate::client::{self, authd};
use authd::User;

pub struct AuthdShadowHooks;

impl ShadowHooks for AuthdShadowHooks {
    /// get_all_entries returns all shadow entries.
    fn get_all_entries() -> Response<Vec<Shadow>> {
        get_all_entries()
    }

    /// get_entry_by_name returns the shadow entry for the given name.
    fn get_entry_by_name(name: String) -> Response<Shadow> {
        get_entry_by_name(name)
    }
}

/// get_all_entries connects to the grpc server and asks for all shadow entries.
fn get_all_entries() -> Response<Vec<Shadow>> {
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
            Ok(r) => Response::Success(users_to_shadow_entries(r.into_inner().users)),
            Err(e) => {
                info!("error when listing shadow: {}", e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// get_entry_by_name connects to the grpc server and asks for the shadow entry with the given name.
fn get_entry_by_name(name: String) -> Response<Shadow> {
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

        let mut req = Request::new(authd::GetUserByNameRequest {
            name,
            should_pre_check: false,
        });
        req.set_timeout(REQUEST_TIMEOUT);
        match client.get_user_by_name(req).await {
            Ok(r) => Response::Success(shadow_entry(r.into_inner().name)),
            Err(e) => {
                info!("error when getting shadow entry: {}", e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// shadow_entries_to_shadows converts a vector of shadow entries to a vector of shadows.
fn shadow_entry(name: String) -> Shadow {
    Shadow {
        name,
        passwd: "x".to_owned(),
        last_change: -1,
        change_min_days: -1,
        change_max_days: -1,
        change_warn_days: -1,
        change_inactive_days: -1,
        expire_date: -1,
        reserved: usize::MAX,
    }
}

/// shadow_entries_to_shadows converts a vector of shadow entries to a vector of shadows.
fn users_to_shadow_entries(names: Vec<User>) -> Vec<Shadow> {
    names
        .into_iter()
        .map(|user| shadow_entry(user.name))
        .collect()
}
