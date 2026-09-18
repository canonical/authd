#!/bin/bash
set -euo pipefail

: "${E2E_TESTS_DESCRIPTION:=}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

# shellcheck source=../../e2e-tests/vm/lib/libprovision.sh
source "$(dirname "${BASH_SOURCE[0]}")/../../e2e-tests/vm/lib/libprovision.sh"

body_without_comments=$(
    tr -d '\r' <<<"${E2E_TESTS_DESCRIPTION}" |
        perl -0pe 's/<!--.*?(?:-->|$)//gs'
)

marker_lines() {
    local marker="$1"

    sed -nE \
        "s/^[[:space:]]*${marker}:[[:space:]]*(.+)$/\1/p" \
        <<<"${body_without_comments}" |
        sed -E 's/[[:space:]]+$//'
}

marker_values() {
    marker_lines "$1" | tr -s ' \t,' '\n'
}

brokers=()
declare -A seen_brokers=()
while IFS= read -r selected; do
    [[ -n "${selected}" ]] || continue

    case "${selected}" in
        google|authd-google)
            broker="authd-google"
            ;;
        msentraid|authd-msentraid)
            broker="authd-msentraid"
            ;;
        *)
            echo "::warning::Ignoring unknown broker '${selected}' in e2e-brokers marker"
            continue
            ;;
    esac

    if [[ -z "${seen_brokers[${broker}]:-}" ]]; then
        seen_brokers["${broker}"]=1
        brokers+=("${broker}")
    fi
done < <(marker_values e2e-brokers)

if ((${#brokers[@]} == 0)); then
    brokers=(authd-msentraid)
fi

ubuntu_releases=()
declare -A seen_ubuntu_releases=()
while IFS= read -r selected; do
    [[ -n "${selected}" ]] || continue

    case "${selected}" in
        noble|resolute|devel)
            ubuntu_release="${selected}"
            ;;
        *)
            echo "::warning::Ignoring unknown Ubuntu release '${selected}' in e2e-ubuntu-releases marker"
            continue
            ;;
    esac

    if [[ -z "${seen_ubuntu_releases[${ubuntu_release}]:-}" ]]; then
        seen_ubuntu_releases["${ubuntu_release}"]=1
        ubuntu_releases+=("${ubuntu_release}")
    fi
done < <(marker_values e2e-ubuntu-releases)

if ((${#ubuntu_releases[@]} == 0)); then
    ubuntu_releases=(noble resolute devel)
fi

tests=()
while IFS= read -r test; do
    [[ -n "${test}" ]] || continue
    tests+=("${test}")
done < <(marker_values e2e-tests)

test_cases=()
while IFS= read -r test_case; do
    [[ -n "${test_case}" ]] || continue
    test_cases+=("${test_case}")
done < <(marker_lines e2e-test-case)

parse_apt_source_marker() {
    local marker="$1"
    local output_var="$2"
    local default_source="${3:-}"
    local selected_apt_source
    local parsed_source=

    while IFS= read -r selected_apt_source; do
        [[ -n "${selected_apt_source}" ]] || continue

        local normalized_source
        if ! normalized_source="$(normalize_apt_source "${selected_apt_source}")"; then
            echo "::error::Invalid APT source '${selected_apt_source}' in ${marker} marker"
            exit 1
        fi

        if [[ -n "${parsed_source}" && "${parsed_source}" != "${normalized_source}" ]]; then
            echo "::error::Conflicting APT sources '${parsed_source}' and '${normalized_source}' in ${marker} marker"
            exit 1
        fi

        if [[ -z "${parsed_source}" ]]; then
            parsed_source="${normalized_source}"
        fi
    done < <(marker_values "${marker}")

    if [[ -z "${parsed_source}" ]]; then
        parsed_source="${default_source}"
    fi

    printf -v "${output_var}" '%s' "${parsed_source}"
}

if [[ -n "$(marker_lines e2e-ppa)" ]]; then
    echo "::error::The e2e-ppa marker was removed; use e2e-apt-source or e2e-authd-apt-source"
    exit 1
fi

apt_source=
parse_apt_source_marker e2e-apt-source apt_source "${AUTHD_DEFAULT_APT_SOURCE}"

authd_apt_source=
parse_apt_source_marker e2e-authd-apt-source authd_apt_source

json_array() {
    if (($# == 0)); then
        printf '[]'
    else
        printf '%s\n' "$@" | jq -R . | jq -sc .
    fi
}

{
    printf 'brokers=%s\n' "$(json_array "${brokers[@]}")"
    printf 'ubuntu_releases=%s\n' "$(json_array "${ubuntu_releases[@]}")"
    printf 'tests=%s\n' "$(json_array "${tests[@]}")"
    printf 'test_cases=%s\n' "$(json_array "${test_cases[@]}")"
    printf 'apt_source=%s\n' "${apt_source}"
    printf 'authd_apt_source=%s\n' "${authd_apt_source}"
} >>"${GITHUB_OUTPUT}"
