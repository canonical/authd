# authd AI Coding Instructions

## Project Overview

authd is an authentication daemon for cloud-based identity providers (MS Entra ID, Google IAM). It's a hybrid Go/Rust/C project that provides:
- **authd daemon** (Go): Main authentication service with gRPC API
- **PAM modules** (Go): Two implementations - native shared library for GDM, and C-wrapper+executable for other PAM apps
- **NSS module** (Rust): Name Service Switch integration for user/group lookups
- **Brokers** (Go): Pluggable D-Bus-based providers that interface with identity providers

## Architecture Fundamentals

### Component Communication
- **Internal**: gRPC for PAM/NSS ↔ authd (defined in `internal/proto/authd/authd.proto`)
- **External**: D-Bus for authd ↔ brokers (interface in `examplebroker/com.ubuntu.auth.ExampleBroker.xml`)
- **Daemon**: Systemd socket activation via `internal/daemon/daemon.go`
- **Data flow**: PAM/NSS → gRPC → authd → D-Bus → broker → identity provider

### Key Directories
- `cmd/authd/`, `cmd/authctl/`: Main binaries
- `internal/brokers/`: Broker manager and D-Bus integration
- `internal/services/`: gRPC service implementations (PAM, NSS, user management)
- `internal/users/`: User/group database management (SQLite + BoltDB legacy)
- `pam/`: PAM module with two build modes (see `pam/Hacking.md`)
- `nss/`: Rust NSS module using `libnss` crate
- `examplebroker/`: Reference broker implementation

## Building & Testing

### Build Commands
```bash
# Full Debian package (includes all components + tests)
debuild --prepend-path=${HOME}/.cargo/bin

# Individual components (development)
go build ./cmd/authd                    # authd daemon only
go generate ./pam/ && go build -tags pam_binary_exec -o ./pam/authd-pam ./pam  # PAM test client
cargo build                              # NSS (debug mode)
```

### Testing Conventions
- **Run tests**: `go test ./...` (add `-race` for race detection)
- **Golden files**: Use `internal/testutils/golden` package
  - Update with `TESTS_UPDATE_GOLDEN=1 go test ./...`
  - Compare/update: `golden.CheckOrUpdate(t, got)` or `golden.CheckOrUpdateYAML(t, got)`
- **Test helpers with underscores**: Functions prefixed `Z_ForTests_` are test-only exports (e.g., `Z_ForTests_CreateDBFromYAML`)
- **Environment variables**:
  - `AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1`: Skip tests requiring external tools (vhs)
  - `AUTHD_SKIP_ROOT_TESTS=1`: Skip tests that fail when run as root

### Code Generation
Critical: Run `go generate` before building PAM or when modifying protobuf files:
```bash
go generate ./pam/                      # PAM module (creates .so files)
go generate ./internal/proto/authd/     # Regenerate protobuf
go generate ./shell-completion/         # Shell completions
```

## Project-Specific Patterns

### Broker Integration
- Brokers are discovered from `/usr/share/authd/brokers/*.conf` (D-Bus service files)
- First broker is always the local broker (no config file)
- Manager in `internal/brokers/manager.go` handles session→broker and user→broker mappings
- Brokers must implement the D-Bus interface defined in `internal/brokers/dbusbroker.go`

### PAM Module Dual Mode
The PAM module has two implementations (see `pam/Hacking.md`):
1. **GDM mode** (`pam_authd.so`): Native Go shared library with GDM JSON protocol support
2. **Generic mode** (`pam_authd_exec.so` + `authd-pam` executable): C wrapper launching Go program via private D-Bus
   - Required for reliability with non-GDM PAM apps (avoids Go threading issues)

### Database & User Management
- Migrating from BoltDB to SQLite: `internal/users/db/` handles both
- User/group data cached locally in `/var/lib/authd/authd.db`
- ID allocation: `internal/users/idlimitsgenerator/` generates UID/GID ranges
- Group file updates: `internal/users/localentries/` handles local system files

### Testing Patterns
- Use `testify/require` for assertions (not `assert`)
- Golden files in `testdata/golden/` subdirectories matching test structure
- Test-only exports via `export_test.go` files (no build tag, package-level visibility)
- PAM integration tests use `vhs` tapes in `pam/integration-tests/testdata/tapes/`

## Common Workflows

### Adding a gRPC Service Method
1. Update `internal/proto/authd/authd.proto`
2. Run `go generate ./internal/proto/authd/`
3. Implement in service (e.g., `internal/services/pam/pam.go`)
4. Add tests with golden files

### Creating a New Broker
1. Implement D-Bus interface from `examplebroker/com.ubuntu.auth.ExampleBroker.xml`
2. Create `.conf` file in `/usr/share/authd/brokers/`
3. Register D-Bus service with systemd

### Debugging
- Logs via `github.com/canonical/authd/log` package (supports systemd journal)
- Enable debug: `authd daemon -vvv` (3 levels of verbosity)
- Socket path: `/run/authd.sock` (override with `AUTHD_NSS_SOCKET` for NSS tests)

## Dependencies & Tools
- **Go**: See `go.mod` for version requirements, uses go modules with vendoring
- **Rust**: Cargo with vendor filtering (see `Cargo.toml` workspace)
- **Required**: `libpam-dev`, `libglib2.0-dev`, `protoc`, `cargo-vendor-filterer`
- **Optional**: `vhs` (PAM CLI tests), `delta` (colored diffs in tests)

## Code Style
- Follow [Effective Go](https://go.dev/doc/effective_go) for Go style conventions
- Use `go fmt` and `gofmt -s`
- Rust: Standard cargo fmt conventions
