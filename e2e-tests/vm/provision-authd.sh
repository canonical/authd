#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
LIB_DIR="${SCRIPT_DIR}/lib"
SSH="${SCRIPT_DIR}/ssh.sh"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/authd-e2e-tests"

usage(){
    cat << EOF
Usage: $0 [--config-file <file>] [--authd-deb <deb>] [--broker-snap <snap>]

Options:
   --config-file <file>  Path to the configuration file (default: config.sh)
   --force              Force installation of authd and brokers even if snapshots already exist.
                        The existing snapshots will be deleted and recreated with the new installation.
   --broker <broker>    The broker to install ("authd-google", "authd-msentraid", ...)
   --authd-deb <deb>    Path to the authd deb file to install (default: install from the edge PPA)
   --broker-snap <snap> Path to the broker snap file to install (default: install from the edge channel)
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
        --force)
            FORCE=true
            shift
            ;;
        --broker)
            BROKER="$2"
            shift 2
            ;;
        --authd-deb)
            AUTHD_DEB="$2"
            shift 2
            ;;
        --broker-snap)
            BROKER_SNAP="$2"
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

# Validate --authd-deb if provided
if [ -n "${AUTHD_DEB:-}" ] && [ ! -f "${AUTHD_DEB}" ]; then
    echo "authd deb file '${AUTHD_DEB}' not found." >&2
    exit 1
fi

# Validate --broker-snap if provided
if [ -n "${BROKER_SNAP:-}" ] && [ ! -f "${BROKER_SNAP}" ]; then
    echo "Broker snap file '${BROKER_SNAP}' not found." >&2
    exit 1
fi

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

assert_env_vars RELEASE VM_NAME_BASE BROKER

ARTIFACTS_DIR="${ARTIFACTS_DIR:-${DATA_DIR}/${RELEASE}}"

if [ -z "${VM_NAME:-}" ]; then
    VM_NAME="${VM_NAME_BASE}-${RELEASE}"
fi

# Check if we have all required artifacts
IMAGE="${ARTIFACTS_DIR}/${VM_NAME}.qcow2"
if [ ! -f "${IMAGE}" ]; then
    echo "Image not found: ${IMAGE}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

LIBVIRT_XML="${ARTIFACTS_DIR}/${VM_NAME_BASE}.xml"
if [ ! -f "${LIBVIRT_XML}" ]; then
    echo "Libvirt XML file not found: ${LIBVIRT_XML}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

INITIAL_SETUP_SNAPSHOT="initial-setup"
AUTHD_STABLE_SNAPSHOT="authd-stable-installed"
BROKER_STABLE_SNAPSHOT="${BROKER}-stable-installed"
AUTHD_SNAPSHOT="authd-installed"
BROKER_SNAPSHOT="${BROKER}-installed"

function install_broker() {
    local broker="$1"
    local channel
    local snap_file

    case "$2" in
        --channel)
            channel="$3"
            ;;
        --snap)
            snap_file="$3"
            ;;
    esac

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

    if [ -n "${snap_file:-}" ]; then
        # Copy the local snap to the VM and install it
        local remote_snap
        remote_snap="/home/ubuntu/$(basename "${snap_file}")"
        scp_to_vm "${snap_file}" "${remote_snap}"
        $SSH snap install --dangerous "${remote_snap}"
    else
        # Install the snap from the specified channel
        $SSH snap install "${broker}" --channel="${channel}"
    fi

    # Configure broker and restart services
    $SSH bash -euo pipefail -s <<-EOF
		cp /snap/${broker}/current/conf/authd/${broker_config} /etc/authd/brokers.d/
		sed -i \
			-e "s|<ISSUER_ID>|${issuer_id}|g" \
			-e "s|<CLIENT_ID>|${client_id}|g" \
			-e "s|<CLIENT_SECRET>|${client_secret}|g" \
			/var/snap/${broker}/current/broker.conf
		echo 'verbosity: 2' > /var/snap/${broker}/current/${broker}.yaml
		systemctl restart authd.service
		snap restart "${broker}"
	EOF

    # Reboot VM and wait until it's back
    virsh reboot "${VM_NAME}"
    wait_for_system_running
}

