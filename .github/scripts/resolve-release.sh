#!/bin/bash
set -euo pipefail

: "${RELEASE:?RELEASE must be set}"
: "${SELECTED_RELEASES:?SELECTED_RELEASES must be set}"
: "${APT_SOURCE:=}"
: "${AUTHD_APT_SOURCE:=}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

# shellcheck source=../../e2e-tests/vm/lib/libprovision.sh
source "$(dirname "${BASH_SOURCE[0]}")/../../e2e-tests/vm/lib/libprovision.sh"

codename="$(resolve_devel_release "${RELEASE}")"

validate_archive_source() {
    local source="$1"
    local source_name="$2"

    if [[ -z "${source}" || "${source}" == ppa:* ]]; then
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

validate_archive_source "${APT_SOURCE}" target
validate_archive_source "${AUTHD_APT_SOURCE}" authd
printf 'codename=%s\n' "${codename}" >>"${GITHUB_OUTPUT}"
