#!/usr/bin/env bash

set -euo pipefail

SCRIPT_PATH=$(dirname "$0")
PROJECT_ROOT=$(realpath "$SCRIPT_PATH")/..
module_libname=pam_authd.so
exec_libname=pam_authd_exec.so

output_dir=""
if [ "${1:-}" = --build-only ]; then
    if [ "$#" -ne 2 ] || [ ! -d "$2" ]; then
        echo "Usage: $0 --build-only OUTPUT_DIRECTORY" >&2
        exit 2
    fi
    output_dir=$(realpath -- "$2")
fi

cd "$SCRIPT_PATH"
exec_output=go-exec/"$exec_libname"

if [ -n "$output_dir" ]; then
    # Use the committed wrapper without running generators in the source tree.
    go build "-ldflags=-extldflags -Wl,-soname,${module_libname}" -buildmode=c-shared \
        -o "${output_dir}/${module_libname}" -tags go_pam_module .
    exec_output="${output_dir}/${exec_libname}"
elif [ -d "$PROJECT_ROOT"/vendor ]; then
    echo Vendored dependencies detected, not re-generating pam_module.go
else
    go run github.com/msteinert/pam/v2/cmd/pam-moduler \
        -libname "$module_libname" -type pamModule -no-main \
        "${@}"
    go generate -x -tags pam_module_generation
fi

# shellcheck disable=SC2086,SC2046
# we do want to do word splitting on flags
${CC:-cc} -o "$exec_output" \
    go-exec/module.c ${CFLAGS:--Wall} \
    $(pkg-config --cflags gio-2.0 gio-unix-2.0) \
    -Wl,--as-needed -Wl,--allow-shlib-undefined \
    -shared -fPIC -Wl,--unresolved-symbols=report-all \
    -Wl,-soname,"$exec_libname" \
    $(pkg-config --libs gio-2.0 gio-unix-2.0) \
    -lpam ${LDFLAGS:-}

chmod 644 "$exec_output"
