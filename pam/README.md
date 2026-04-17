# authd PAM Module

This directory contains the PAM (Pluggable Authentication Modules) integration
for authd.

## Overview

The module has two binary implementations to cover different PAM consumers,
and within those a further split at runtime into three UI modes.

### Binary implementations

**GDM mode (`pam_authd.so`)** — a native Go shared library loaded directly by
the GDM PAM worker via the `gdm-authd` PAM service. In addition to standard
PAM, it speaks the
[GDM extended binary / JSON protocol](https://gitlab.gnome.org/GNOME/gdm/-/merge_requests/227)
so that GDM can render rich authentication UI.

**Generic / exec mode (`pam_authd_exec.so` + `pam_authd`)** — a thin C wrapper
(`go-exec/`) that launches a companion Go executable (`pam_authd`) as a child
process and relays PAM conversations over a private D-Bus connection. This is
required for applications such as `sshd`, `su`, `sudo`, and `login` where
loading a Go shared library would cause threading hangs.

### Runtime UI modes

Both binary implementations share the same Go authentication logic inside
`pam_authd`. At runtime it selects one of three UI modes based on the
environment:

**GDM** (`adapter.Gdm`) — active when the GDM extended binary/JSON PAM
extension is detected. This only ever happens inside GDM's PAM worker during a
graphical login.

**Interactive terminal** (`adapter.InteractiveTerminal`) — active when a real
TTY is attached to the PAM session and GDM is not in use. This covers local
terminal applications: `su`, `sudo`, `login`, `passwd`. A Bubble Tea TUI is
rendered directly on the terminal.

**Native / headless** (`adapter.Native`) — active when there is **no TTY**
available to the PAM module and GDM is not in use. This is the normal case for
**SSH**: `sshd` owns the terminal itself and does not expose a TTY to the PAM
module, so `pam_authd` drives the conversation entirely through PAM's own
challenge/response protocol (`PAM_PROMPT_ECHO_ON` / `PAM_PROMPT_ECHO_OFF`).
The `force_native_client=true` module argument forces this mode regardless of
TTY availability, which is useful for testing.

## Directory Structure

| Path | Description |
|------|-------------|
| `pam.go` | Core PAM module logic |
| `pam_module.go` | Auto-generated C-exported `pam_sm_*` entry points |
| `main-exec.go` | Entry point for the exec-mode child binary |
| `go-exec/` | C wrapper that implements `pam_authd_exec.so` |
| `internal/adapter/` | Authentication UI adapters (native CLI and GDM) |
| `tools/pam-runner/` | Developer tool for manual end-to-end PAM testing |
| `integration-tests/` | Automated integration tests using `vhs` tapes |

## Building

### GDM shared library

The module can be built in release or debug mode:

```bash
# Release build
go generate -C pam -x

# Debug build
go generate -C pam -x -tags pam_debug
```

Output: `pam/pam_authd.so`

This library can technically be loaded by any PAM application, but due to Go's
multithreading behaviour it is known to hang when loaded by some applications
(e.g. `sshd`). The generic exec module below is the recommended approach for
non-GDM consumers.

### Generic exec module

The exec module pairs a minimal C wrapper with a Go child binary that
communicate over a private D-Bus connection. The connection address is nearly
impossible to predict, and authenticity is further enforced by native D-Bus
credential checks and child PID verification.

```bash
go generate -C pam -x
go build -C pam -o $PWD/pam/pam_authd
```

Output:
- `pam/go-exec/pam_authd_exec.so` — the actual PAM module
- `pam/pam_authd` — companion child application

Use it in a PAM service file:

```
auth sufficient /path/to/pam_authd_exec.so /path/to/pam_authd
```

Optional arguments (passed after the executable path):

```
auth sufficient /path/to/pam_authd_exec.so /path/to/pam_authd \
    --exec-debug debug=true force_native_client=true logfile=/dev/stderr
```

See [`go-exec/module.c`](go-exec/module.c) and [`pam.go`](pam.go) for the full
list of supported arguments.

## Manual Testing

The `pam-runner` tool compiles the modules, writes a temporary PAM service file,
and runs a real PAM transaction so you can exercise the full flow without
installing anything system-wide.

```bash
# Basic login flow
go run -tags=withpamrunner ./pam/tools/pam-runner login

# Login with a specific authd socket and debug output
go run -tags=withpamrunner ./pam/tools/pam-runner login \
    socket=/tmp/authd.sock debug=true

# Password-change flow (like passwd)
go run -tags=withpamrunner ./pam/tools/pam-runner passwd

# Force the native CLI UI (as used by SSH)
AUTHD_PAM_RUNNER_SUPPORTS_CONVERSATION=1 go run -tags withpamrunner \
    ./pam/tools/pam-runner login force_native_client=true
```

## Running Tests

```bash
go test ./pam/...
```

Integration tests require the `vhs` tool. Set
`AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1` to skip them:

```bash
AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1 go test ./pam/...
```

## Troubleshooting

Enable debug logging by passing `debug=true` together with `logfile=` pointing to a writable path (`/dev/stderr` is valid):

```
auth sufficient /path/to/pam_authd_exec.so /path/to/pam_authd \
    debug=true logfile=/dev/stderr
```

Alternatively, leave `logfile` unset and tail the systemd journal in real time:

```bash
journalctl -ef _COMM=exec-child
```

> **Note:** To test against an installed version of authd rather than a local
> build, point `socket=` at the installed socket path (e.g. `/run/authd.sock`).
