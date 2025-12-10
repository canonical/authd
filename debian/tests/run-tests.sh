#!/usr/bin/env bash

set -exuo pipefail

export AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1
export GOPROXY=off
export GOTOOLCHAIN=local

PATH=$PATH:$("$(dirname "$0")"/../get-depends-go-bin-path.sh)
export PATH

go test ./...
