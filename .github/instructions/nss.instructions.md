---
applyTo: "nss/**"
---

# NSS module instructions

The Rust NSS module is part of the root Cargo workspace and exposes authd user
and group information to the rest of the Linux system.

- Run `cargo fmt` and the focused Cargo test or build from the repository root
  for Rust changes.
- `nss/build.rs` compiles `../internal/proto/authd/authd.proto` during every
  build. Changes to the shared gRPC API require compatible NSS client changes
  and integration coverage in `nss/integration-tests/`.
- Keep `custom_socket` and `integration_tests` feature gates intact. The
  `AUTHD_NSS_SOCKET` override is only available when the relevant feature is
  enabled.
- Preserve the NSS ABI and lookup behavior. Changes that alter user or group
  resolution need both Rust-level and system-facing integration tests.
