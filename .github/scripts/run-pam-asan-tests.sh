#!/usr/bin/env bash

set -euo pipefail

script_dir="$(dirname "$(readlink -f "$0")")"
cd "${script_dir}/../.."

: "${AUTHD_TESTS_ARTIFACTS_PATH:?AUTHD_TESTS_ARTIFACTS_PATH must be set}"
: "${GO_GC_FLAGS:?GO_GC_FLAGS must be set}"
: "${GO_TESTS_TIMEOUT:?GO_TESTS_TIMEOUT must be set}"

# Print executed commands to ease debugging
set -x

echo "::group::Install llvm-symbolizer"
# For llvm-symbolizer
sudo apt-get install -y llvm
echo "::endgroup::"

if go test -C ./pam/internal -json -asan -gcflags=all="${GO_GC_FLAGS}" \
    -failfast -timeout "${GO_TESTS_TIMEOUT}" ./... | \
    gotestfmt --logfile "${AUTHD_TESTS_ARTIFACTS_PATH}/gotestfmt.pam-internal-asan.log"; then
    :
else
    exit_code=$?
    cat "${AUTHD_TESTS_ARTIFACTS_PATH}"/asan.log* || true
    exit "${exit_code}"
fi

echo "Running PAM integration tests"
pushd ./pam/integration-tests >/dev/null
go test -asan -gcflags=all="${GO_GC_FLAGS}" -c

run_pam_integration_tests() {
    local log_suffix=""
    if [ "$#" -gt 0 ]; then
        log_suffix="${1}"
        shift
    fi
    go tool test2json -p pam/integrations-test ./integration-tests.test \
        -test.v=test2json \
        -test.failfast \
        -test.timeout "${GO_TESTS_TIMEOUT}" \
        "$@" | \
    tee "${AUTHD_TESTS_ARTIFACTS_PATH}/test2json.pam-integration-tests-asan${log_suffix}.json" | \
    gotestfmt --logfile "${AUTHD_TESTS_ARTIFACTS_PATH}/gotestfmt.pam-integration-tests-asan${log_suffix}.log"
}

# Names of the tests that failed in the given test2json output. Keep
# failed subtests, but omit their duplicate top-level failure event.
failed_test_paths() {
    [ -s "${1}" ] || return 0
    jq -r 'select(.Action == "fail" and .Test != null) | .Test' "${1}" |
        sort -u |
        awk '
            { failed[$0] = 1 }
            END {
                for (test in failed) {
                    has_failed_descendant = 0
                    for (candidate in failed) {
                        if (candidate != test && index(candidate, test "/") == 1) {
                            has_failed_descendant = 1
                            break
                        }
                    }
                    if (!has_failed_descendant) print test
                }
            }
        ' |
        sort -u
}

