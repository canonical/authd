#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat << EOF
Usage: $0 [options] [test.robot...]

Runs YARF end-to-end tests against a libvirt VM configured with the specified broker.
For each test the script reverts the VM to a broker-specific snapshot, links
test resources, launches the test via YARF, and optionally saves a VM snapshot
if the test fails.

Required environment variables (or use the corresponding command-line options):
  E2E_USER           The username used for authd login in the tests
  E2E_PASSWORD       The password used for authd login in the tests
  BROKER             The broker to test (e.g., authd-msentraid)

Optional environment variables / flags:
  SNAPSHOT_ON_FAIL   If set, take a snapshot of the VM when a test fails

Prerequisites:
  - A libvirt domain as set up by the vm/provision.sh script, with the snapshots:
      \${BROKER}-stable-configured
      \${BROKER}-edge-configured
  - YARF must be installed via the setup_yarf.sh script

Options:
  -u, --user USERNAME          Username for the tests (can also be set via E2E_USER environment variable)
  -p, --password PASSWORD      Password for the tests (can also be set via E2E_PASSWORD environment variable)
  -b, --broker BROKER          Broker to test (can also be set via BROKER environment variable)
      --snapshot-on-fail       Take a snapshot of the VM if a test fails
  -h, --help                   Show this help message and exit
EOF
}

ROOT_DIR=$(dirname "$(readlink -f "$0")")
TESTS_DIR="${ROOT_DIR}/tests"
VM_NAME=${VM_NAME:-"e2e-runner"}

# Parse command line arguments
TESTS_TO_RUN=""
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
        --snapshot-on-fail)
            SNAPSHOT_ON_FAIL=1
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

if [ -z "${E2E_USER:-}" ] || [ -z "${E2E_PASSWORD:-}" ] || [ -z "${BROKER:-}" ]; then
    echo "Error: E2E_USER, E2E_PASSWORD, and BROKER must be set either as environment variables or via command line arguments."
    usage
    exit 1
fi

if [ -z "${TESTS_TO_RUN}" ]; then
    echo "Running all tests in ${TESTS_DIR}"
    TESTS_TO_RUN=$(find "${TESTS_DIR}" -type f -name "*.robot")
fi

# Create a temporary test run directory
TEST_RUNS_DIR="${XDG_RUNTIME_DIR}/authd-e2e-test-runs"
mkdir -p "${TEST_RUNS_DIR}"
TEST_RUN_DIR=$(mktemp -d --tmpdir="${TEST_RUNS_DIR}" "${BROKER}-XXXXXX")
cd "${TEST_RUN_DIR}"
mkdir -p output resources

# Activate YARF environment
YARF_DIR="${ROOT_DIR}/.yarf"
if [ ! -d "${YARF_DIR}" ]; then
    echo "YARF directory not found at ${YARF_DIR}. Please run setup_yarf.sh first."
    exit 1
fi
# shellcheck disable=SC1091 # Avoid info message about not following sourced file
source "${YARF_DIR}/.venv/bin/activate"

# Run the YARF tests
tests_failed=
test_results=()
for test_file in $TESTS_TO_RUN; do
    test_name=$(basename "${test_file}")

    ln -s "${test_file}" .
    ln -sf --no-target-directory "$(dirname "${test_file}")/resources/authd" resources/authd
    # Update the symlink to the broker resources to use the specified broker
    ln -sf --no-target-directory "$(dirname "${test_file}")/resources/${BROKER}" resources/broker
    # Ensure the test run directory is cleaned up on exit
    # shellcheck disable=SC2064 # We want to capture the current value of test_name
    trap "rm -rf ${test_name} resources" EXIT

    SNAPSHOT_NAME=${BROKER}-edge-configured
    if [[ "${test_name}" == *"migration"* ]]; then
        SNAPSHOT_NAME=${BROKER}-stable-configured
    fi
    virsh snapshot-revert "${VM_NAME}" "${SNAPSHOT_NAME}" || true

    echo "Running test: ${test_name}"
    E2E_USER="$E2E_USER" \
    E2E_PASSWORD="$E2E_PASSWORD" \
    VNC_PORT=$(virsh vncdisplay "${VM_NAME}" | cut -d':' -f2) \
    yarf --outdir "output/${test_name}" --platform=Vnc . "$@" || test_result=$?

    if [ "${test_result:-0}" -ne 0 ] && [ -v SNAPSHOT_ON_FAIL ]; then
        echo "Test failed. Saving VM snapshot as requested..."
        virsh snapshot-create-as "${VM_NAME}" "${test_name}-fail-$(date +%Y%m%d%H%M)"
        echo "Snapshot '${test_name}-fail-$(date +%Y%m%d%H%M)' created."
    fi

    if [ "${test_result:-0}" -ne 0 ]; then
        tests_failed=1
        test_results+=("${test_name}: FAILED")
    else
        test_results+=("${test_name}: OK")
    fi
    rm -f "${test_name}"
done

for result in "${test_results[@]}"; do
    echo "${result}"
done

if [ -n "${tests_failed}" ]; then
    exit 1
fi
