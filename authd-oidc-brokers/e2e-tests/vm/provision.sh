#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF
Usage: $0 --ssh-public-key <file> --brokers <name1,...> --issuer-ids <id1,...> --client-ids <id1,...> --client-secrets <secret1,...> --user <name> --release <release>
       $0 -k <file> -i <id> -c <id> -u <name> -r <release>

Options:
  -k, --ssh-public-key <file>    Path to the SSH public key file to be added to the VM
  -b, --brokers <name>           Comma-separated names of the brokers to install (e.g., authd-msentraid)
  -i, --issuer-ids <id>          Comma-separated list of OIDC Issuer IDs for broker configuration. Use '-' for brokers that do not require an issuer.
  -c, --client-ids <id>          Comma-separated list of OIDC Client IDs for broker configuration.
  -s, --client-secrets <secret>  Comma-separated list of OIDC Client Secrets for broker configuration. Use '-' for brokers that do not require a secret.
  -r, --release <release>        Ubuntu release for the VM (e.g., 'questing')
  --force                        Force provisioning: remove existing VM and artifacts and create a fresh VM
  -h, --help                     Show this help message and exit

Provisions the VM for end-to-end tests
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -k|--ssh-public-key)
            SSH_PUBLIC_KEY_FILE="$2"
            shift 2
            ;;
        -b|--brokers)
            BROKERS="$2"
            shift 2
            ;;
        -i|--issuer-ids)
            ISSUER_IDS="$2"
            shift 2
            ;;
        -c|--client-ids)
            CLIENT_IDS="$2"
            shift 2
            ;;
        -s|--client-secrets)
            CLIENT_SECRETS="$2"
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
   [ -z "${BROKERS:-}" ] || \
   [ -z "${ISSUER_IDS:-}" ] || \
   [ -z "${CLIENT_IDS:-}" ] || \
   [ -z "${CLIENT_SECRETS:-}" ] || \
   [ -z "${RELEASE:-}" ]; then
   echo "Missing required arguments." >&2
   usage
   exit 1
fi

IFS=',' read -r -a BROKER_ARRAY <<< "${BROKERS}"

IFS=',' read -r -a ISSUER_ID_ARRAY <<< "${ISSUER_IDS}"
if [ "${#BROKER_ARRAY[@]}" -ne "${#ISSUER_ID_ARRAY[@]}" ]; then
    echo "The number of brokers must match the number of issuer IDs." >&2
    exit 1
fi

IFS=',' read -r -a CLIENT_ID_ARRAY <<< "${CLIENT_IDS}"
if [ "${#BROKER_ARRAY[@]}" -ne "${#CLIENT_ID_ARRAY[@]}" ]; then
    echo "The number of brokers must match the number of issuer IDs." >&2
    exit 1
fi

IFS=',' read -r -a CLIENT_SECRET_ARRAY <<< "${CLIENT_SECRETS}"
if [ "${#BROKER_ARRAY[@]}" -ne "${#CLIENT_SECRET_ARRAY[@]}" ]; then
    echo "The number of brokers must match the number of issuer IDs." >&2
    exit 1
fi


# Print executed commands to ease debugging
set -x

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"

export VM_NAME_BASE="e2e-runner"
export VM_NAME="${VM_NAME_BASE}-${RELEASE}"
export RELEASE

# Provision the VM with Ubuntu
"${SCRIPT_DIR}/provision-ubuntu.sh" \
    --ssh-public-key "${SSH_PUBLIC_KEY_FILE}" \
    --release "${RELEASE}" \
    ${FORCE:+--force}

# Provision authd in the VM
"${SCRIPT_DIR}/provision-authd.sh" --ssh-public-key "${SSH_PUBLIC_KEY_FILE}"

for index in "${!BROKER_ARRAY[@]}"; do
    ISSUER_ID="${ISSUER_ID_ARRAY[$index]}"
    CLIENT_ID="${CLIENT_ID_ARRAY[$index]}"
    CLIENT_SECRET="${CLIENT_SECRET_ARRAY[$index]}"

    # Provision the broker in the VM
    "${SCRIPT_DIR}/provision-broker.sh" \
        --broker "${BROKER_ARRAY[$index]}" \
        --issuer-id "${ISSUER_ID}" \
        --client-id "${CLIENT_ID}" \
        --client-secret "${CLIENT_SECRET}"
done
