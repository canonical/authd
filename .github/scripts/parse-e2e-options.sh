#!/bin/bash
set -euo pipefail

: "${E2E_TESTS_DESCRIPTION:=}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

body_without_comments=$(
    tr -d '\r' <<<"${E2E_TESTS_DESCRIPTION}" |
        perl -0pe 's/<!--.*?(?:-->|$)//gs'
)

marker_values() {
    local marker="$1"

    sed -nE \
        "s/^[[:space:]]*${marker}:[[:space:]]*(.+)[[:space:]]*$/\1/p" \
        <<<"${body_without_comments}" |
        tr -s ' \t,' '\n'
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
    brokers=(authd-msentraid authd-google)
fi

tests=()
while IFS= read -r test; do
    [[ -n "${test}" ]] || continue
    tests+=("${test}")
done < <(marker_values e2e-tests)

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

json_array() {
    if (($# == 0)); then
        printf '[]'
    else
        printf '%s\n' "$@" | jq -R . | jq -sc .
    fi
}

{
    printf 'brokers=%s\n' "$(json_array "${brokers[@]}")"
    printf 'tests=%s\n' "$(json_array "${tests[@]}")"
    printf 'authd_ppa=%s\n' "${authd_ppa}"
} >>"${GITHUB_OUTPUT}"
