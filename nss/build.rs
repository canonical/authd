fn main() -> Result<(), Box<dyn std::error::Error>> {
    tonic_prost_build::configure()
        .build_server(false)
        .protoc_arg("--experimental_allow_proto3_optional")
        .compile_protos(
            &["../internal/proto/authd/authd.proto"],
            &["../internal/proto"],
        )?;

    #[cfg(feature = "integration_tests")]
    cc::Build::new()
        .file("src/db_override.c")
        .define("INTEGRATION_TESTS", "1")
        .compile("db_override");

    Ok(())
}
