#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
LIB_DIR="${SCRIPT_DIR}/lib"
SSH="${SCRIPT_DIR}/ssh.sh"
LIBVIRT_XML_TEMPLATE="${SCRIPT_DIR}/e2e-runner-template.xml"
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/authd-e2e-tests"

usage(){
    cat << EOF
Usage: $0 [--config-file <file>] [--force]

Options:
   --config-file <file>  Path to the configuration file (default: config.sh)
   --force               Force provisioning: remove existing VM and artifacts and create a fresh VM
   --no-snapshot         Do not create a snapshot after initial setup
  -h, --help             Show this help message and exit

Provisions the VM for end-to-end tests
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
        --no-snapshot)
            NO_SNAPSHOT=true
            shift
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

assert_env_vars RELEASE VM_NAME_BASE SSH_PUBLIC_KEY_FILE

# Validate SSH public key file
if [ ! -f "${SSH_PUBLIC_KEY_FILE}" ]; then
    echo "SSH public key file not found: ${SSH_PUBLIC_KEY_FILE}"
    exit 1
fi

if [[ "${SSH_PUBLIC_KEY_FILE}" != *.pub ]]; then
    echo "SSH public key file must have a .pub extension"
    exit 1
fi

# Cache sudo password early
sudo -v

# Installing all the packages can take some time, so we set the timeout to 15 minutes
CLOUT_INIT_TIMEOUT=900

ARTIFACTS_DIR="${SCRIPT_DIR}/.artifacts/${RELEASE}"
CLOUD_INIT_TEMPLATE="${SCRIPT_DIR}/cloud-init-template-${RELEASE}.yaml"

if [ -z "${VM_NAME:-}" ]; then
    VM_NAME="${VM_NAME_BASE}-${RELEASE}"
fi

function cloud_init_finished() {
    local image=$1
    sudo guestfish --ro -a "${image}" -i stat /var/lib/cloud/instance/boot-finished &>/dev/null
}

# Print executed commands to ease debugging
set -x

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

    # Detach the cloud-init ISO
    virsh detach-disk "${VM_NAME}" vdb --config

    if [ -z "${NO_SNAPSHOT:-}" ]; then
        boot_system
        # Create a snapshot of the initial setup
        force_create_snapshot "initial-setup"
    fi
else
    echo "Cloud-init has already finished."
    restore_snapshot_and_sync_time "initial-setup"
fi
