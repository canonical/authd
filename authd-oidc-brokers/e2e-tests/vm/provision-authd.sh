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
LIB_DIR="${SCRIPT_DIR}/lib"
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

# Print executed commands to ease debugging
set -x

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

# shellcheck source=lib/libprovision.sh disable=SC1091
source "${LIB_DIR}/libprovision.sh"

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
