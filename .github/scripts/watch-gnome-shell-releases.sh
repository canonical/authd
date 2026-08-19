#!/usr/bin/env bash

set -euo pipefail

readonly LAUNCHPAD_API="https://api.launchpad.net/1.0"
readonly UBUNTU_ARCHIVE="${LAUNCHPAD_API}/ubuntu/+archive/primary"
readonly AUTHD_DEV_PPA="${LAUNCHPAD_API}/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-dev"

declare -A SALSA_BRANCHES=(
    [noble]="ubuntu/noble"
    [resolute]="ubuntu/resolute"
    [stonking]="ubuntu/latest"
)

launchpad_sources() {
    local archive="$1"
    local series="$2"
    local encoded_series

    encoded_series="$(jq -rn \
        --arg series "https://api.launchpad.net/1.0/ubuntu/${series}" \
        '$series | @uri')"

    curl \
        --fail \
        --location \
        --retry 3 \
        --retry-delay 5 \
        --silent \
        --show-error \
        "${archive}?ws.op=getPublishedSources&source_name=gnome-shell&distro_series=${encoded_series}&status=Published&ws.size=100"
}

latest_source_version() {
    local json="$1"
    local series="$2"
    local source

    source="$(jq -r --arg series "$series" '
        [
            .entries[]?
            | select(
                .source_package_name == "gnome-shell"
                and .pocket != "Proposed"
            )
        ]
        | if length == 0
          then error("No published gnome-shell source found for " + $series)
          else max_by(.date_published)
          end
        | [.source_package_version, .date_published, .self_link]
        | @tsv
    ' <<<"${json}")"

    printf '%s\n' "${source}"
}

issue_body() {
    local series="$1"
    local ubuntu_version="$2"
    local ubuntu_date="$3"
    local ubuntu_link="$4"
    local ppa_version="$5"
    local salsa_branch="${SALSA_BRANCHES[${series}]}"

    cat <<EOF
<!-- gnome-shell-watch: series=${series} version=${ubuntu_version} -->
Ubuntu's published \`gnome-shell\` version is ahead of \`authd-dev\` for
**${series}**.

- Ubuntu version: \`${ubuntu_version}\`
- Published: ${ubuntu_date}
- Ubuntu source publication: [Launchpad](${ubuntu_link})
- Salsa packaging branch: [${salsa_branch}](https://salsa.debian.org/gnome-team/gnome-shell/-/tree/${salsa_branch})
- Latest \`authd-dev\` version: \`${ppa_version:-not published}\`

This watcher is temporary. It can be removed after the authd patches are
merged into the Ubuntu gnome-shell branches.
EOF
}

find_issue_number() {
    local title="$1"
    local issues

    issues="$(gh api \
        --paginate \
        --slurp \
        "repos/${GITHUB_REPOSITORY}/issues?state=all&per_page=100")"
    jq -r --arg title "${title}" '
        [
            .[][]?
            | select(.pull_request == null and .title == $title)
        ]
        | sort_by(.number)
        | last
        | .number // empty
    ' <<<"${issues}"
}

notify_release() {
    local series="$1"
    local ubuntu_version="$2"
    local ubuntu_date="$3"
    local ubuntu_link="$4"
    local ppa_version="$5"
    local title="[gnome-shell] ${series} release notification"
    local body
    local issue_number
    local previous_body
    local comment

    body="$(issue_body \
        "${series}" \
        "${ubuntu_version}" \
        "${ubuntu_date}" \
        "${ubuntu_link}" \
        "${ppa_version}")"

    if [ "${DRY_RUN:-false}" = true ]; then
        printf '%s\n\n%s\n' "${title}" "${body}"
        return
    fi

    issue_number="$(find_issue_number "${title}")"

    if [ -z "${issue_number}" ]; then
        gh api \
            --method POST \
            "repos/${GITHUB_REPOSITORY}/issues" \
            --field "title=${title}" \
            --field "body=${body}" \
            >/dev/null
        return
    fi

    previous_body="$(gh api \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
        --jq '.body // ""')"
    if grep -Fq "series=${series} version=${ubuntu_version}" <<<"${previous_body}"; then
        return
    fi

    gh api \
        --method PATCH \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
        --field "body=${body}" \
        --field state=open \
        >/dev/null

    comment="$(cat <<EOF
Ubuntu published a new \`gnome-shell\` version for **${series}**:
\`${ubuntu_version}\`

The previous notification has been updated. The \`authd-dev\` version is
\`${ppa_version:-not published}\`.
EOF
)"
    gh api \
        --method POST \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}/comments" \
        --field "body=${comment}" \
        >/dev/null
}

resolve_release() {
    local series="$1"
    local ubuntu_version="$2"
    local ppa_version="$3"
    local title="[gnome-shell] ${series} release notification"
    local issue_number
    local issue_state

    if [ "${DRY_RUN:-false}" = true ]; then
        printf '%s: Ubuntu=%s, authd-dev=%s; no notification needed\n' \
            "${series}" \
            "${ubuntu_version}" \
            "${ppa_version:-not published}"
        return
    fi

    issue_number="$(find_issue_number "${title}")"
    if [ -z "${issue_number}" ]; then
        return
    fi

    issue_state="$(gh api \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
        --jq '.state')"
    if [ "${issue_state}" != open ]; then
        return
    fi

    gh api \
        --method PATCH \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
        --field state=closed \
        >/dev/null
    gh api \
        --method POST \
        "repos/${GITHUB_REPOSITORY}/issues/${issue_number}/comments" \
        --field "body=The \`authd-dev\` version (\`${ppa_version}\`) now covers the Ubuntu version (\`${ubuntu_version}\`) for **${series}**. Closing this notification." \
        >/dev/null
}

for series in noble resolute stonking; do
    ubuntu_sources="$(launchpad_sources "${UBUNTU_ARCHIVE}" "${series}")"
    IFS=$'\t' read -r ubuntu_version ubuntu_date ubuntu_link < <(
        latest_source_version "${ubuntu_sources}" "${series}"
    )

    ppa_sources="$(launchpad_sources "${AUTHD_DEV_PPA}" "${series}")"
    ppa_version=""
    if jq -e '
        any(.entries[]?; .source_package_name == "gnome-shell" and .pocket != "Proposed")
    ' <<<"${ppa_sources}" >/dev/null; then
        IFS=$'\t' read -r ppa_version _ _ < <(
            latest_source_version "${ppa_sources}" "${series}"
        )
    fi

    if [ -z "${ppa_version}" ] || \
        dpkg --compare-versions "${ubuntu_version}" gt "${ppa_version}"; then
        notify_release \
            "${series}" \
            "${ubuntu_version}" \
            "${ubuntu_date}" \
            "${ubuntu_link}" \
            "${ppa_version}"
    else
        resolve_release "${series}" "${ubuntu_version}" "${ppa_version}"
    fi
done
