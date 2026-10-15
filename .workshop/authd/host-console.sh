#!/bin/bash
set -euo pipefail

# Open the workshop VM's graphical console (the GDM greeter) from the host.
#
# This runs on the HOST, not inside the workshop: the SPICE viewer is a host
# GUI program. Workshop manages the VM on the host LXD daemon; this script
# only attaches to its virtual console.

readonly WORKSHOP_NAME=authd-dev

die() {
    echo "host-console.sh: $*" >&2
    exit 1
}

# The lock identifies this checkout's Workshop project, even in a worktree.
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "${script_dir}/../.." && pwd)"
if [ ! -r "${project_dir}/.workshop.lock" ]; then
    die "Workshop project lock file is missing; launch the workshop first"
fi
project_id="$(<"${project_dir}/.workshop.lock")"
[ -n "${project_id}" ] || die "Workshop project lock file is empty"
readonly VM_NAME="${WORKSHOP_NAME}-${project_id}"

[ "$#" -eq 0 ] || die "this command does not accept arguments"
command -v remote-viewer >/dev/null 2>&1 ||
    command -v spicy >/dev/null 2>&1 ||
    die "no SPICE viewer found; install virt-viewer or spice-client-gtk"

if [ -x /snap/lxd/current/bin/lxc ]; then
    # Keep the LXD snap's client configuration while running outside snap
    # confinement so the SPICE viewer inherits the host display environment.
    lxd_snap_common=/var/snap/lxd/common
    lxd_snap_user_common="${HOME}/snap/lxd/common"
    if [ -z "${LXD_DIR:-}" ]; then
        LXD_DIR="${lxd_snap_common}/lxd/"
        if [ ! -w "${lxd_snap_common}/lxd/unix.socket" ] &&
            [ -w "${lxd_snap_common}/lxd-user/unix.socket" ]; then
            LXD_DIR="${lxd_snap_common}/lxd-user/"
        fi
        export LXD_DIR
    fi
    if [ -z "${LXD_CONF:-}" ]; then
        LXD_CONF="${lxd_snap_user_common}/config"
        export LXD_CONF
    fi
    if [ -z "${LXD_GLOBAL_CONF:-}" ]; then
        LXD_GLOBAL_CONF="${lxd_snap_common}/global-conf/"
        export LXD_GLOBAL_CONF
    fi
    mkdir -p "${LXD_CONF}"
    lxc_client=/snap/lxd/current/bin/lxc
else
    lxc_client="$(command -v lxc)" ||
        die "lxc is not installed on the host"
fi
if ! "${lxc_client}" --force-local project list >/dev/null 2>&1; then
    die "cannot access the host LXD API; check access to its local socket"
fi

# Workshop uses the username project when valid and the UID project otherwise.
lxd_project=""
for project in "workshop.$(id -un)" "workshop.$(id -u)"; do
    if "${lxc_client}" --force-local list --project "${project}" \
        --columns nt --format csv 2>/dev/null | grep -Fx "${VM_NAME},VIRTUAL-MACHINE" >/dev/null; then
        lxd_project="${project}"
        break
    fi
done
[ -n "${lxd_project}" ] ||
    die "cannot find ${VM_NAME} in an accessible Workshop LXD project"

# LXD reports instance states in uppercase, including RUNNING.
if ! vm_status="$("${lxc_client}" --force-local info --project "${lxd_project}" "${VM_NAME}" | awk '/^Status:/ {print $2}')"; then
    die "cannot query status for ${VM_NAME} from the host LXD daemon"
fi
case "${vm_status^^}" in
    RUNNING|READY) ;;
    *) die "workshop VM is ${vm_status:-unknown}; run 'workshop start' from the repo root first" ;;
esac

# remote-viewer/spicy need a graphical session. Over plain SSH there is
# no display, so lxc would fail with no useful message instead.
if [ -z "${DISPLAY:-}" ] && [ -z "${WAYLAND_DISPLAY:-}" ]; then
    die "no graphical session (DISPLAY and WAYLAND_DISPLAY are empty); run this from your desktop session or over ssh -X/-Y"
fi

exec "${lxc_client}" --force-local --project "${lxd_project}" console "${VM_NAME}" --type=vga