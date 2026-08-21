---
applyTo: "authd-oidc-brokers/**"
---

# OIDC broker instructions

This directory is a separate Go module. Use commands from this directory with
`go -C authd-oidc-brokers ...` and use its `.golangci.yaml` configuration.

- Provider selection is compile-time: the default build uses generic OIDC,
  `withgoogle` selects Google, and `withmsentraid` selects Microsoft Entra ID.
  Test all three variants after broker changes.
- Before MS Entra tests or linting, initialize the recursive
  `third_party/libhimmelblau` submodule and run:

  ```bash
  go -C authd-oidc-brokers generate --tags withmsentraid ./internal/providers/msentraid/...
  ```

  Do not hand-edit generated `himmelblau.h` or the generated library.
- A D-Bus API change must stay compatible with the daemon. Update
  `internal/broker/LatestAPIVersion`,
  `../../internal/brokers/dbusbroker.go`,
  `internal/dbusservice/interfaces/`, `internal/dbusservice/dbusservice.go`,
  and tests together.
- Broker configuration samples in `conf/variants/` are public templates.
  Preserve redaction and document new user-visible settings there.
- A provider-specific change should include a test for the affected provider,
  not only the default generic OIDC build.
