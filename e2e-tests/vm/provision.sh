#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
CONFIG_FILE="${SCRIPT_DIR}/config.env"
APT_SOURCE_ARGS=()
AUTHD_APT_SOURCE_ARGS=()

usage(){
    cat << EOF
Usage: $0 [--config-file <config file>] [--release <release>] [--data-dir <directory>] [--broker <broker>] [--authd-deb <deb>] [--apt-source <source> ...] [--authd-apt-source <source> ...] [--apt-source-base <source>] [--authd-apt-source-base <source>] [--broker-snap <snap>] [--force]

Options:
  --config-file <config file>  Path to the configuration file (default: config.env)
  --release <release>          Ubuntu release to provision (e.g. noble, resolute); overrides config file
  --data-dir <directory>       Base directory for VM artifacts (or AUTHD_E2E_DATA_DIR)
  --broker <broker>            The broker to install ("authd-google", "authd-msentraid", ...)
  --authd-deb <deb>            Path to the authd deb file to install
  --apt-source <source>        Add a PPA or Ubuntu archive suite for all packages except authd; repeatable
  --authd-apt-source <source> Add a PPA or Ubuntu archive suite for authd; repeatable
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
            APT_SOURCE_ARGS+=("$2")
            shift 2
            ;;
        --authd-apt-source)
            AUTHD_APT_SOURCE_ARGS+=("$2")
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
provision_authd_args=(
    --config-file "${CONFIG_FILE}"
)
if [[ -n "${RELEASE_ARG:-}" ]]; then
    provision_authd_args+=(--release "${RELEASE_ARG}")
fi
if [[ -n "${DATA_DIR_ARG:-}" ]]; then
    provision_authd_args+=(--data-dir "${DATA_DIR_ARG}")
fi
if [[ -n "${BROKER:-}" ]]; then
    provision_authd_args+=(--broker "${BROKER}")
fi
if [[ -n "${AUTHD_DEB:-}" ]]; then
    provision_authd_args+=(--authd-deb "${AUTHD_DEB}")
fi
if [[ -n "${APT_SOURCE_BASE:-}" ]]; then
    provision_authd_args+=(--apt-source-base "${APT_SOURCE_BASE}")
fi
if [[ -n "${AUTHD_APT_SOURCE_BASE:-}" ]]; then
    provision_authd_args+=(--authd-apt-source-base "${AUTHD_APT_SOURCE_BASE}")
fi
for source in "${APT_SOURCE_ARGS[@]}"; do
    provision_authd_args+=(--apt-source "${source}")
done
for source in "${AUTHD_APT_SOURCE_ARGS[@]}"; do
    provision_authd_args+=(--authd-apt-source "${source}")
done
if [[ -n "${BROKER_SNAP:-}" ]]; then
    provision_authd_args+=(--broker-snap "${BROKER_SNAP}")
fi
if [[ -n "${FORCE:-}" ]]; then
    provision_authd_args+=(--force)
fi
"${SCRIPT_DIR}/provision-authd.sh" "${provision_authd_args[@]}"
