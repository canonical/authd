#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF
Usage: $0 --issuer-id <id> --client-id <id> --user <name>
       $0 -i <id> -c <id> -u <name>

Options:
  -b, --broker <name>           Name of the broker to install (e.g., authd-msentraid)
  -i, --issuer-id <id>          OIDC Issuer ID for broker configuration
  -c, --client-id <id>          OIDC Client ID for broker configuration
  -s, --client-secret <secret>  OIDC Client Secret for broker configuration
  --delete-snapshots            Delete intermediate snapshots after provisioning
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
        -b|--broker)
            BROKER="$2"
            shift 2
            ;;
        -i|--issuer-id)
            ISSUER_ID="$2"
            shift 2
            ;;
        -c|--client-id)
            CLIENT_ID="$2"
            shift 2
            ;;
        -s|--client-secret)
            CLIENT_SECRET="$2"
            shift 2
            ;;
        --delete-snapshots)
            DELETE_SNAPSHOTS=true
            shift
            ;;
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

if [ -z "${BROKER:-}" ] || \
   ([ -z "${ISSUER_ID:-}" ] && [ -z "${CLIENT_SECRET:-}" ]) || \
   [ -z "${CLIENT_ID:-}" ]; then
   echo "Missing required arguments." >&2
   usage
   exit 1
fi

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

function install_broker() {
    local channel="$1"
    local base_snapshot="authd-${channel}-installed"
    local broker_config="${BROKER#authd-}.conf"

    virsh snapshot-revert "${VM_NAME}" --snapshotname "${base_snapshot}"

    # Install broker, configure and restart services
    $SSH bash -euo pipefail -s <<-EOF
        sudo snap install "${BROKER}" --channel="${channel}"
        sudo mkdir -p /etc/authd/brokers.d
        sudo cp /snap/${BROKER}/current/conf/authd/${broker_config} /etc/authd/brokers.d/
        sudo sed -i \
            -e "s|<ISSUER_ID>|${ISSUER_ID}|g" \
            -e "s|<CLIENT_ID>|${CLIENT_ID}|g" \
			-e "s|<CLIENT_SECRET>|${CLIENT_SECRET}|g" \
            /var/snap/${BROKER}/current/broker.conf
        echo 'verbosity: 2' | sudo tee /var/snap/${BROKER}/current/${BROKER}.yaml
        sudo systemctl restart authd.service
        sudo snap restart "${BROKER}"
	EOF

    # Reboot VM and wait until it's back
    virsh reboot "${VM_NAME}"
    wait_for_system_running

    # Snapshot this broker installation
    force_create_snapshot "${BROKER}-${channel}-configured"
}

# Print executed commands to ease debugging
set -x

IMAGE="${ARTIFACTS_DIR}/${VM_NAME_BASE}.qcow2"
if [ ! -f "${IMAGE}" ]; then
    echo "Image not found: ${IMAGE}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

# Check if the VM is already defined
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    echo "Domain not found: ${VM_NAME}. Please run provision-ubuntu.sh or provision-authd.sh first."
    exit 1
fi

install_broker "stable"
# Remove the authd-stable-installed snapshot which is no longer needed
if [ ! -z "${DELETE_SNAPSHOTS:-}" ]; then
	virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-stable-installed"
fi

install_broker "edge"
# Remove the authd-edge-installed snapshot which is no longer needed
if [ ! -z "${DELETE_SNAPSHOTS:-}" ]; then
	virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-edge-installed"
fi
