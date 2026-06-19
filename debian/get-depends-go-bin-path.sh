#!/bin/sh

set -eu

debian_path=$(dirname "$0")
backported_go_version=$(grep-dctrl -s Build-Depends -n - "${debian_path}"/control | \
    sed -n "s,.*\bgolang-\([0-9.]\+\)\b.*,\1,p")

if [ -n "${backported_go_version}" ]; then
    echo "/usr/lib/go-${backported_go_version}/bin"
fi
