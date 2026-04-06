#!/usr/bin/env bash
# shellcheck disable=SC2034
# Shared helpers for authd dev scripts.
#
# Source this from any script under dev/scripts/:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${SCRIPT_DIR}/../lib/common.sh"

# Prevent double-sourcing
[[ -n "${_AUTHD_COMMON_SOURCED:-}" ]] && return 0
_AUTHD_COMMON_SOURCED=1

# Variables and functions defined here are used by sourcing scripts
# (install-authd, install-broker), so shellcheck's "appears unused"
# warnings are false positives (suppressed via file-level SC2034 above).

# --- Path resolution ---
_COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_DIR="$(dirname "$_COMMON_DIR")"
PROJECT_DIR="$(dirname "$DEV_DIR")"
WORKSPACE="${WORKSPACE:-${PROJECT_DIR}}"

# --- Output helpers ---
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'
BLUE=$'\033[0;34m'; BOLD=$'\033[1m'; NC=$'\033[0m'

info()  { printf '%s\n' "${BLUE}[INFO]${NC} $*"; }
ok()    { printf '%s\n' "${GREEN}[  OK]${NC} $*"; }
warn()  { printf '%s\n' "${YELLOW}[WARN]${NC} $*"; }
error() { printf '%s\n' "${RED}[ERR ]${NC} $*" >&2; }
die()   { error "$@"; exit 1; }

# Escape a string for use as a sed replacement value.
# Handles |, &, /, and backslash.
sed_escape() { printf '%s\n' "$1" | sed -e 's/[|&/\\]/\\&/g'; }

# --- Host-side Helpers (moved from dev-env.sh for consistency) ---

detect_ssh_key() {
    local key_files=(
        "${HOME}/.ssh/id_ed25519.pub"
        "${HOME}/.ssh/id_rsa.pub"
        "${HOME}/.ssh/id_ecdsa.pub"
    )
    for kf in "${key_files[@]}"; do
        if [[ -f "$kf" ]]; then
            local private_key="${kf%.pub}"
            [[ -f "$private_key" ]] || die "Public key found at ${kf} but private key missing: ${private_key}"
            [[ -r "$private_key" ]] || die "Private key not readable: ${private_key} (check permissions)"
            echo "$kf"
            return 0
        fi
    done
    die "No SSH public key found. Generate one with: ssh-keygen -t ed25519"
}

# Return the private key path for the detected SSH key.
get_ssh_private_key() {
    local pub_key
    pub_key=$(detect_ssh_key)
    echo "${pub_key%.pub}"
}

# --- Common paths (matching debian/install) ---
MULTIARCH=$(dpkg --print-multiarch 2>/dev/null || gcc -dumpmachine 2>/dev/null || echo "$(uname -m)-linux-gnu")
PAM_MODULE_DIR="/usr/lib/${MULTIARCH}/security"
NSS_LIB_DIR="/usr/lib/${MULTIARCH}"
DAEMONS_PATH="/usr/libexec"

# --- Environment ---

ensure_path() {
    export PATH="/usr/local/go/bin:${HOME}/.cargo/bin:${PATH}"
}

ensure_workspace() {
    cd "$WORKSPACE" || die "Cannot cd to workspace: $WORKSPACE"
}

ensure_git_submodules() {
    if [[ -f "${WORKSPACE}/.gitmodules" ]]; then
        if ! git config --global --get-all safe.directory 2>/dev/null | grep -qxF "${WORKSPACE}"; then
            git config --global --add safe.directory "${WORKSPACE}" 2>/dev/null || true
        fi
        (cd "${WORKSPACE}" && git submodule update --init --recursive) || \
            warn "Failed to update submodules (may not be a git repo or already initialized)"
    fi
}

# --- Broker variant configuration ---
#
# Sets variant-specific variables. D-Bus metadata (DISPLAY_NAME, DBUS_NAME,
# DBUS_OBJECT) is read from the upstream template at
# authd-oidc-brokers/conf/variants/<variant>/authd.conf so that the dev
# scripts stay in sync with the broker source automatically.
load_variant_config() {
    local variant="$1"

    VARIANT_CONF_DIR="${WORKSPACE}/authd-oidc-brokers/conf/variants/${variant}"

    case "$variant" in
        google)
            BUILD_TAG="withgoogle"
            BINARY_NAME="authd-google"
            CONF_DIR="/etc/authd-google"
            SERVICE_NAME="authd-google"
            DEFAULT_ISSUER="https://accounts.google.com"
            ;;
        msentraid)
            BUILD_TAG="withmsentraid"
            BINARY_NAME="authd-msentraid"
            CONF_DIR="/etc/authd-msentraid"
            SERVICE_NAME="authd-msentraid"
            DEFAULT_ISSUER=""
            ;;
        oidc)
            BUILD_TAG=""
            BINARY_NAME="authd-oidc"
            CONF_DIR="/etc/authd-oidc"
            SERVICE_NAME="authd-oidc"
            DEFAULT_ISSUER=""
            ;;
        *)
            die "Unknown variant: ${variant}. Use: google, msentraid, or oidc"
            ;;
    esac

    # Read D-Bus metadata from upstream authd.conf template
    local authd_conf="${VARIANT_CONF_DIR}/authd.conf"
    if [[ -f "$authd_conf" ]]; then
        DISPLAY_NAME=$(sed -n 's/^name = //p' "$authd_conf")
        DBUS_NAME=$(sed -n 's/^dbus_name = //p' "$authd_conf")
        DBUS_OBJECT=$(sed -n 's/^dbus_object = //p' "$authd_conf")
    else
        warn "Upstream config not found: ${authd_conf} — using variant name as display name"
        DISPLAY_NAME="$variant"
        DBUS_NAME=""
        DBUS_OBJECT=""
    fi
}
