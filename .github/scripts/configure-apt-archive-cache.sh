#!/bin/bash

set -euo pipefail

APT_ARCHIVES_DIR="${RUNNER_TEMP}/e2e-apt-cache/archives"
mkdir -p "${APT_ARCHIVES_DIR}/partial"

printf 'Dir::Cache::archives "%s/";\nAPT::Keep-Downloaded-Packages "true";\n' \
    "${APT_ARCHIVES_DIR}" \
    | sudo tee /etc/apt/apt.conf.d/zzzz-e2e-apt-cache >/dev/null
