#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CONFIG_FILE="${SCRIPT_DIR}/config.env"

usage(){
    cat << EOF
Usage: $0 [--config-file <config file>] [--release <release>] [--broker <broker>] [--authd-deb <deb>] [--local-deb <deb-or-dir>] [--authd-ppa <ppa>] [--broker-snap <snap>] [--force]

Options:
  --config-file <config file>  Path to the configuration file (default: config.env)
  --release <release>          Ubuntu release to provision (e.g. noble, resolute); overrides config file
  --broker <broker>            The broker to install ("authd-google", "authd-msentraid", ...)
  --authd-deb <deb>            Path to the authd deb file to install (default: install from the edge PPA)
  --local-deb <path>           Path to a local .deb file or a directory containing local .deb files; can be repeated
  --authd-ppa <ppa>            PPA to use instead of authd-edge when installing authd and its dependencies
  --broker-snap <snap>         Path to the broker snap file to install (default: install from the edge channel)
  --force                      Force provisioning: remove existing VM and artifacts and create a fresh VM
  -h, --help                   Show this help message and exit

Provisions the VM for end-to-end tests
EOF
}

LOCAL_DEB_ARGS=()

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --config-file)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --release)
            RELEASE_ARG="$2"
            shift 2
            ;;
        --force)
            FORCE="true"
            shift
            ;;
        --b|--broker)
            BROKER="$2"
            shift 2
            ;;
        --authd-deb)
            AUTHD_DEB="$2"
            shift 2
            ;;
        --local-deb)
            if [ $# -lt 2 ]; then
                echo >&2 "Error: $1 requires an argument"
                usage
                exit 1
            fi
            LOCAL_DEB_ARGS+=("$2")
            shift 2
            ;;
        --authd-ppa)
            AUTHD_PPA="$2"
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

# Print executed commands to ease debugging
set -x

PROVISION_UBUNTU_ARGS=(--config-file "${CONFIG_FILE}")
if [ -n "${RELEASE_ARG:-}" ]; then
    PROVISION_UBUNTU_ARGS+=(--release "${RELEASE_ARG}")
fi
if [ -n "${FORCE:-}" ]; then
    PROVISION_UBUNTU_ARGS+=(--force)
fi

# Provision the VM with Ubuntu
"${SCRIPT_DIR}/provision-ubuntu.sh" "${PROVISION_UBUNTU_ARGS[@]}"

PROVISION_AUTHD_ARGS=(--config-file "${CONFIG_FILE}")
if [ -n "${RELEASE_ARG:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--release "${RELEASE_ARG}")
fi
if [ -n "${BROKER:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--broker "${BROKER}")
fi
if [ -n "${AUTHD_DEB:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--authd-deb "${AUTHD_DEB}")
fi
for local_deb in "${LOCAL_DEB_ARGS[@]}"; do
    PROVISION_AUTHD_ARGS+=(--local-deb "${local_deb}")
done
if [ -n "${AUTHD_PPA:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--authd-ppa "${AUTHD_PPA}")
fi
if [ -n "${BROKER_SNAP:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--broker-snap "${BROKER_SNAP}")
fi
if [ -n "${FORCE:-}" ]; then
    PROVISION_AUTHD_ARGS+=(--force)
fi

# Provision authd in the VM
"${SCRIPT_DIR}/provision-authd.sh" "${PROVISION_AUTHD_ARGS[@]}"
