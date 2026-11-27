# authd Workshop development environment

The Workshop VM builds authd from this checkout and runs the GDM greeter
and sshd for cloud login tests.

Run all host commands from the repository root.

## What you get

- One Ubuntu 26.04 VM with authd, `authctl`, both PAM modules, and NSS.
- GDM from the Ubuntu archive, sshd, and D-Bus wiring.
- Go 1.26 and stable Rust from the Workshop SDK Store.
- The host checkout is mounted at `/project`. Launch and applied refreshes
  build and install the default stack; brokers build on demand.
- The `workshop` user has passwordless sudo in the guest. That is
  root-equivalent inside the VM, as is host LXD access.

Actions are defined in [`.workshop.yaml`](../.workshop.yaml):

| Action | Purpose |
| --- | --- |
| `build [...]` | Rebuild and reinstall from source (bare = default set, `all` = default + brokers) |
| `broker configure <variant> ...` | Configure a broker; builds its binary if missing |
| `broker edit/start/stop/status <variant>` | Edit config or manage broker lifecycle |
| `validate` | Check PAM, NSS, GDM, sshd, and the socket |
| `test [--no-race]` | Run `go test -race ./...` for the root module |
| `lint` | Run the pinned `golangci-lint` wrapper |
| `logs [...]` | Show journal logs (exits; `-f` follows) |
| `login <user@domain>` | Open a login shell through sshd and PAM |
| `gdm status\|restart\|logs` | Check or restart GDM inside the VM |

Run `workshop run -- <action> --help` for full flags.

## Prerequisites

