#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CONFIG_FILE="${SCRIPT_DIR}/config.env"

usage(){
    cat << EOF
Usage: $0 [--config-file <config file>] [--release <release>] [--data-dir <directory>] [--broker <broker>] [--authd-deb <deb>] [--apt-source <source>] [--authd-apt-source <source>] [--apt-source-base <source>] [--authd-apt-source-base <source>] [--broker-snap <snap>] [--force]

Options:
  --config-file <config file>  Path to the configuration file (default: config.env)
  --release <release>          Ubuntu release to provision (e.g. noble, resolute); overrides config file
  --data-dir <directory>       Base directory for VM artifacts (or AUTHD_E2E_DATA_DIR)
  --broker <broker>            The broker to install ("authd-google", "authd-msentraid", ...)
  --authd-deb <deb>            Path to the authd deb file to install
  --apt-source <source>        PPA or Ubuntu archive suite for all packages except authd
  --authd-apt-source <source> PPA or Ubuntu archive suite from which to install authd
  --apt-source-base <source>  Ubuntu archive suite for the stable system package baseline
  --authd-apt-source-base <source>
                              PPA or Ubuntu archive suite for the stable authd baseline
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
        --data-dir)
            DATA_DIR_ARG="$2"
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
        --apt-source-base)
            APT_SOURCE_BASE="$2"
            shift 2
            ;;
        --authd-apt-source-base)
            AUTHD_APT_SOURCE_BASE="$2"
            shift 2
            ;;
        --apt-source)
            APT_SOURCE="$2"
            shift 2
            ;;
        --authd-apt-source)
            AUTHD_APT_SOURCE="$2"
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

# A linked worktree does not have its own copy of the gitignored config.
# Reuse the config from the main worktree when it is available.
if [[ "${CONFIG_FILE}" == "${SCRIPT_DIR}/config.env" && ! -f "${CONFIG_FILE}" ]]; then
    _git_common_dir=
    if command -v git >/dev/null 2>&1; then
        _git_common_dir="$(git -C "${SCRIPT_DIR}" rev-parse --git-common-dir 2>/dev/null || true)"
    fi
    if [[ "${_git_common_dir}" == /* ]]; then
        _linked_config="$(dirname "${_git_common_dir}")/e2e-tests/vm/config.env"
        if [[ -f "${_linked_config}" ]]; then
            CONFIG_FILE="${_linked_config}"
        fi
    fi
    unset _git_common_dir _linked_config
fi

# Print executed commands to ease debugging
set -x

# Provision the VM with Ubuntu
"${SCRIPT_DIR}/provision-ubuntu.sh" \
  --config-file "${CONFIG_FILE}" \
  ${RELEASE_ARG:+--release "${RELEASE_ARG}"} \
  ${DATA_DIR_ARG:+--data-dir "${DATA_DIR_ARG}"} \
  ${FORCE:+--force}

# Provision authd in the VM
"${SCRIPT_DIR}/provision-authd.sh" \
  --config-file "${CONFIG_FILE}" \
  ${RELEASE_ARG:+--release "${RELEASE_ARG}"} \
  ${DATA_DIR_ARG:+--data-dir "${DATA_DIR_ARG}"} \
  ${BROKER:+--broker "${BROKER}"} \
  ${AUTHD_DEB:+--authd-deb "${AUTHD_DEB}"} \
  ${APT_SOURCE_BASE:+--apt-source-base "${APT_SOURCE_BASE}"} \
  ${AUTHD_APT_SOURCE_BASE:+--authd-apt-source-base "${AUTHD_APT_SOURCE_BASE}"} \
  ${APT_SOURCE:+--apt-source "${APT_SOURCE}"} \
  ${AUTHD_APT_SOURCE:+--authd-apt-source "${AUTHD_APT_SOURCE}"} \
  ${BROKER_SNAP:+--broker-snap "${BROKER_SNAP}"} \
  ${FORCE:+--force}
