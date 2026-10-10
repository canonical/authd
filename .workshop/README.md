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
- Go 1.26 from the pinned Workshop Store SDK and Cargo from its `latest/stable` channel.
- Source edits on the host appear at `/project` in the VM.
  `setup-project` rebuilds and reinstalls on every launch and every applied refresh.
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

Install [Workshop](https://ubuntu.com/workshop/docs/) 0.9.7 or later.
VM support is experimental. It needs LXD `latest/edge` and one snap setting.
If LXD is missing, the commands install it; otherwise they refresh it.
The host must also provide hardware virtualization.

```shell
if snap list lxd >/dev/null 2>&1; then
  sudo snap refresh lxd --channel=latest/edge
else
  sudo snap install lxd --channel=latest/edge
fi
sudo snap set workshop workshop.experimental-vms=1
sudo snap restart workshop.workshopd
```

You also need a SPICE viewer (`remote-viewer` or `spicy`) to open GDM.
Containers remain the Workshop default. This project uses a VM because
GDM and PAM need a full system.

## VM size

LXD boots this VM with about 1 GiB of RAM. That is too small for the cold
Go and Rust builds `setup-project` runs. The Workshop definition cannot set
VM memory or root disk size.

For a fresh checkout, create the VM without the project SDK, resize it, then
add the SDK so its first cold build runs at the larger size. Temporarily remove
`- name: project-authd` from `sdks:` in `.workshop.yaml`; keep `rust` and `go`.
Do not commit this temporary edit.
After each Workshop mutation below, check its change with the same triplet:
`workshop changes`, `workshop tasks <CHANGE_ID>`, and `workshop info`.

Launch and verify the toolchain-only VM:

```shell
workshop warnings
workshop launch --wait-on-error
workshop changes
workshop tasks <CHANGE_ID>
workshop info
```

Stop it:

```shell
workshop stop
```

Set the VM resources while it is stopped. Workshop uses the username LXD
project when valid, else the UID project:

```shell
instance="authd-dev-$(cat .workshop.lock)"
project=""
for candidate in "workshop.$(id -un)" "workshop.$(id -u)"; do
  if lxc --force-local info --project "$candidate" "$instance" >/dev/null 2>&1; then
    project="$candidate"
    break
  fi
done
if [ -z "$project" ]; then
  echo "Cannot find ${instance}; check LXD project access before continuing." >&2
else
  lxc --force-local config set --project "$project" "$instance" limits.memory=8GiB &&
    lxc --force-local config device set --project "$project" "$instance" root size=30GiB
fi
```

Start the VM:

```shell
workshop start
```

Restore `- name: project-authd` in `.workshop.yaml`, then apply it to the
sized VM:

```shell
workshop refresh --wait-on-error
workshop run -- validate
```

The new SDK's `setup-base` and `setup-project` hooks run during that refresh.
Reapply the resource settings if a later refresh resets them.

## Launch

For a fresh checkout, follow the staged first-launch steps in
[VM size](#vm-size). Do not launch with `project-authd` enabled before the VM
is resized.

Do not launch a `ready` workshop again. If it is `stopped`, run
`workshop start`.

An existing `authd-dev` guest from before these SDK state hooks has no old
`save-state` hook. Its first refresh cannot preserve `authd.yaml`, broker
configuration, or broker service state; back those up before refreshing.
Fresh launches are unaffected.

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

`build` covers source edits only. Refresh after base/SDK/interface changes or
`setup-project`/state-hook edits. Action edits apply immediately. Use
`workshop refresh --wait-on-error` for refreshable changes. `setup-base` does
not rerun on refresh; back up guest state before recreating the workshop.

`test` covers only the root Go module. Broker tests live in the
`authd-oidc-brokers` module. Rust/NSS tests use `cargo test`.

## Configure a broker and log in

`validate` passes without a broker, but authd cannot authenticate yet.
Use `--client-secret-file` to keep secrets out of shell history. The example
file is Git-ignored; create it with mode `600`.

Google:

```shell
workshop run -- broker configure google --client-id ID --client-secret-file /project/.authd-client-secret --ssh-suffixes '@example.com'
```

Microsoft Entra ID:

```shell
workshop run -- broker configure msentraid --issuer URL --client-id ID --register-device --entra-auth --ssh-suffixes '@example.com'
```

`broker configure` writes the config and builds a missing binary. It starts a
broker when a complete configuration is first created, and restarts an active
broker after updates. A stopped broker stays stopped; start it with
`workshop run -- broker start <variant>`. The secret example stays on the host
and is Git-ignored.

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
