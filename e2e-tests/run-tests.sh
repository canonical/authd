#!/usr/bin/env bash

set -euo pipefail
set -x

usage() {
    cat << EOF
Usage: $0 [options] [test.robot...]

Runs YARF end-to-end tests against a libvirt VM configured with the specified broker.
For each test the script reverts the VM to a broker-specific snapshot, links
test resources and launches the test via robot framework.

Required environment variables (or use the corresponding command-line options):
  E2E_USER           The username used for authd login in the tests
  E2E_PASSWORD       The password used for authd login in the tests
  TOTP_SECRET        The secret used to generate OTP codes for the E2E_USER's MFA
  BROKER             The broker to test (e.g., authd-msentraid)

Prerequisites:
  - A libvirt domain as set up by the vm/provision.sh script, with the snapshots:
      \${BROKER}-stable-configured
      \${BROKER}-edge-configured
  - YARF must be installed via the setup_yarf.sh script

Options:
  -u, --user <name>            Username for the tests (can also be set via E2E_USER environment variable)
  -p, --password <password>    Password for the tests (can also be set via E2E_PASSWORD environment variable)
  -s, --totp-secret <secret>   Secret to generate OTP codes for the user's MFA (can also be set via TOTP_SECRET environment variable)
  -b, --broker <broker>        Broker to test (can also be set via BROKER environment variable)
  -r, --release <release>      Ubuntu release to test (e.g., 'questing', can also be set via RELEASE environment variable)
  -o, --output-dir DIR         Directory to store test outputs (default: temporary directory)
  -h, --help                   Show this help message and exit
      --rerunfailed            Re-run only the tests that failed in the previous run
EOF
}

ROOT_DIR=$(dirname "$(readlink -f "$0")")
TESTS_DIR="${ROOT_DIR}/tests"
TEST_RUNS_DIR="${XDG_RUNTIME_DIR}/authd-e2e-test-runs"

# Parse command line arguments
TESTS_TO_RUN=""
OUTPUT_DIR=""
while [[ $# -gt 0 ]]; do
    key="$1"

    case $key in
        --user|-u)
            E2E_USER="$2"
            shift 2
            ;;
        --password|-p)
            E2E_PASSWORD="$2"
            shift 2
            ;;
        --broker|-b)
            BROKER="$2"
            shift 2
            ;;
        --release|-r)
            RELEASE="$2"
            shift 2
            ;;
        --totp-secret|-s)
            TOTP_SECRET="$2"
            shift 2
            ;;
        --rerunfailed)
            RERUNFAILED=1
            shift
            ;;
        --output-dir|-o)
            OUTPUT_DIR="$2"
            shift 2
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
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
        *)
            TESTS_TO_RUN="${TESTS_TO_RUN} ${TESTS_DIR}/$(basename "${1}")"
            shift
            ;;
    esac
done

if [ -z "${E2E_USER:-}" ] || [ -z "${E2E_PASSWORD:-}" ] || [ -z "${BROKER:-}" ] || [ -z "${RELEASE:-}" ] || [ -z "${TOTP_SECRET:-}" ]; then
    echo >&2  "Error: E2E_USER, E2E_PASSWORD, BROKER, RELEASE, and TOTP_SECRET must be set either as environment variables or via command line arguments."
    usage
    exit 1
fi

VM_NAME=${VM_NAME:-"e2e-runner-${RELEASE}"}

if [ -z "${TESTS_TO_RUN}" ]; then
    echo "Running all tests in ${TESTS_DIR}"
    TESTS_TO_RUN=$(find "${TESTS_DIR}" -type f -name "*.robot")
fi

PREVIOUS_TEST_RUN_DIR=$(readlink -f "${TEST_RUNS_DIR}/${BROKER}-latest" || true)
if [ -n "${RERUNFAILED:-}" ] && [ -z "${PREVIOUS_TEST_RUN_DIR}" ]; then
    echo >&2 "Error: No previous test run found to rerun failed tests from."
    exit 1
fi

systemd_ver=$(systemctl --version | awk 'NR==1 {print $2}')
if dpkg --compare-versions "$systemd_ver" "ge" "256"; then
    SYSTEMD_SUPPORTS_VSOCK=1
fi

ROBOT_ARGS=()
if [ -n "${RERUNFAILED:-}" ]; then
    echo "Rerunning failed tests from previous run in ${PREVIOUS_TEST_RUN_DIR}"
    ROBOT_ARGS+=(--rerunfailed "${PREVIOUS_TEST_RUN_DIR}/output.xml")
fi

# Launch the domain if it's not already running, so that we can get its VNC port
if ! virsh domstate "${VM_NAME}" | grep -q '^running'; then
    # For some reason, when using external snapshot and the host was rebooted,
    # `virsh start` fails with a permission denied error.
    # Reverting to a snapshot first fixes this (and since it's a live snapshot,
    # we don't need to start the VM afterwards).
    virsh snapshot-revert "${VM_NAME}" "${BROKER}-stable-configured"
fi
VNC_PORT=$(virsh vncdisplay "${VM_NAME}" | cut -d':' -f2)

# Create a temporary test run directory
mkdir -p "${TEST_RUNS_DIR}"
TEST_RUN_DIR=$(mktemp -d --tmpdir="${TEST_RUNS_DIR}" "${BROKER}-XXXXXX")
ln -sf --no-target-directory "${TEST_RUN_DIR}" "${TEST_RUNS_DIR}/${BROKER}-latest"
cd "${TEST_RUN_DIR}"

if [ -z "${OUTPUT_DIR:-}" ]; then
    OUTPUT_DIR=output
    echo "No output directory specified, using current test run directory."
fi

mkdir -p "${OUTPUT_DIR}" resources

# Activate YARF environment
YARF_DIR="${ROOT_DIR}/.yarf"
if [ ! -d "${YARF_DIR}" ]; then
    echo >&2  "YARF directory not found at ${YARF_DIR}. Please run setup_yarf.sh first."
    exit 1
fi
# shellcheck disable=SC1091 # Avoid info message about not following sourced file
source "${YARF_DIR}/.venv/bin/activate"

# Create symlinks to the resources directory
mkdir -p tests/resources
for resource in "${ROOT_DIR}/resources/"*; do
    ln -s "${resource}" "tests/resources/$(basename "${resource}")"
done
ln -sf --no-target-directory "${BROKER}" tests/resources/broker

# Create symlinks to the test files
for test_file in $TESTS_TO_RUN; do
    ln -s "${test_file}" tests
done

env \
    E2E_USER="$E2E_USER" \
    E2E_PASSWORD="$E2E_PASSWORD" \
    TOTP_SECRET="$TOTP_SECRET" \
    BROKER="$BROKER" \
    RELEASE="$RELEASE" \
    VNC_PORT="$VNC_PORT" \
    SYSTEMD_SUPPORTS_VSOCK="${SYSTEMD_SUPPORTS_VSOCK:-}" \
    robot \
        --loglevel DEBUG \
        --pythonpath "${YARF_DIR}/yarf/rf_libraries/libraries/vnc" \
        --outputdir "${OUTPUT_DIR}" \
        "${ROBOT_ARGS[@]}" \
        "$@" \
        tests \
        || test_result=$?

exit "${test_result:-0}"
