# authd Workshop development environment

This Workshop shows the authd software development lifecycle in one VM.
It builds authd, PAM, and NSS from this checkout. It runs a real GDM
greeter and sshd. Use it to edit code, rebuild, validate, and log in
with a cloud identity.

Run all host commands from the repository root.

## What you get

- One Ubuntu 26.04 VM (`confinement: virtual-machine`, experimental).
- authd, `authctl`, both PAM modules, and NSS built from `/project`.
- GDM with the patched `gnome-shell`, sshd, and D-Bus wiring.
- Go 1.26 and Cargo from the pinned Workshop Store SDKs, matching `go.mod`.
- Source edits on the host appear at `/project` in the VM.
  `setup-project` rebuilds and reinstalls on every launch and refresh.
- The `workshop` user has passwordless sudo in the guest. That is
  root-equivalent inside the VM, as is host LXD access.

Actions are defined in [`.workshop.yaml`](../.workshop.yaml):

| Action | Purpose |
| --- | --- |
| `build [...]` | Rebuild and reinstall from source (bare = default set, `all` = default + brokers) |
| `broker <variant> ...\|edit\|start\|stop\|status` | Configure and run a broker |
| `validate` | Check PAM, NSS, GDM, sshd, and the socket |
| `test [--no-race]` | Run `go test -race ./...` for the root module |
| `lint` | Run the pinned `golangci-lint` wrapper |
| `logs [...]` | Show journal logs (exits; `-f` follows) |
| `login <user@domain>` | Open a login shell through sshd and PAM |
| `gdm status\|restart\|logs` | Check or restart GDM inside the VM |

Run `workshop run -- <action> --help` for full flags.

## Prerequisites

Install [Workshop](https://ubuntu.com/workshop/docs/) 0.9.7 or later.
VM support is experimental. It needs LXD `latest/edge` and one snap setting:

```shell
sudo snap refresh lxd --channel=latest/edge
sudo snap set workshop workshop.experimental-vms=1
sudo snap restart workshop.workshopd
```

You also need a SPICE viewer (`remote-viewer` or `spicy`) to open GDM.
Containers remain the Workshop default. This project uses a VM because
GDM and PAM need a full system.

## Launch

Check warnings first. Then launch and verify the change:

```shell
workshop warnings
workshop launch --wait-on-error
workshop changes
workshop tasks <CHANGE_ID>
workshop info
workshop run -- validate
```

Do not launch a `ready` workshop again. If it is `stopped`, run
`workshop start`. After every mutating command, check the same triplet:
`workshop changes`, `workshop tasks <CHANGE_ID>`, `workshop info`.

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

`build` covers source edits only. If you change `.workshop.yaml` or the
SDK hooks under `.workshop/authd/`, run `workshop refresh --wait-on-error`
instead. Refresh is non-destructive. Prefer it over remove plus launch.

`test` covers only the root Go module. Broker tests live in the
`authd-oidc-brokers` module. Rust/NSS tests use `cargo test`.

## Configure a broker and log in

`validate` passes without a broker, but authd cannot authenticate yet.
Configure one broker from source. Prefer `--client-secret-file` so the
secret stays out of shell history.

Google:

```shell
workshop run -- broker google --client-id ID --client-secret-file /project/secret --ssh-suffixes '@example.com'
```

Microsoft Entra ID:

```shell
workshop run -- broker msentraid --issuer URL --client-id ID --register-device --entra-auth --ssh-suffixes '@example.com'
```

Passing flags starts a new complete broker and restarts an active one
to apply the change. A stopped broker stays stopped; start it with
`workshop run -- broker start <variant>`. The secret path is inside the
VM.

`--ssh-suffixes` allows first-time SSH logins for that email domain.
Replace `@example.com` with a suffix from your test tenant.
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

Without a terminal (or to use other tools), edit the file directly.
Broker config lives in-guest at `/etc/authd-<variant>/broker.conf`
(e.g. `/etc/authd-msentraid/broker.conf`). Broker registration lives
at `/etc/authd/brokers.d/<variant>.conf`:

```shell
workshop exec -- sudoedit /etc/authd-msentraid/broker.conf
# or: workshop shell, then sudoedit the same path
workshop run -- broker start msentraid
workshop run -- broker status msentraid
workshop run -- logs msentraid
```

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
ssh 'user@example.com'@authd-dev.authd.wp   # hostname is in workshop info
```

Check the new Linux account with `workshop exec -- getent passwd user@example.com`.

## VM size

LXD boots this VM with about 1 GiB of RAM. That is too small for cold
builds. Use 8 GiB of RAM and a 30 GiB root disk. Set both while the
workshop is stopped:

```shell
instance="authd-dev-$(cat .workshop.lock)"
# Workshop uses workshop.USERNAME when valid, else workshop.UID.
for project in "workshop.$(id -un)" "workshop.$(id -u)"; do
  lxc info --project "$project" "$instance" >/dev/null 2>&1 && break
done
workshop stop
lxc config set --project "$project" "$instance" limits.memory=8GiB
lxc config device set --project "$project" "$instance" root size=30GiB
workshop start
```

Reapply these values if a refresh resets them.

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

A refresh that pauses in `Waiting` can be continued or aborted with
`workshop refresh --continue` or `workshop refresh --abort`. See
[Fix workshops](https://ubuntu.com/workshop/docs/how-to/fix-workshops/debug-issues.md)
for the full recovery flow.

## Next steps

- For Workshop itself, read the [Workshop docs](https://ubuntu.com/workshop/docs/).
- For the separate libvirt GDM suite, read [`e2e-tests/TESTING.md`](../e2e-tests/TESTING.md).
