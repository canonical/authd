#!/usr/bin/env bash

set -exuo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"

# Skip flaky tests because we don't want autopkgtests to fail, which would cause
# trouble for maintainers of packages which authd depends on.
export AUTHD_SKIP_FLAKY_TESTS=1

export GOPROXY=off
export GOTOOLCHAIN=local

PATH=$("${SCRIPT_DIR}/../get-depends-cargo-bin-paths.sh"):$("${SCRIPT_DIR}/../get-depends-go-bin-path.sh"):$PATH
export PATH

go test ./...
