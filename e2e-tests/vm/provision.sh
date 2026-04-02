#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CONFIG_FILE="${SCRIPT_DIR}/config.sh"

usage(){
    cat << EOF
Usage: $0 [--config-file <config file>] [--force]

Options:
  --config-file <config file>  Path to the configuration file (default: config.sh)
  --force                      Force provisioning: remove existing VM and artifacts and create a fresh VM
  -h, --help                   Show this help message and exit

Provisions the VM for end-to-end tests
EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --config-file)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --force)
            FORCE="true"
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

# Print executed commands to ease debugging
set -x

# Provision the VM with Ubuntu
"${SCRIPT_DIR}/provision-ubuntu.sh" --config-file "${CONFIG_FILE}" ${FORCE:+--force}

# Provision authd in the VM
"${SCRIPT_DIR}/provision-authd.sh" --config-file "${CONFIG_FILE}"
