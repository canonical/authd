# authd Development Environment

LXD VM for daily authd development. Provides full systemd, D-Bus, SSH, and GDM —
enough to build, test, and exercise real PAM/NSS login flows without touching your
host system. The host source tree is bind-mounted at `/workspace/authd`.

## Prerequisites

- LXD: `sudo snap install lxd && lxd init --auto && sudo usermod -aG lxd "$USER"` (logout/login)
- SSH key: `ls ~/.ssh/id_ed25519.pub || ssh-keygen -t ed25519`
- SPICE viewer (only needed for `lxc console --type=vga`): `sudo apt install virt-viewer`

The SSH key is injected into the VM for key-based authentication, VS Code Remote SSH,
and testing PAM login flows over SSH (`ssh user@domain.com@vm-ip`).

## Quick Start

```bash
./dev/dev-env.sh up                    # Create VM + build authd (~20 min first time)
./dev/dev-env.sh broker google \
    --client-id ID --client-secret SEC \
    --ssh-suffixes '@gmail.com'        # Configure broker
ssh you@gmail.com@$(./dev/dev-env.sh ip)  # Test login from host
```

The `up` command provisions the VM, installs all toolchains (Go, Rust, protoc),
and automatically builds + installs authd, PAM, and NSS modules. After `up`
completes, the only manual step is configuring a broker with your IdP credentials.

## Commands

Global flags (before the subcommand, apply to all commands):
`--name NAME` (default: authd-dev), `--release NAME` (default: noble), `--workspace PATH` (default: /workspace/authd).

| Command | Description |
|---------|-------------|
| `up` | Create and provision VM (also restarts a stopped VM) |
| `stop` | Stop VM (preserves state; restart with `up`) |
| `down [--force]` | Stop and delete VM and profile |
| `shell` | Direct shell via `lxc exec` (no PAM, always works) |
| `ssh` | Connect via SSH (goes through PAM — for login testing) |
| `status` | VM status and snapshots |
| `snapshot <name>` / `restore <name>` | Manage snapshots |
| `broker <variant> [opts]` | Configure credentials + install broker; `--rebuild` to recompile without touching credentials; `edit` subaction to open broker.conf |
| `build [component]` | Fast rebuild + install a single component |
| `validate` | Health-check authd stack (socket, PAM, NSS, brokers) |
| `test [args]` | Run tests inside the VM (e.g. `--update-golden`, `--skip-external`) |
| `logs [target]` | Tail logs (authd/google/msentraid/oidc/cloud-init) |
| `exec <cmd>` | Run a command inside the VM (in workspace dir) |
| `ip` | Print VM IP |

## Iterative Development

After the initial `install-authd`, use `build` from the **host**:

```bash
./dev/dev-env.sh build authd          # Daemon + proto regen + restart
./dev/dev-env.sh build pam            # PAM modules (reconnect SSH to load)
./dev/dev-env.sh build nss            # NSS module + ldconfig
./dev/dev-env.sh build broker google  # Broker binary + restart
./dev/dev-env.sh build all            # Full install-authd
```

To run tests inside the VM:

```bash
./dev/dev-env.sh test                                        # All tests with race detection (default)
./dev/dev-env.sh test ./internal/brokers/...                 # Specific package
./dev/dev-env.sh test --update-golden                        # Auto-update golden files
./dev/dev-env.sh test --skip-external                        # Skip VHS (requires external tools)
```

Tail service logs from the host:

```bash
./dev/dev-env.sh logs authd            # authd daemon logs
./dev/dev-env.sh logs google           # Google broker logs
./dev/dev-env.sh logs cloud-init       # Cloud-init provisioning log
```

## Broker Setup

```bash
# Google (--issuer defaults to accounts.google.com)
./dev/dev-env.sh broker google \
    --client-id 843411...googleusercontent.com \
    --client-secret GOCSPX-... \
    --ssh-suffixes '@gmail.com'

# Microsoft Entra ID
./dev/dev-env.sh broker msentraid \
    --issuer https://login.microsoftonline.com/TENANT_ID/v2.0 \
    --client-id CLIENT_ID \
    --ssh-suffixes '@yourdomain.com'

# Generic OIDC (Keycloak, etc.)
./dev/dev-env.sh broker oidc \
    --issuer https://keycloak.example.com/realms/myrealm \
    --client-id authd-client --client-secret SECRET \
    --ssh-suffixes '*'
```

The `broker` command patches credentials, enables, and restarts the service automatically.
Add `--rebuild` to recompile from source (e.g. after pulling broker changes).
Use `broker <variant> edit` to open `broker.conf` directly in your editor.

## Validation

After installing authd (and optionally a broker), verify the full stack:

```bash
./dev/dev-env.sh validate
```

Checks: toolchain, authd.socket, PAM module, NSS config, broker registration.

## Troubleshooting

| Problem | Check |
|---------|-------|
| VM won't start | `lxc info authd-dev` |
| Provisioning failed | `./dev/dev-env.sh logs cloud-init` or `lxc exec authd-dev -- cloud-init status` |
| Build failed during `up` | `./dev/dev-env.sh exec ./dev/scripts/install-authd` to retry |
| Wrong Go version | `which go` should be `/usr/local/go/bin/go`, not `/usr/bin/go` |
| authd won't start | `sudo systemctl status authd.socket` (authd uses socket activation) |
| SSH login rejects user | `ssh_allowed_suffixes_first_auth` must be set in broker.conf |
| GDM console login password | `cat ~/.config/<vm-name>/vm-password` (default: `~/.config/authd-dev/vm-password`) |
| VGA console freezes immediately | See [Known issue: VGA console on HiDPI](#known-issues) below |

**Recovery from failed provisioning:** `./dev/dev-env.sh down --force && ./dev/dev-env.sh up`

**Re-running install scripts:** `./dev/dev-env.sh exec ./dev/scripts/install-authd` is safe to re-run (idempotent config, rebuilds binaries).
`broker` auto-detects whether a binary exists: if it does, only credentials are patched (no rebuild). Pass `--rebuild` to recompile from source.

**Snapshots:** `up` creates two snapshots: `clean` (toolchain only, pre-build) and `installed` (authd + PAM + NSS built; no broker credentials — run `./dev/dev-env.sh broker <variant>` to configure).
Use `./dev/dev-env.sh restore installed` to reset to a freshly built state.

**Go/Rust versions** are auto-synced from `go.mod` and `authd-oidc-brokers/rust-toolchain.toml`
at VM creation time. No manual version management needed.

## GDM / VGA Console

The VM runs GDM at `graphical.target` so you can test real GDM login flows.

**Accessing the GDM screen:**
```bash
lxc console authd-dev --type=vga   # Opens SPICE viewer (requires virt-viewer on host)
```

The default login password is random-generated at VM creation time:
```bash
cat ~/.config/authd-dev/vm-password     # default VM name
cat ~/.config/<vm-name>/vm-password     # custom --name
```

## File Layout

```
dev/
├── dev-env.sh              # VM lifecycle + build (run from host)
├── cloud-init.yaml         # VM provisioning template (Go, Rust, deps, GDM)
├── DEV-GUIDE.md            # This file
├── lib/
│   └── common.sh           # Shared helpers (output, build, variant config)
└── scripts/
    ├── install-authd       # Build + install authd + PAM + NSS + configure (verbose mode)
    └── install-broker      # Configure or build+install OIDC broker + D-Bus + systemd
```
