#!/usr/bin/env bash

set -euo pipefail

readonly LAUNCHPAD_API="https://api.launchpad.net/1.0"
readonly UBUNTU_ARCHIVE="${LAUNCHPAD_API}/ubuntu/+archive/primary"

# The currently published Noble Security source predates this watcher.
readonly NOBLE_SECURITY_BASELINE="2024-08-15T13:46:21.864748+00:00"

declare -A UBUNTU_SALSA_BRANCHES=(
    [noble]="ubuntu/noble"
)

declare -A AUTHD_PPA_SALSA_BRANCHES=(
    [noble]="ubuntu/noble-authd"
)

launchpad_sources() {
    local series="$1"
    local pocket="$2"
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
        "${UBUNTU_ARCHIVE}?ws.op=getPublishedSources&source_name=gnome-shell&distro_series=${encoded_series}&pocket=${pocket}&status=Published&ws.size=100"
}

source_versions() {
    local json="$1"
    local pocket="$2"
    local baseline="$3"

    jq -r \
        --arg pocket "${pocket}" \
        --arg baseline "${baseline}" '
        [
            .entries[]?
            | select(
                .source_package_name == "gnome-shell"
                and .pocket == $pocket
                and ($baseline == "" or .date_published > $baseline)
            )
        ]
        | sort_by(.date_published)[]
        | .source_package_version as $version
        | [
            $version,
            .date_published,
            "https://launchpad.net/ubuntu/+source/gnome-shell/" + ($version | @uri)
        ]
        | @tsv
    ' <<<"${json}"
}

issue_body() {
    local series="$1"
    local pocket="$2"
    local ubuntu_version="$3"
    local ubuntu_date="$4"
    local ubuntu_link="$5"
    local ubuntu_salsa_branch="${UBUNTU_SALSA_BRANCHES[${series}]}"
    local ppa_salsa_branch="${AUTHD_PPA_SALSA_BRANCHES[${series}]}"
    local pocket_name="${pocket,,}"

    cat <<EOF
<!-- gnome-shell-watch: series=${series} pocket=${pocket_name} version=${ubuntu_version} -->
A new Ubuntu \`gnome-shell\` source has been published to the **${series}-${pocket_name}** pocket.

- ${pocket} source: [\`gnome-shell ${ubuntu_version}\` on Launchpad](${ubuntu_link})
- Published: ${ubuntu_date}
- Salsa branch: [${ubuntu_salsa_branch}](https://salsa.debian.org/gnome-team/gnome-shell/-/tree/${ubuntu_salsa_branch})

### Expected action

1. Review the Ubuntu source changes and identify what must be carried into the
   \`${series}\` authd PPA package.
2. Apply the required changes on the authd PPA branch
   [\`${ppa_salsa_branch}\`](https://salsa.debian.org/gnome-team/gnome-shell/-/tree/${ppa_salsa_branch}),
   update the Debian changelog, and open the related merge request(s).
3. Build and test the updated package in the
   [\`authd-dev\` PPA](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-dev),
   following the test steps in the [GNOME Shell release checklist](https://github.com/canonical/authd/blob/main/RELEASE.md#release-the-gnome-shell-authd-integration).
   After merging, publish it to [\`authd-edge\`](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge),
   validate it there, then copy it to the [stable \`authd\` PPA](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd).

Close this issue after reviewing this upload and either carrying any required
changes into the stable \`authd\` PPA or confirming that no changes are needed.
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
    local pocket="$2"
    local ubuntu_version="$3"
    local ubuntu_date="$4"
    local ubuntu_link="$5"
    local title="[gnome-shell] ${series} ${ubuntu_version}"
    local body
    local issue_number
    local previous_body

    body="$(issue_body \
        "${series}" \
        "${pocket}" \
        "${ubuntu_version}" \
        "${ubuntu_date}" \
        "${ubuntu_link}")"

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
    if [ "${previous_body}" != "${body}" ]; then
        gh api \
            --method PATCH \
            "repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
            --field "body=${body}" \
            >/dev/null
    fi
}

declare -A seen_versions=()

for series in "${!AUTHD_PPA_SALSA_BRANCHES[@]}"; do
    for pocket in Proposed Security; do
        ubuntu_sources="$(launchpad_sources "${series}" "${pocket}")"
        baseline=""
        if [ "${series}" = noble ] && [ "${pocket}" = Security ]; then
            baseline="${NOBLE_SECURITY_BASELINE}"
        fi
        versions="$(source_versions "${ubuntu_sources}" "${pocket}" "${baseline}")"
        if [ -z "${versions}" ]; then
            continue
        fi

        while IFS=$'\t' read -r ubuntu_version ubuntu_date ubuntu_link; do
            version_key="${series}:${ubuntu_version}"
            if [ -n "${seen_versions[${version_key}]:-}" ]; then
                continue
            fi
            seen_versions["${version_key}"]=true
            notify_release \
                "${series}" \
                "${pocket}" \
                "${ubuntu_version}" \
                "${ubuntu_date}" \
                "${ubuntu_link}"
        done <<<"${versions}"
    done
done
