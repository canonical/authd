#!/usr/bin/env bash

set -exuo pipefail

# Skip tests which depend on vhs which is not available in the build environment.
export AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1

# Skip flaky tests because we don't want autopkgtests to fail, which would cause
# trouble for maintainers of packages which authd depends on.
export AUTHD_SKIP_FLAKY_TESTS=1

export GOPROXY=off
export GOTOOLCHAIN=local

PATH=$PATH:$("$(dirname "$0")"/../get-depends-go-bin-path.sh)
export PATH

go test ./...
