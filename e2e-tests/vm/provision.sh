#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CONFIG_FILE="${SCRIPT_DIR}/config.env"

usage(){
    cat << EOF
Usage: $0 [--config-file <config file>] [--release <release>] [--broker <broker>] [--authd-deb <deb>] [--authd-ppa <ppa>] [--broker-snap <snap>] [--force]

Options:
  --config-file <config file>  Path to the configuration file (default: config.env)
  --release <release>          Ubuntu release to provision (e.g. noble, resolute); overrides config file
  --broker <broker>            The broker to install ("authd-google", "authd-msentraid", ...)
  --authd-deb <deb>            Path to the authd deb file to install (default: install from the edge PPA)
  --authd-ppa <ppa>            PPA to use instead of authd-edge when installing authd and its dependencies
  --broker-snap <snap>         Path to the broker snap file to install (default: install from the edge channel)
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

# Provision the VM with Ubuntu
"${SCRIPT_DIR}/provision-ubuntu.sh" \
  --config-file "${CONFIG_FILE}" \
  ${RELEASE_ARG:+--release "${RELEASE_ARG}"} \
  ${FORCE:+--force}

# Provision authd in the VM
"${SCRIPT_DIR}/provision-authd.sh" \
  --config-file "${CONFIG_FILE}" \
  ${RELEASE_ARG:+--release "${RELEASE_ARG}"} \
  ${BROKER:+--broker "${BROKER}"} \
  ${AUTHD_DEB:+--authd-deb "${AUTHD_DEB}"} \
  ${AUTHD_PPA:+--authd-ppa "${AUTHD_PPA}"} \
  ${BROKER_SNAP:+--broker-snap "${BROKER_SNAP}"} \
  ${FORCE:+--force}
