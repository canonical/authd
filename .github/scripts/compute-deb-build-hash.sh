#!/bin/bash

set -euo pipefail

# Add all files in the repo, except for those that we know are not relevant for
# the build of the Debian package.
relevant_files=$(git ls-files | grep -v \
    -e '^\.github/' \
    -e '^\.gitignore$' \
    -e '^\.gitmodules$' \
    -e '^\.golangci\.yaml$' \
    -e '\.md$' \
    -e '^COPYING' \
    -e '^authd-oidc-brokers/' \
    -e '^docs/' \
    -e '^e2e-tests/' \
    -e '^examplebroker/' \
    -e '^gotestcov$' \
    -e '^snap/' \
    -e '_test\.go$' \
    -e '/testdata/' \
    -e '/testutils/')

# We excluded the .github directory, so we need to add the workflow file back,
# since it is relevant for the build of the Debian package.
relevant_files="${relevant_files}"$'\n'".github/workflows/debian-build.yaml"

# Sort the files
relevant_files=$(echo "${relevant_files}" | sort -u)

# Print the files for debugging purposes.
echo >&2 "Build-relevant files: ${relevant_files}"

hash=$(echo "${relevant_files}" |
    grep -e '.' |
    xargs git hash-object | sha256sum | cut -d' ' -f1)

# Print the hash for debugging purposes.
echo >&2 "Hash: ${hash}"

echo "${hash}"
