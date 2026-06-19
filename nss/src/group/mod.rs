use crate::{info, REQUEST_TIMEOUT};
use libc::gid_t;
use libnss::group::{Group, GroupHooks};
use libnss::interop::Response;
use tokio::runtime::Builder;
use tonic::Request;

use crate::client::{self, authd};
use authd::Group as AuthdGroup;

pub struct AuthdGroupHooks;
impl GroupHooks for AuthdGroupHooks {
    /// get_all_entries returns all group entries.
    fn get_all_entries() -> Response<Vec<Group>> {
        get_all_entries()
    }

    /// get_entry_by_gid returns the group entry for the given gid.
    fn get_entry_by_gid(gid: gid_t) -> Response<Group> {
        get_entry_by_gid(gid)
    }

    /// get_entry_by_name returns the group entry for the given name.
    fn get_entry_by_name(name: String) -> Response<Group> {
        get_entry_by_name(name)
    }
}

/// get_all_entries connects to the grpc server and asks for all group entries.
fn get_all_entries() -> Response<Vec<Group>> {
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
        match client.list_groups(req).await {
            Ok(r) => Response::Success(authd_groups_to_group_entries(r.into_inner().groups)),
            Err(e) => {
                info!("error when listing groups: {}", e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// get_entry_by_gid connects to the grpc server and asks for the group entry with the given gid.
fn get_entry_by_gid(gid: gid_t) -> Response<Group> {
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

        let mut req = Request::new(authd::GetGroupByIdRequest { id: gid });
        req.set_timeout(REQUEST_TIMEOUT);
        match client.get_group_by_id(req).await {
            Ok(r) => Response::Success(authd_group_to_group_entry(r.into_inner())),
            Err(e) => {
                info!("error when getting group by ID '{}': {}", gid, e.code());
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// get_entry_by_name connects to the grpc server and asks for the group entry with the given name.
fn get_entry_by_name(name: String) -> Response<Group> {
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

        let mut req = Request::new(authd::GetGroupByNameRequest { name: name.clone() });
        req.set_timeout(REQUEST_TIMEOUT);
        match client.get_group_by_name(req).await {
            Ok(r) => Response::Success(authd_group_to_group_entry(r.into_inner())),
            Err(e) => {
                info!(
                    "error when getting group by name '{}': {}",
                    name,
                    e.code().description()
                );
                super::grpc_status_to_nss_response(e)
            }
        }
    })
}

/// authd_group_to_group_entry converts a authd::Group to a libnss::Group.
fn authd_group_to_group_entry(group: AuthdGroup) -> Group {
    Group {
        name: group.name,
        passwd: group.passwd,
        gid: group.gid,
        members: group.members,
    }
}

/// authd_groups_to_group_entries converts a Vec<authd::Group> to a Vec<libnss::Group>.
fn authd_groups_to_group_entries(groups: Vec<AuthdGroup>) -> Vec<Group> {
    groups.into_iter().map(authd_group_to_group_entry).collect()
}
