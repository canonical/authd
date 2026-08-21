# Copilot instructions for authd

Read [AGENTS.md](../AGENTS.md) before changing this repository. It contains
the shared build, test, packaging, and prose conventions. `AGENTS.md` and
`CLAUDE.md` are currently identical; keep their shared guidance synchronized
if either file is changed.

## Architecture and boundaries

authd is a Linux authentication daemon. PAM and NSS clients communicate with
the Go daemon over gRPC, while the daemon communicates with identity brokers
over D-Bus. Keep these boundaries explicit:

- `internal/proto/authd/authd.proto` defines the gRPC contract.
- `internal/services/manager.go` constructs and registers the PAM and user
  services on the daemon's gRPC server.
- `internal/brokers/` discovers brokers and negotiates the supported D-Bus
  interface version.
- `authd-oidc-brokers/` is a separate Go module that implements the external
  OIDC broker and provider-specific variants.
- `pam/` contains the Go PAM client, GDM extension, and C exec wrapper.
- `nss/` is the Rust NSS shared library. Its build script compiles the shared
  gRPC protocol.

Do not expose raw broker, D-Bus, or identity-provider errors to users. Preserve
the existing error-redaction and user-display error paths.

## Language and test conventions

### Go

- Format changed Go with `gofmt -s`. Run `scripts/golangci-lint run` after
  changing root Go code.
- Use `testify/require`, not `assert`. Keep test-only exports in
  `export_test.go` files with the `Z_ForTests_` prefix.
- Use the golden helpers in `internal/testutils/golden`; update fixtures only
  with `TESTS_UPDATE_GOLDEN=1`.
- Do not edit generated `*.pb.go` files by hand. Regenerate them from their
  source protocol.

### Rust and C

- The root Cargo workspace builds the NSS module. Run `cargo fmt` for Rust
  changes and preserve the feature gates in `nss/Cargo.toml`.
- PAM is deliberately split between a native Go shared library and a C wrapper
  plus Go child process. Do not merge those modes or bypass their D-Bus
  boundary.
- Generated PAM shared objects and generated protocol files come from the
  checked-in generation commands. Regenerate them instead of manually changing
  their outputs.

### Broker providers

- `authd-oidc-brokers/` is an independent Go module with its own lint config
  and provider build tags.
- Test broker changes with the default, `withgoogle`, and `withmsentraid`
  configurations. Generate the MS Entra binding before testing that provider.
- Never put tenant IDs, client secrets, test credentials, tokens, or
  unredacted logs in source, fixtures, workflow output, or documentation.

## Documentation and workflow conventions

- Documentation is a Sphinx and MyST project under `docs/`. Keep content in
  its Diataxis section and use `make -C docs html` or the focused documented
  check when changing it.
- Use precise system terms. The NSS module lets other Linux applications query
  authd-managed users; it is not primarily an internal authd client. Refer to
  the persisted store as the local database unless a value is truly a cache.
- Generated `authctl` documentation, man pages, and shell completions must
  stay synchronized with command changes.
- Preserve GitHub Actions path filters and least-privilege permissions. Format
  workflow changes with the repository's `yamlfmt` check.
- Keep non-trivial parsing and validation in
  `.github/scripts/parse-e2e-options.sh`, rather than growing shell logic in a
  workflow. When E2E PR markers change, update their parser, template,
  workflows, and `e2e-tests/TESTING.md` together.

## Conventions mined from recent reviews

- Explain technical behavior accurately and do not omit necessary details
  ([PR #1796](https://github.com/canonical/authd/pull/1796)).
- Add regression coverage for recoverable authentication paths, including
  cancellation and retry behavior where applicable
  ([PR #1811](https://github.com/canonical/authd/pull/1811)).
- Keep workflow artifact paths stable and verify an artifact exists immediately
  after download, so later steps fail clearly
  ([PR #1765](https://github.com/canonical/authd/pull/1765)).
- For user-management documentation, describe NSS as a system-facing lookup
  module and distinguish the local database from a cache
  ([PR #1760](https://github.com/canonical/authd/pull/1760)).

## Maintenance matrix

| When changing | Also update or validate |
| --- | --- |
| gRPC messages or services | Update `internal/proto/authd/authd.proto`; run `go generate ./internal/proto/authd/`; update implementations in `internal/services/pam/` or `internal/services/user/`, registration in `internal/services/manager.go`, and callers in `pam/`, `nss/`, or `cmd/authctl/internal/client/`; add focused service and client tests. |
| NSS-visible gRPC behavior | Update the gRPC contract and `nss/build.rs` together. The Rust build generates bindings from `../internal/proto/authd/authd.proto`; cover changes in `nss/integration-tests/` when the system lookup behavior changes. |
| D-Bus broker contract | Synchronize `internal/brokers/dbusbroker.go`, `examplebroker/com.ubuntu.auth.ExampleBroker.xml`, `authd-oidc-brokers/internal/dbusservice/`, and broker tests. A non-backward-compatible version change must also update both `LatestAPIVersion` values and the exported interface list. |
| Broker or provider behavior | Update `authd-oidc-brokers/internal/broker/`, the selected provider under `internal/providers/`, and variant configuration under `conf/variants/` when needed. Run tests for default, Google, and MS Entra builds; the MS Entra binding requires the recursive `libhimmelblau` submodule and generation. |
| PAM protocol or UI behavior | Update `pam/internal/proto/` or `pam/internal/gdm/` as applicable, regenerate with `go generate ./pam/`, and cover the relevant native, GDM, exec, or SSH integration flow in `pam/integration-tests/`. Keep `pam/README.md` accurate. |
| User database or local-entry behavior | Update `internal/users/`, database migrations under `internal/users/db/`, and fixture YAML or golden data. Review `internal/users/localentries/`, `cmd/authctl/`, and `docs/explanation/user-management.md` when the observable user or group behavior changes. |
| `authctl` command surface | Register commands in `cmd/authctl/root/`, `user/`, or `group/`; update command tests and golden output; regenerate `docs/reference/cli/`, `man/authctl.1`, and `shell-completion/` with their checked-in `go generate` entry points. |
| Generated code or IDs | Use the source generator: `internal/proto/authd/generate.go`, `pam/generate.go`, `docs/generate.go`, `man/generate.go`, `shell-completion/generate.go`, or `internal/users/generate.go`. Review generated output before committing it. |
| Debian package or release behavior | Update `debian/control`, package files, and `debian/changelog` as required. Follow `RELEASE.md`; `debian/changelog` is the canonical versioned release history. |
| Product documentation | Update the appropriate `docs/howto/`, `docs/reference/`, or `docs/explanation/` page and navigation. Keep README links and `CONTRIBUTING.md` synchronized when contributor-facing behavior changes. |
| E2E PR controls | Update `.github/pull_request_template.md`, `.github/scripts/parse-e2e-options.sh`, `.github/workflows/e2e-tests*.yaml`, and `e2e-tests/TESTING.md` together. Preserve the supported `e2e-brokers`, `e2e-tests`, and `e2e-ppa` marker behavior. |
| GitHub Actions | Retain relevant `push` and `pull_request` path filters, action permissions, and dependent reusable workflows. Run the `yamlfmt` workflow-equivalent lint for `.github/` changes. |
