use authd::user_service_client::UserServiceClient;
use hyper_util::rt::TokioIo;
use std::error::Error;
use tokio::net::UnixStream;
use tonic::transport::{Channel, Endpoint, Uri};
use tower::service_fn;

use crate::{info, CONNECTION_TIMEOUT};

pub mod authd {
    tonic::include_proto!("authd");
}

/// new_client creates a new client connection to the gRPC server or returns an active one.
pub async fn new_client() -> Result<UserServiceClient<Channel>, Box<dyn Error>> {
    info!("Connecting to authd on {}...", super::socket_path());

    // The URL must have a valid format, even though we don't use it.
    let ch = Endpoint::try_from("https://not-used:404")?
        .connect_timeout(CONNECTION_TIMEOUT)
        .connect_with_connector(service_fn(|_: Uri| async {
            let stream = UnixStream::connect(super::socket_path()).await?;
            Ok::<_, std::io::Error>(TokioIo::new(stream))
        }))
        .await?;

    Ok(UserServiceClient::new(ch))
}
