#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
LIB_DIR="${SCRIPT_DIR}/lib"
SSH="${SCRIPT_DIR}/ssh.sh"

usage(){
    cat << EOF
Usage: $0 [--config-file <file>]

Options:
   --config-file <file>  Path to the configuration file (default: config.sh)
  -h, --help             Show this help message and exit

Provisions authd in the VM for end-to-end tests
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --config-file)
            CONFIG_FILE="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
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

# Validate config file if provided
if [ -n "${CONFIG_FILE:-}" ] && [ ! -f "${CONFIG_FILE}" ]; then
    echo "Configuration file '${CONFIG_FILE}' not found." >&2
    exit 1
fi

# Set default config file if not provided
if [ -z "${CONFIG_FILE:-}" ]; then
    CONFIG_FILE="${SCRIPT_DIR}/config.sh"
fi

# Load the configuration file (if it exists)
if [ -f "${CONFIG_FILE}" ]; then
    # shellcheck source=config.sh disable=SC1091
    source "${CONFIG_FILE}"
fi

# shellcheck source=lib/libprovision.sh
source "${LIB_DIR}/libprovision.sh"

assert_env_vars RELEASE VM_NAME_BASE BROKERS

IFS=',' read -r -a BROKER_ARRAY <<< "${BROKERS}"

ARTIFACTS_DIR="${SCRIPT_DIR}/.artifacts/${RELEASE}"

if [ -z "${VM_NAME:-}" ]; then
    VM_NAME="${VM_NAME_BASE}-${RELEASE}"
fi

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

function install_broker() {
    local broker="$1"
    local channel="$2"
    local base_snapshot="authd-${channel}-installed"
    local broker_config="${broker#authd-}.conf"

    # Get the issuer ID from the environment variable corresponding to the broker.
    # For example, for broker "authd-msentraid", we use "AUTHD_MSENTRAID_ISSUER_ID".
    local broker_prefix="${broker^^}"
    broker_prefix="${broker_prefix//-/_}"
    local issuer_id_var="${broker_prefix}_ISSUER_ID"
    local client_id_var="${broker_prefix}_CLIENT_ID"
    local client_secret_var="${broker_prefix}_CLIENT_SECRET"

    # Assert that required environment variables are set.
    # The issuer ID is optional (authd-google has a default one).
    # The client secret is also optional (authd-msentraid does not require it).
    assert_env_vars "${client_id_var}"

    local issuer_id="${!issuer_id_var:-}"
    local client_id="${!client_id_var}"
    local client_secret="${!client_secret_var:-}"

    virsh snapshot-revert "${VM_NAME}" --snapshotname "${base_snapshot}"

    # Install broker, configure and restart services
    $SSH bash -euo pipefail -s <<-EOF
        sudo snap install "${broker}" --channel="${channel}"
        sudo mkdir -p /etc/authd/brokers.d
        sudo cp /snap/${broker}/current/conf/authd/${broker_config} /etc/authd/brokers.d/
        sudo sed -i \
            -e "s|<ISSUER_ID>|${issuer_id}|g" \
            -e "s|<CLIENT_ID>|${client_id}|g" \
			-e "s|<CLIENT_SECRET>|${client_secret}|g" \
            /var/snap/${broker}/current/broker.conf
        echo 'verbosity: 2' | sudo tee /var/snap/${broker}/current/${broker}.yaml
        sudo systemctl restart authd.service
        sudo snap restart "${broker}"
	EOF

    # Reboot VM and wait until it's back
    virsh reboot "${VM_NAME}"
    wait_for_system_running

    # Snapshot this broker installation
    force_create_snapshot "${broker}-${channel}-configured"
}

function install_brokers() {
    local channel="$1"
    for index in "${!BROKER_ARRAY[@]}"; do
        install_broker "${BROKER_ARRAY[$index]}" "${channel}"
    done
}

# Print executed commands to ease debugging
set -x

# Define the VM
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    virsh define "${LIBVIRT_XML}"
fi

INITIAL_SETUP_SNAPSHOT="initial-setup"
if has_snapshot "$INITIAL_SETUP_SNAPSHOT"; then
    PRE_AUTHD_SNAPSHOT="${INITIAL_SETUP_SNAPSHOT}"
else
    PRE_AUTHD_SNAPSHOT="pre-authd-setup"
fi

if has_snapshot "$PRE_AUTHD_SNAPSHOT"; then
    restore_snapshot_and_sync_time "$PRE_AUTHD_SNAPSHOT"
else
    # Ensure the VM is running to perform initial setup
    boot_system
    # Create a pre-authd setup snapshot
    PRE_AUTHD_SNAPSHOT=""
    force_create_snapshot "$PRE_AUTHD_SNAPSHOT"
fi

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

install_brokers "stable"

# Remove the authd-stable-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-stable-installed"

# Revert to the pre-authd setup snapshot before installing authd edge
restore_snapshot_and_sync_time "$PRE_AUTHD_SNAPSHOT"

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

install_brokers "edge"

# Remove the authd-edge-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-edge-installed"
