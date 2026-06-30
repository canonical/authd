#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat << EOF
Usage: $0 [options]

Launches the YARF interactive console (a Robot Framework REPL) connected to the
e2e libvirt VM via VNC, so you can try out keywords interactively against a live
screen. This is the recommended way to develop reliable OCR matches
('Find Text' / 'Match Text' / 'Read Text'): drive the VM to the target screen,
probe what the OCR actually reads, and iterate on the string/regex/region until
it matches reliably, before copying the working match into a .robot or .resource
file.

The console restores the broker-specific snapshot and connects to the VM exactly
like run-tests.sh, but instead of running a suite it drops you into the REPL.

Inside the console, load the authd test keywords with:

    Import Resource    \${CURDIR}/resources/authd.resource
    Import Resource    \${CURDIR}/resources/broker.resource

Then probe OCR, for example:

    \${matches}=    Find Text    Select your provider
    Log To Console    \${matches}
    Read Text

Required environment variables (or use the corresponding command-line options):
  BROKER             The broker whose snapshot to restore (e.g., authd-google)
  RELEASE            The Ubuntu release of the VM (e.g., noble, resolute)

Optional:
  E2E_USER, E2E_PASSWORD, TOTP_SECRET   Forwarded to the console so keywords
                                        that reference them work as in a run.

Options:
  -b, --broker <broker>     Broker to use (or BROKER env var)
  -r, --release <release>   Ubuntu release (or RELEASE env var)
  -o, --output-dir DIR      Directory for console artifacts (default: temp dir)
  -h, --help                Show this help message and exit
EOF
}

ROOT_DIR=$(dirname "$(readlink -f "$0")")
TEST_RUNS_DIR="${XDG_RUNTIME_DIR}/authd-e2e-test-runs"

# Load broker-specific credentials from e2e-tests-<broker>.env before argument
# parsing, so that explicit CLI flags take priority over values from the file.
_scan_broker="${BROKER:-}"
_scan_args=("$@")
for ((i=0; i<${#_scan_args[@]}; i++)); do
    if [[ "${_scan_args[$i]}" == "--broker" || "${_scan_args[$i]}" == "-b" ]]; then
        _scan_broker="${_scan_args[$((i+1))]:-}"
        break
    fi
done
if [[ -n "${_scan_broker:-}" ]]; then
    _env_file="${ROOT_DIR}/e2e-tests-${_scan_broker#authd-}.env"
    if [[ -f "${_env_file}" ]]; then
        set -a
        # shellcheck disable=SC1090
        source "${_env_file}"
        set +a
    fi
fi
unset _scan_broker _scan_args _env_file

while [[ $# -gt 0 ]]; do
    case "$1" in
        --broker|-b)
            if [[ $# -lt 2 ]]; then
                echo >&2 "Error: $1 requires an argument"
                usage
                exit 1
            fi
            BROKER="$2"
            shift 2
            ;;
        --release|-r)
            if [[ $# -lt 2 ]]; then
                echo >&2 "Error: $1 requires an argument"
                usage
                exit 1
            fi
            RELEASE="$2"
            shift 2
            ;;
        --output-dir|-o)
            if [[ $# -lt 2 ]]; then
                echo >&2 "Error: $1 requires an argument"
                usage
                exit 1
            fi
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

if [ -z "${BROKER:-}" ] || [ -z "${RELEASE:-}" ]; then
    echo >&2 "Error: BROKER and RELEASE must be set either as environment variables or via command line arguments."
    usage
    exit 1
fi

VM_NAME=${VM_NAME:-"e2e-runner-${RELEASE}"}

# Restore the broker snapshot so the console starts from a known state, mirroring
# what run-tests.sh does at the start of each test. If the VM is already running
# we leave it as-is, so you can attach the console to a state you've set up by
# hand or left behind from a previous (failed) test run.
if ! virsh domstate "${VM_NAME}" | grep -q '^running'; then
    virsh snapshot-revert "${VM_NAME}" "${BROKER}-installed"
fi
VNC_PORT=$(virsh vncdisplay "${VM_NAME}" | cut -d':' -f2)

if [ -z "${OUTPUT_DIR:-}" ]; then
    mkdir -p "${TEST_RUNS_DIR}"
    OUTPUT_DIR=$(mktemp -d --tmpdir="${TEST_RUNS_DIR}" "${BROKER}-console-XXXXXX")
else
    mkdir -p "${OUTPUT_DIR}"
fi

YARF_DIR="${ROOT_DIR}/.yarf"
if [ ! -d "${YARF_DIR}" ]; then
    echo >&2 "YARF directory not found at ${YARF_DIR}. Please run setup-yarf.sh first."
    exit 1
fi
# shellcheck disable=SC1091 # Avoid info message about not following sourced file
source "${YARF_DIR}/.venv/bin/activate"

# Running `yarf` with no suite argument drops into the interactive console (a
# Robot Framework debug REPL) for the selected platform. The Vnc platform reads
# VNC_HOST/VNC_PORT from the environment to connect to the VM's display.
#
# Run from this directory so that ${CURDIR} inside the console (which YARF sets
# to the current working directory) resolves to the e2e-tests root, making
# `Import Resource ${CURDIR}/resources/...` work regardless of where this script
# was invoked from.
cd "${ROOT_DIR}"

systemd_ver=$(systemctl --version | awk 'NR==1 {print $2}')
if dpkg --compare-versions "$systemd_ver" "ge" "256" && [ -z "${FORCE_JOURNAL_TCP:-}" ]; then
    SYSTEMD_SUPPORTS_VSOCK=1
fi

env \
    BROKER="$BROKER" \
    RELEASE="$RELEASE" \
    E2E_USER="${E2E_USER:-}" \
    E2E_PASSWORD="${E2E_PASSWORD:-}" \
    TOTP_SECRET="${TOTP_SECRET:-}" \
    VNC_PORT="$VNC_PORT" \
    SYSTEMD_SUPPORTS_VSOCK="${SYSTEMD_SUPPORTS_VSOCK:-}" \
    YARF_LOG_LEVEL="${YARF_LOG_LEVEL:-DEBUG}" \
    yarf --platform Vnc --outdir "${OUTPUT_DIR}"
