#!/usr/bin/env bash

yarf_environment_revision() (
    cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" || exit
    {
        # submodule update uses the index, which may differ from HEAD.
        git -C .. rev-parse :e2e-tests/.yarf
        sha256sum pyproject.toml uv.lock
    } | sha256sum | cut -d' ' -f1
)