# Check that every test that started reached a terminal event.
test_output_is_complete() {
    local test_output="${1}"
    [ -s "${test_output}" ] || return 1
    jq -e -s '
        all(.[]; type == "object" and (.Action | type) == "string") and
        ([.[] | select(.Action == "run")] | length) > 0 and
        ([.[] | select(.Action == "run")] |
            all(.[]; (.Test | type) == "string" and (.Test | length) > 0)) and
        (
            ([.[] | select(.Action == "run") |
                [(.Package // ""), .Test] | @json] | unique) as $started |
            ([.[] |
                select(
                    (.Action == "pass" or .Action == "fail" or
                     .Action == "skip" or .Action == "bench") and
                    (.Test | type) == "string"
                ) |
                [(.Package // ""), .Test] | @json] | unique) as $terminal |
            (($started - $terminal) | length) == 0
        )
    ' "${test_output}" >/dev/null
}

# Turn a test name into a -test.run pattern that matches the exact
# test path, escaping each component independently.
test_run_pattern() {
    local test_name="${1}"
    local component
    local escaped
    local pattern=""
    local -a components
    IFS=/ read -ra components <<< "${test_name}"
    for component in "${components[@]}"; do
        escaped=$(printf '%s' "${component}" | sed 's/[.[\^$*+?(){|\\]/\\&/g')
        pattern="${pattern:+${pattern}/}^${escaped}$"
    done
    printf '%s' "${pattern}"
}

# Retry only ASAN internal checks. Other sanitizer diagnostics may
# indicate a real memory error and must fail directly.
asan_internal_check_failure() {
    local logs=(
        "${AUTHD_TESTS_ARTIFACTS_PATH}"/asan.log*
        "${AUTHD_TESTS_ARTIFACTS_PATH}/test2json.pam-integration-tests-asan.json"
    )
    local found_asan_error=0
    local f
    for f in "${logs[@]}"; do
        [ -e "${f}" ] || continue
        if grep -qF "AddressSanitizer:" "${f}"; then
            found_asan_error=1
            if grep -F "AddressSanitizer:" "${f}" | grep -qvF "CHECK failed"; then
                return 1
            fi
        fi
    done
    [ "${found_asan_error}" -eq 1 ]
}

exit_code=0
if run_pam_integration_tests; then
    :
else
    exit_code=$?
    if asan_internal_check_failure; then
        # Only the tests that failed are retried. If none could be identified
        # (e.g. the test binary died before reporting any test result), the
        # whole suite is retried instead.
        test_output="${AUTHD_TESTS_ARTIFACTS_PATH}/test2json.pam-integration-tests-asan.json"
        if ! test_output_is_complete "${test_output}"; then
            echo "ASAN internal check failure detected -- test output incomplete; retrying all tests"
            failed_test_names=""
        else
            failed_test_names=$(failed_test_paths "${test_output}")
        fi
        if [ -n "${failed_test_names}" ]; then
            retry_exit_code=0
            retry_index=0
            while IFS= read -r failed_test_name; do
                [ -n "${failed_test_name}" ] || continue
                retry_index=$((retry_index + 1))
                echo "ASAN internal check failure detected -- retrying failed test: ${failed_test_name}"
                retry_args=("-test.run=$(test_run_pattern "${failed_test_name}")")
                run_pam_integration_tests "-retry-${retry_index}" \
                    "${retry_args[@]}" || retry_exit_code=$?
            done <<< "${failed_test_names}"
            exit_code=${retry_exit_code}
        else
            echo "ASAN internal check failure detected -- retrying all tests"
            exit_code=0
            run_pam_integration_tests "-retry" || exit_code=$?
        fi
        for f in "${AUTHD_TESTS_ARTIFACTS_PATH}"/asan.log*; do
            [ -e "${f}" ] && mv "${f}" "${f}.prev"
        done
    fi
fi
popd || exit

# We don't need the xtrace output after this point
set +x

# We're logging to a file, and this is useful for having artifacts, but we still may want to see it in logs.
# Print up to asan_log_preview_lines lines in total across all ASAN logs unconditionally (always visible),
# with the full content of each log also available in a collapsible group.
asan_log_preview_lines=50
asan_log_remaining=${asan_log_preview_lines}
for f in "${AUTHD_TESTS_ARTIFACTS_PATH}"/asan.log*; do
    if ! [ -e "${f}" ]; then
        continue
    fi
    if [ -s "${f}" ]; then
        total_lines=$(wc -l < "${f}")
        if [ "${asan_log_remaining}" -le 0 ]; then
            echo "=== ${f} (${total_lines} lines, omitted from preview -- see collapsible group below) ==="
        elif [ "${total_lines}" -gt "${asan_log_remaining}" ]; then
            echo "=== ${f} (first ${asan_log_remaining} of ${total_lines} lines) ==="
            head -n "${asan_log_remaining}" "${f}"
            echo "... (truncated -- see collapsible group below for the full log)"
            asan_log_remaining=0
        else
            echo "=== ${f} (${total_lines} lines) ==="
            cat "${f}"
            asan_log_remaining=$((asan_log_remaining - total_lines))
        fi
        echo "::group::${f} (full ${total_lines} lines)"
        cat "${f}"
        echo "::endgroup::"
    else
        echo "${f}: empty"
    fi
done

exit "${exit_code}"
