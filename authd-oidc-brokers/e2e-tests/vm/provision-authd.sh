#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF
Usage: $0

Options:
  -h, --help                    Show this help message and exit

Provisions authd in the VM for end-to-end tests
EOF
}

if [ -z "${VM_NAME:-}" ]; then
    echo "Missing VM_NAME environment variable. Please configure it first or run this script
    through 'provision.sh'"
    exit 1
fi

if [ -z "${RELEASE:-}" ]; then
    echo "Missing RELEASE environment variable. Please configure it first or run this script
    through 'provision.sh'"
    exit 1
fi

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
ARTIFACTS_DIR="${SCRIPT_DIR}/.artifacts/${RELEASE}"
SSH="${SCRIPT_DIR}/ssh.sh"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo >&2 "Unknown option: $1"
            exit 1
            ;;
        *)
            echo >&2 "Unexpected positional argument: $1"
            exit 1
    esac
done

function force_create_snapshot() {
    local snapshot_name="$1"
    if virsh snapshot-list "${VM_NAME}" | grep -q "${snapshot_name}"; then
        time virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "${snapshot_name}"
    fi

    if virsh domstate "${VM_NAME}" | grep -q '^running'; then
        # If the VM is running, we have to use --memspec to create the snapshot
        local memfile="${IMAGE%.qcow2}-${snapshot_name}.mem"
        time virsh snapshot-create-as --domain "${VM_NAME}" --name "${snapshot_name}" \
          --memspec "${memfile},snapshot=external"
        return
    fi

    time virsh snapshot-create-as --domain "${VM_NAME}" --name "${snapshot_name}" --disk-only
}

function restore_snapshot_and_sync_time() {
    local snapshot_name="$1"
    virsh snapshot-revert "${VM_NAME}" --snapshotname "${snapshot_name}"
    sync_time
}

function sync_time() {
    local cmd="nm-online -q && \
sudo systemctl restart systemd-timesyncd.service && \
timedatectl show -p NTPSynchronized --value | grep -q yes"
    retry --times 10 --delay 3 -- "$SSH" -- "$cmd"
}

function wait_for_system_running() {
    # Wait until we can connect via SSH
    retry --times 30 --delay 3 -- "$SSH" -- true
    # shellcheck disable=SC2016
    local cmd='output=$(systemctl is-system-running --wait) || [ $output = degraded ]'
    retry --times 3 --delay 3 -- timeout 30 -- "$SSH" -- "$cmd"
}

function reboot_system() {
    # For some reason, `virsh shutdown` sometimes doesn't cause the VM
    # to shut down, so we retry it a few times.
    local cmd="virsh shutdown \"${VM_NAME}\" && \
virsh await \"${VM_NAME}\" --condition domain-inactive --timeout 5"
    retry --times 3 --delay 1 -- sh -c "$cmd"
    virsh start "${VM_NAME}"
    wait_for_system_running
}

# Print executed commands to ease debugging
set -x

# Install required packages
sudo apt-get -y install \
    bsdutils \
    clang \
    ffmpeg \
    gir1.2-webkit2-4.1 \
    libcairo2-dev \
    libgirepository-2.0-dev \
    libvirt0 \
    libvirt-clients \
    libvirt-clients-qemu \
    libvirt-daemon \
    libvirt-daemon-driver-qemu \
    libvirt-daemon-system \
    libxkbcommon-dev \
    qemu-kvm \
    qemu-system-x86 \
    qemu-utils \
    python3-cairo \
    python3-gi \
    python3-tk \
    socat \
    xvfb


# Check if we have all required artifacts
IMAGE="${ARTIFACTS_DIR}/${VM_NAME_BASE}.qcow2"
if [ ! -f "${IMAGE}" ]; then
    echo "Image not found: ${IMAGE}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

LIBVIRT_XML="${ARTIFACTS_DIR}/${VM_NAME_BASE}.xml"
if [ ! -f "${LIBVIRT_XML}" ]; then
    echo "Libvirt XML file not found: ${LIBVIRT_XML}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

# Define the VM
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    virsh define "${LIBVIRT_XML}"
fi

# Boot the VM and wait until it's running
reboot_system

# Create a snapshot of the initial setup
force_create_snapshot "initial-setup"

# Install authd stable and create a snapshot
retry --times 3 --delay 1 -- timeout 30 -- "$SSH" -- \
  "sudo add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd"

timeout 600 -- \
    "$SSH" -- \
    'sudo apt-get install -y authd && \
     sudo mkdir -p /etc/systemd/system/authd.service.d && \
     cat <<-EOF | sudo tee /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
		EOF'

force_create_snapshot "authd-stable-installed"

# Revert to the initial setup snapshot before installing authd edge
restore_snapshot_and_sync_time "initial-setup"

# Install authd edge and create a snapshot
retry --times 3 --delay 1 -- timeout 30 -- "$SSH" -- \
  "sudo add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd-edge"

timeout 600 -- \
    "$SSH" -- \
    'sudo apt-get install -y authd && \
     sudo mkdir -p /etc/systemd/system/authd.service.d && \
     cat <<-EOF | sudo tee /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
		EOF'

force_create_snapshot "authd-edge-installed"
