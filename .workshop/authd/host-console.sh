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
    # Bypass the snap launcher so the host viewer inherits the real Wayland
    # runtime directory instead of the snap-specific XDG_RUNTIME_DIR.
    lxc_client=/snap/lxd/current/bin/lxc
else
    lxc_client="$(command -v lxc)" ||
        die "lxc is not installed on the host"
fi
if ! "${lxc_client}" project list >/dev/null 2>&1; then
    die "cannot access the host LXD API; check access to its local socket"
fi

# Workshop uses the username project when valid and the UID project otherwise.
lxd_project=""
for project in "workshop.$(id -un)" "workshop.$(id -u)"; do
    "${lxc_client}" project show "${project}" >/dev/null 2>&1 || continue
    mapfile -t vms < <(
        "${lxc_client}" list --project "${project}" --columns nt --format csv |
            awk -F, -v name="${VM_NAME}" \
                '$1 == name && $2 == "VIRTUAL-MACHINE" { print $1 }'
    )
    if [ "${#vms[@]}" -eq 1 ]; then
        lxd_project="${project}"
        break
    fi
done
[ -n "${lxd_project}" ] ||
    die "cannot find ${VM_NAME} in an accessible Workshop LXD project"

# The VGA console shows the GDM greeter only while the VM runs. A running
# Workshop VM reports Status READY (classic LXD says Running for the same
# state), so accept both spellings.
vm_status="$("${lxc_client}" info --project "${lxd_project}" "${VM_NAME}" 2>/dev/null | awk '/^Status:/ {print $2}')"
case "${vm_status}" in
    Running|READY) ;;
    *) die "workshop VM is ${vm_status:-unknown}; run 'workshop start' from the repo root first" ;;
esac

# remote-viewer/spicy need a graphical session. Over plain SSH there is
# no display, so lxc would fail with no useful message instead.
if [ -z "${DISPLAY:-}" ] && [ -z "${WAYLAND_DISPLAY:-}" ]; then
    die "no graphical session (DISPLAY and WAYLAND_DISPLAY are empty); run this from your desktop session or over ssh -X/-Y"
fi

exec "${lxc_client}" --project "${lxd_project}" console "${VM_NAME}" --type=vga