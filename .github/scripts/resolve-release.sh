#!/bin/bash
set -euo pipefail

: "${RELEASE:?RELEASE must be set}"
: "${SELECTED_RELEASES:?SELECTED_RELEASES must be set}"
: "${APT_SOURCES:=[]}"
: "${AUTHD_APT_SOURCES:=[]}"
: "${APT_SOURCE_BASE:=}"
: "${AUTHD_APT_SOURCE_BASE:=}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

# shellcheck source=../../e2e-tests/vm/lib/libprovision.sh
source "$(dirname "${BASH_SOURCE[0]}")/../../e2e-tests/vm/lib/libprovision.sh"

codename="$(resolve_devel_release "${RELEASE}")"

if ! jq -e 'type == "array" and all(.[]; type == "string")' \
    <<<"${SELECTED_RELEASES}" >/dev/null; then
    echo "::error::Invalid selected Ubuntu releases JSON." >&2
    exit 1
fi

normalize_json_sources() {
    local json="$1"
    local source_name="$2"
    local output_name="$3"
    local -a raw_sources=()

    if ! jq -e 'type == "array" and all(.[]; type == "string")' \
        <<<"${json}" >/dev/null; then
        echo "::error::Invalid ${source_name} JSON." >&2
        exit 1
    fi

    mapfile -t raw_sources < <(jq -r '.[]' <<<"${json}")
    if ! normalize_apt_sources_into_array "${output_name}" "${raw_sources[@]}"; then
        echo "::error::Invalid source in ${source_name}." >&2
        exit 1
    fi
}

validate_archive_source() {
    local source="$1"
    local source_name="$2"

    if [[ -z "${source}" ]]; then
        return
    fi
    if [[ "${source}" == ppa:* ]]; then
        if [[ "${source_name}" == "base" ]]; then
            echo "::error::Archive base source '${source}' must be an Ubuntu archive suite." >&2
            exit 1
        fi
        return
    fi

    if [[ "${source}" == "${codename}" || "${source}" == "${codename}-"* ]]; then
        return
    fi

    while IFS= read -r selected_release; do
        if [[ "${selected_release}" == "devel" ]]; then
            continue
        fi
        if [[ "${source}" == "${selected_release}" || "${source}" == "${selected_release}-"* ]]; then
            return
        fi
    done < <(jq -r '.[]' <<<"${SELECTED_RELEASES}")

    # Stable jobs cannot know the current devel codename. Defer this
    # check to the devel job when it is part of the matrix.
    if [[ "${RELEASE}" != "devel" ]] &&
        jq -e 'index("devel") != null' <<<"${SELECTED_RELEASES}" >/dev/null; then
        return
    fi

    echo "::error::Archive ${source_name} '${source}' does not match any selected Ubuntu release." >&2
    exit 1
}

json_array() {
    if (($# == 0)); then
        printf '[]'
    else
        printf '%s\n' "$@" | jq -R . | jq -sc .
    fi
}

apt_source_candidates=()
authd_apt_source_candidates=()
normalize_json_sources "${APT_SOURCES}" "system sources" apt_source_candidates
normalize_json_sources "${AUTHD_APT_SOURCES}" "authd sources" authd_apt_source_candidates

for source in "${apt_source_candidates[@]}"; do
    validate_archive_source "${source}" target
done
for source in "${authd_apt_source_candidates[@]}"; do
    validate_archive_source "${source}" authd
done
validate_archive_source "${APT_SOURCE_BASE}" base
validate_archive_source "${AUTHD_APT_SOURCE_BASE}" authd-base

apt_sources=()
for source in "${apt_source_candidates[@]}"; do
    if is_ppa_source "${source}" ||
       [[ "${source}" == "${codename}" || "${source}" == "${codename}-"* ]]; then
        apt_sources+=("${source}")
    fi
done
if ((${#apt_sources[@]} == 0)); then
    apt_sources=(
        "${codename}-proposed"
        "${AUTHD_DEFAULT_APT_SOURCE}"
    )
fi

authd_apt_sources=()
for source in "${authd_apt_source_candidates[@]}"; do
    if is_ppa_source "${source}" ||
       [[ "${source}" == "${codename}" || "${source}" == "${codename}-"* ]]; then
        authd_apt_sources+=("${source}")
    fi
done

{
    printf 'codename=%s\n' "${codename}"
    printf 'apt_sources=%s\n' "$(json_array "${apt_sources[@]}")"
    printf 'authd_apt_sources=%s\n' "$(json_array "${authd_apt_sources[@]}")"
} >>"${GITHUB_OUTPUT}"