Install [Workshop](https://ubuntu.com/workshop/docs/tutorial/part-1-get-started/#tut-install)
0.9.10 or later and LXD 6.10 or later. The LXD `6/stable` channel works for
new installations. The host needs hardware virtualization. To open the
greeter, install `remote-viewer` or `spicy` and use a desktop session with
access to the host LXD daemon.

## Launch

```shell
workshop launch --wait-on-error
workshop run -- validate
```

The definition sets 8 GiB of memory and a 30 GiB root disk. Workshop runs
the base setup on a 10 GiB disk and expands it before building authd.
If setup fails, use `workshop changes`, `workshop tasks <CHANGE_ID>`, and
`workshop info` to find the failing step.

If you already have an authd workshop, start it if stopped, then use
`workshop refresh --wait-on-error` to apply the new definition. Back up
any guest config if the old SDK did not have a `save-state` hook.

## Daily use

Rebuild one component after editing source. No refresh is needed:

```shell
workshop run -- build pam                  # or authd, authctl, nss
workshop run -- build brokers google oidc  # skip slow msentraid
workshop run -- build all                  # default set + all brokers
workshop run -- gdm restart                # after a PAM rebuild
workshop run -- validate
workshop run -- test
workshop run -- lint
```

Bare `workshop run -- build` builds the default set
(`authd authctl pam nss`). `all` builds the default set plus all brokers.

Builds use the generated Go files already in the checkout. To update a
protobuf schema, run these commands from the repo root in `workshop shell`
or on a host with Go and `protoc` installed:

```shell
GOFLAGS=-mod=readonly go -C tools install github.com/golang/protobuf/protoc-gen-go google.golang.org/grpc/cmd/protoc-gen-go-grpc
GOFLAGS=-mod=readonly go generate ./internal/proto/authd ./pam/internal/proto ./pam/internal/gdm
```

Run `go generate ./pam/` explicitly to update the PAM Go wrapper.
Run Git commands on the host when using a linked worktree.

Refresh after changes to the base, SDK, hooks, or interfaces. Workshop reruns
`setup-base` when its snapshot is no longer valid, including after an edit
to the in-project SDK. Action edits apply without a refresh.

An applied refresh or restore keeps `authd.yaml`, broker configuration,
and broker service state. It does not keep `/var/lib/authd`, broker runtime
data, or cloud-user home directories. Back up guest data you need before
refreshing. A stop and start keeps the guest disk.

The `test` action defaults to the root Go module. To test brokers, pass
`-C brokers` as the first Go argument. Rust/NSS tests use `cargo test`.

## Configure a broker and log in

`validate` passes without a broker, but authd cannot authenticate yet.
For a client secret, use a mode `600` file under `/home/workshop` in the
guest. Create it in `workshop shell` with an editor and remove the input
file after configuration. Keep the secret out of `/project` and shell history.

Google:

```shell
workshop run -- broker configure google --client-id ID --client-secret-file /home/workshop/.authd-client-secret --ssh-suffixes '@example.com'
```

Microsoft Entra ID:

Initialize the submodule on the host before building this broker:

```shell
git submodule update --init --recursive -- brokers/third_party/libhimmelblau
```

```shell
workshop run -- broker configure msentraid --issuer URL --client-id ID --register-device --entra-auth --ssh-suffixes '@example.com'
```

`broker configure` writes the config and builds a missing binary. It starts a
broker when a complete configuration is first created, and restarts an active
broker after updates. Start a stopped broker with
`workshop run -- broker start <variant>`.

`--ssh-suffixes` allows first-time SSH logins for those email domains.
It takes a comma-separated list; replace `@example.com` with a suffix
from your test tenant.
`--register-device` (msentraid only) enables device registration.
`--entra-auth` and `--no-entra-auth` (msentraid only) set the
`[flows] entra_auth` key. The upstream template disables `entra_auth`
by default until group lookup is set up, so `--register-device` alone
does not enable the Entra flow. Use `--no-register-device` to turn
device registration off again without editing files.

### Edit broker config by hand

Prefer the `edit` verb: it opens the broker config in your editor,
backs it up, and restarts an active broker on save:

```shell
workshop run -- broker edit msentraid
workshop run --env EDITOR=vim -- broker edit msentraid  # one-shot editor
workshop run -- logs msentraid
```

To use a different editor, open `workshop shell` and run `sudoedit` there.
Broker config lives in the guest at `/etc/authd-<variant>/broker.conf`
(e.g. `/etc/authd-msentraid/broker.conf`). Broker registration lives
at `/etc/authd/brokers.d/<variant>.conf`:

```shell
workshop shell
sudoedit /etc/authd-msentraid/broker.conf
sudo systemctl restart authd-msentraid.service  # if already running
```

If it is stopped, use `workshop run -- broker start msentraid` after editing.

For the Entra flow, set both `register_device = true` and
`entra_auth = true`.

Then log in at the real greeter with the host helper. It runs on the
host only and needs direct host LXD access, which is root-equivalent.
It refuses with a clear message when the VM is stopped or the session
has no display for the SPICE viewer:

```shell
.workshop/authd/host-console.sh
```

At GDM, pick the broker, enter `user@domain`, and complete the
device-code flow. Create a local password when asked. SSH uses the
other PAM module (`pam_authd_exec.so`):

```shell
workshop run -- login user@example.com
ssh -l 'user@example.com' authd-dev.authd.wp   # use the hostname from workshop info
```

Check the new Linux account with `workshop exec -- getent passwd user@example.com`.

## Stop and start

`stop` keeps the disk. `remove` deletes guest data. Never remove a
working VM to reset state:

```shell
workshop stop
workshop info     # expect Stopped
workshop start
workshop info     # expect Ready
workshop run -- validate
```

## If a change fails

Read the failed task instead of guessing. Then check warnings:

```shell
workshop tasks <CHANGE_ID>
workshop warnings
```

A launch or refresh that pauses in `Waiting` can be continued or aborted
with `--continue` or `--abort` on the same command, for example
`workshop launch --continue` or `workshop refresh --abort`. See
[Fix workshops](https://ubuntu.com/workshop/docs/how-to/fix-workshops/debug-issues.md)
for the full recovery flow.

## Next steps

- For Workshop itself, read the [Workshop docs](https://ubuntu.com/workshop/docs/).
- For the separate libvirt GDM suite, read [`e2e-tests/TESTING.md`](../e2e-tests/TESTING.md).