function scp_to_vm() {
    local local_path="$1"
    local remote_path="$2"
    local cid
    cid=$(virsh dumpxml "${VM_NAME}" | \
          xmllint --xpath 'string(//vsock/cid/@address)' -)
    scp \
      -o ProxyCommand="socat - VSOCK-CONNECT:${cid}:22" \
      -o UserKnownHostsFile=/dev/null \
      -o StrictHostKeyChecking=no \
      -o LogLevel=ERROR \
      "${local_path}" "root@localhost:${remote_path}"
}

# Print executed commands to ease debugging
set -x

# Define the VM
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    virsh define "${LIBVIRT_XML}"
fi

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
    force_create_snapshot "$PRE_AUTHD_SNAPSHOT"
fi

if [ -z "${FORCE:-}" ] && has_snapshot "${AUTHD_STABLE_SNAPSHOT}"; then
    restore_snapshot_and_sync_time "${AUTHD_STABLE_SNAPSHOT}"
else
    # Install authd stable and create a snapshot
    PPA="ubuntu-enterprise-desktop/authd"
    # Launchpad is sometimes slow to respond, so we add retries to avoid
    # transient failures in the tests.
    cmd="add-apt-repository -y ppa:${PPA}"
    retry --times 5 --delay 3 -- "$SSH" -- "$cmd"
    $SSH "apt-get install -y authd"
    force_create_snapshot "${AUTHD_STABLE_SNAPSHOT}"
fi

if [ -z "${FORCE:-}" ] && has_snapshot "${BROKER_STABLE_SNAPSHOT}"; then
    restore_snapshot_and_sync_time "${BROKER_STABLE_SNAPSHOT}"
else
    install_broker "${BROKER}" --channel "stable"
    # Snapshot this broker installation
    force_create_snapshot "${BROKER_STABLE_SNAPSHOT}"
fi

# Remove the authd-stable-installed snapshot which is no longer needed
# virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-stable-installed"

# Revert to the pre-authd setup snapshot before installing the version to test
restore_snapshot_and_sync_time "$PRE_AUTHD_SNAPSHOT"

# Add the edge PPA. We also need that when installing authd from a deb file,
# because it depends on gnome-shell from the edge PPA.
PPA="ubuntu-enterprise-desktop/authd-edge"
$SSH "add-apt-repository -y ppa:${PPA}"

# Configure authd to be verbose. We do this before installing authd to avoid
# having to restart the service after installation (just a simple optimization).
$SSH bash -euo pipefail -s <<-EOF
    mkdir -p /etc/systemd/system/authd.service.d
    cat <<-UNIT > /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
	UNIT
EOF

# Install the version of authd to test
if [ -n "${AUTHD_DEB:-}" ]; then
    scp_to_vm "${AUTHD_DEB}" "/home/ubuntu/$(basename "${AUTHD_DEB}")"
    $SSH apt-get install -y "/home/ubuntu/$(basename "${AUTHD_DEB}")"
else
    $SSH "apt-get install -y authd"
fi

# Configure the PAM module to be verbose as well
$SSH bash -euo pipefail -s <<-EOF
    sed -i '/pam_authd_exec\.so\|pam_authd\.so/ s/$/ debug=true/' /etc/pam.d/*
EOF

force_create_snapshot "${AUTHD_SNAPSHOT}"

# Install the brokers for the version of authd to test.
if [ -n "${BROKER_SNAP:-}" ]; then
    install_broker "${BROKER}" --snap "${BROKER_SNAP}"
else
    install_broker "${BROKER}" --channel "edge"
fi

force_create_snapshot "${BROKER_SNAPSHOT}"

# Remove the authd-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "${AUTHD_SNAPSHOT}"
