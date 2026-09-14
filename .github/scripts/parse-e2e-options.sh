#!/bin/bash
set -euo pipefail

: "${E2E_TESTS_DESCRIPTION:=}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

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

authd_ppa=
while IFS= read -r ppa; do
    case "${ppa}" in
        authd-dev)
            authd_ppa="ubuntu-enterprise-desktop/authd-dev"
            break
            ;;
        "")
            ;;
        *)
            echo "::warning::Ignoring unknown PPA '${ppa}' in e2e-ppa marker"
            ;;
    esac
done < <(marker_values e2e-ppa)

apt_source=
while IFS= read -r selected_apt_source; do
    [[ -n "${selected_apt_source}" ]] || continue

    if [[ ! "${selected_apt_source}" =~ ^[a-z0-9][a-z0-9+.-]*$ ]]; then
        echo "::error::Invalid APT source '${selected_apt_source}' in e2e-apt-source marker"
        exit 1
    fi

    if [[ -n "${apt_source}" ]]; then
        echo "::warning::Ignoring additional APT source '${selected_apt_source}' in e2e-apt-source marker"
        continue
    fi

    apt_source="${selected_apt_source}"
done < <(marker_values e2e-apt-source)

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
    printf 'authd_ppa=%s\n' "${authd_ppa}"
    printf 'apt_source=%s\n' "${apt_source}"
} >>"${GITHUB_OUTPUT}"
