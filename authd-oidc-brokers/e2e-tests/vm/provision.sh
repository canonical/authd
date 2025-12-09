#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF
Usage: $0 --ssh-public-key <file> --issuer-id <id> --client-id <id> --user <name> --release <release>
       $0 -k <file> -i <id> -c <id> -u <name> -r <release>

Options:
  -k, --ssh-public-key <file>   Path to the SSH public key file to be added to the VM
  -i, --issuer-id <id>          OIDC Issuer ID for broker configuration
  -c, --client-id <id>          OIDC Client ID for broker configuration
  -u, --user <name>             Username used for authd login in the tests
  -r, --release <release>       Ubuntu release for the VM (e.g., 'questing')
  --force                       Force provisioning: remove existing VM and artifacts and create a fresh VM
  -h, --help                    Show this help message and exit

Provisions the VM for end-to-end tests
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -k|--ssh-public-key)
            SSH_PUBLIC_KEY_FILE="$2"
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
        -u|--user)
            AUTHD_USER="$2"
            shift 2
            ;;
        -r|--release)
            RELEASE="$2"
            shift 2
            ;;
        --force)
            FORCE=true
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

if [ -z "${SSH_PUBLIC_KEY_FILE:-}" ] || \
   [ -z "${ISSUER_ID:-}" ] || \
   [ -z "${CLIENT_ID:-}" ] || \
   [ -z "${AUTHD_USER:-}" ] || \
   [ -z "${RELEASE:-}" ]; then
   echo "Missing required arguments." >&2
   usage
   exit 1
fi

# Validate SSH public key file
if [ ! -f "${SSH_PUBLIC_KEY_FILE}" ]; then
    echo "SSH public key file not found: ${SSH_PUBLIC_KEY_FILE}"
    exit 1
fi

if [[ "${SSH_PUBLIC_KEY_FILE}" != *.pub ]]; then
    echo "SSH public key file must have a .pub extension"
    exit 1
fi

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CLOUD_INIT_TEMPLATE="${SCRIPT_DIR}/cloud-init-template-${RELEASE}.yaml"
LIBVIRT_XML_TEMPLATE="${SCRIPT_DIR}/e2e-runner-template.xml"
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/authd-e2e-tests"
ARTIFACTS_DIR="${SCRIPT_DIR}/.artifacts/${RELEASE}"
SSH="${SCRIPT_DIR}/ssh.sh"

VM_NAME_BASE="e2e-runner"
VM_NAME="${VM_NAME_BASE}-${RELEASE}"
BROKERS=("authd-msentraid")

# Installing all the packages can take some time, so we set the timeout to 15 minutes
CLOUT_INIT_TIMEOUT=900

# The RELEASE variable is used by the ssh.sh script
export RELEASE

function cloud_init_finished() {
    local image=$1
    sudo guestfish --ro -a "${image}" -i stat /var/lib/cloud/instance/boot-finished &>/dev/null
}

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

function install_brokers() {
    local channel="$1"
    local base_snapshot="authd-${channel}-installed"
    local broker

    for broker in "${BROKERS[@]}"; do
        local broker_config="${broker#authd-}.conf"

        # Install broker, configure and restart services
        $SSH -- bash -euo pipefail -s <<-EOF
			sudo snap install "${broker}" --channel="${channel}"
			sudo mkdir -p /etc/authd/brokers.d
			sudo cp /snap/${broker}/current/conf/authd/${broker_config} /etc/authd/brokers.d/
			sudo sed -i \
		  		-e "s|<ISSUER_ID>|${ISSUER_ID}|g" \
		  		-e "s|<CLIENT_ID>|${CLIENT_ID}|g" \
		  		-e "s|#ssh_allowed_suffixes_first_auth =|ssh_allowed_suffixes_first_auth = ${AUTHD_USER}|g" \
		  		/var/snap/${broker}/current/broker.conf
			echo 'verbosity: 2' | sudo tee /var/snap/${broker}/current/${broker}.yaml
			sudo systemctl restart authd.service
			sudo snap restart "${broker}"
		EOF

        # Reboot VM and wait until it's back
        reboot_system

        # Snapshot this broker installation
        force_create_snapshot "${broker}-${channel}-configured"

        # If not the last broker, revert to the base authd snapshot for this channel
        if [ "${broker}" != "${BROKERS[-1]}" ]; then
            restore_snapshot_and_sync_time "${base_snapshot}"
        fi
    done
}

# Print executed commands to ease debugging
set -x

# Install required packages
sudo apt-get -y install \
    bsdutils \
    clang \
    cloud-image-utils \
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
    sshpass \
    wget \
    xvfb

# Download the image
IMAGE_URL="https://cloud-images.ubuntu.com/${RELEASE}/current/${RELEASE}-server-cloudimg-amd64.img"
SOURCE_IMAGE="${CACHE_DIR}/$(basename "${IMAGE_URL}")"
if [ ! -f "${SOURCE_IMAGE}" ]; then
    mkdir -p "${CACHE_DIR}"
    wget -O "${SOURCE_IMAGE}" "${IMAGE_URL}"
