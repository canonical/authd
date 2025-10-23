#!/usr/bin/env bash

set -euo pipefail

# Required environment variables:
#    E2E_USER - The username to use for the tests
#    E2E_PASSWORD - The password to use for the tests
#    BROKER - The broker to test (e.g., authd-msentraid)
# Optional environment variables:
#    SNAPSHOT_ON_FAIL - If set, a snapshot of the VM will be taken if a test fails.
#    SHOW_WEBVIEW - If set to 1, the webview used for device authentication is shown during the test run (useful for debugging).

# Required setup:
#   Virsh domain named 'e2e-runner' must exist
#   Domain snapshots created:
#      ${broker}-stable-configured
#      ${broker}-edge-configured

# This script is used to run the YARF tests for the authd-oidc-brokers project.
ROOT_DIR=$(dirname "$(readlink -f "$0")")
TESTS_DIR="${ROOT_DIR}/tests"
VM_NAME=${VM_NAME:-"e2e-runner"}

# Create directory for the test run
TEST_RUNS_DIR="${ROOT_DIR}/.runs"
mkdir -p "${TEST_RUNS_DIR}"
TEST_RUN_DIR=$(mktemp -d --tmpdir="${TEST_RUNS_DIR}" "${BROKER}-XXXXXX")
cd "${TEST_RUN_DIR}"
mkdir -p output resources

TESTS_TO_RUN=""
while [ $# -gt 0 ]; do
    case "$1" in
        --)
            shift
            break ;;
        *)
            echo "Running specific test: $1"
            TESTS_TO_RUN="${TESTS_TO_RUN} ${TESTS_DIR}/$(basename "${1}")"
            shift ;;
    esac
done

if [ -z "${TESTS_TO_RUN}" ]; then
    echo "No specific tests provided. Running all tests in ${TESTS_DIR}."
    TESTS_TO_RUN=$(find "${TESTS_DIR}" -type f -name "*.robot")
fi

# Activate YARF environment
YARF_DIR="${ROOT_DIR}/.yarf"
if [ ! -d "${YARF_DIR}" ]; then
    echo "YARF directory not found at ${YARF_DIR}. Please run setup_yarf.sh first."
    exit 1
fi
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
    yarf --outdir "output/${test_name}" --platform=Vnc . "$@" || test_result=$? && true

    if [ ${test_result} -ne 0 ] && [ -v SNAPSHOT_ON_FAIL ]; then
        echo "Test failed. Saving VM snapshot as requested..."
        virsh snapshot-create-as "${VM_NAME}" "${test_name}-fail-$(date +%Y%m%d%H%M)"
        echo "Snapshot '${test_name}-fail-$(date +%Y%m%d%H%M)' created."
    fi

    if [ ${test_result} -ne 0 ]; then
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