else
    echo "Source image already exists: ${SOURCE_IMAGE}"
fi

if [ "${FORCE:-}" = true ]; then
    echo "Force provisioning enabled. Removing existing VM and artifacts."

    # Destroy and undefine the VM if it exists
    if virsh dominfo "${VM_NAME}" &> /dev/null; then
        if virsh domstate "${VM_NAME}" | grep -q '^running'; then
            virsh destroy "${VM_NAME}"
        fi
        virsh undefine "${VM_NAME}" --snapshots-metadata
    fi

    # Remove artifacts of this VM
    rm -rf "${ARTIFACTS_DIR}"
fi

# Copy and resize the image
IMAGE="${ARTIFACTS_DIR}/${VM_NAME_BASE}.qcow2"
if [ ! -f "${IMAGE}" ]; then
    mkdir -p "${ARTIFACTS_DIR}"
    cp "${SOURCE_IMAGE}" "${IMAGE}"

    qemu-img resize "${IMAGE}" 10G
else
    echo "Image already exists: ${IMAGE}"
fi

# Create a cloud-init ISO
CLOUD_INIT_ISO="${ARTIFACTS_DIR}/seed.iso"
if [ ! -f "${CLOUD_INIT_ISO}" ]; then
    CLOUD_INIT_DIR="$(mktemp -d)"
    trap 'rm -rf ${CLOUD_INIT_DIR}' EXIT

    SSH_PUBLIC_KEY=$(cat "${SSH_PUBLIC_KEY_FILE}") \
        envsubst < "${CLOUD_INIT_TEMPLATE}" > "${CLOUD_INIT_DIR}/user-data"

    cloud-localds "${CLOUD_INIT_ISO}" "${CLOUD_INIT_DIR}/user-data"
else
    echo "Cloud-init ISO already exists: ${CLOUD_INIT_ISO}"
fi

# Create the libvirt XML
LIBVIRT_XML="${ARTIFACTS_DIR}/${VM_NAME_BASE}.xml"
if [ ! -f "${LIBVIRT_XML}" ]; then
    IMAGE_FILE=${IMAGE} \
    VM_NAME=${VM_NAME} \
      envsubst < "${LIBVIRT_XML_TEMPLATE}" > "${LIBVIRT_XML}"
else
    echo "Libvirt XML file already exists: ${LIBVIRT_XML}"
fi

# Define the VM
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    virsh define "${LIBVIRT_XML}"
else
    echo "VM already defined: ${VM_NAME}"
fi

# Attach the cloud-init ISO
if ! virsh domblklist "${VM_NAME}" | grep -q "seed.iso"; then
    virsh attach-disk "${VM_NAME}" "${CLOUD_INIT_ISO}" vdb --targetbus virtio --type disk --mode readonly --config
else
    echo "Cloud-init ISO already attached to VM: ${VM_NAME}"
fi

# Ensure the VM is shut off before proceeding
if virsh domstate "${VM_NAME}" | grep -q '^running'; then
    virsh destroy "${VM_NAME}"
fi

# Start the VM and wait for it to finish the initial setup
if ! cloud_init_finished "${IMAGE}"; then
    # Start the VM to let cloud-init do its work
    virsh start "${VM_NAME}"

    # Print the console output and wait until cloud-init has finished and the VM has shut down
    echo "Waiting for VM to finish cloud-init setup..."
    script -q -e -f /dev/null -c "virsh console $VM_NAME" &
    VM_CONSOLE_PID=$!
    virsh await "${VM_NAME}" --condition domain-inactive --timeout "${CLOUT_INIT_TIMEOUT}"
    kill "${VM_CONSOLE_PID}" || true

    # Detach the cloud-init iSO
    virsh detach-disk "${VM_NAME}" vdb --config

    # Boot the VM and wait until it's running
    virsh start "${VM_NAME}"
    wait_for_system_running

    # Create a snapshot of the initial setup
    force_create_snapshot "initial-setup"
else
    echo "Cloud-init has already finished."
    restore_snapshot_and_sync_time "initial-setup"
fi

# Install authd stable and create a snapshot
retry --times 3 --delay 1 -- timeout 30 -- "$SSH" -- \
  "sudo add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd"
timeout 600 -- \
    "$SSH" -- \
    "sudo apt-get install -y authd && \
     sudo mkdir -p /etc/systemd/system/authd.service.d && \
     cat <<-EOF | sudo tee /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
		EOF"
force_create_snapshot "authd-stable-installed"

install_brokers "stable"

# Remove the authd-stable-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-stable-installed"

# Revert to the initial setup snapshot before installing authd edge
restore_snapshot_and_sync_time "initial-setup"

# Install authd edge and create a snapshot
retry --times 3 --delay 1 -- timeout 30 -- "$SSH" -- \
  "sudo add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd-edge"
timeout 600 -- \
    "$SSH" -- \
    "sudo apt-get install -y authd && \
     sudo mkdir -p /etc/systemd/system/authd.service.d && \
     cat <<-EOF | sudo tee /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
		EOF"
force_create_snapshot "authd-edge-installed"

install_brokers "edge"

# Remove the authd-edge-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-edge-installed"
