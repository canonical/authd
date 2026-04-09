#!/bin/sh

set -eu

debian_path=$(dirname "$0")
min_cargo_version=$(grep-dctrl -s Build-Depends -n - "${debian_path}"/control | \
    sed -n "s,.*\bcargo-\([0-9.]\+\)\b.*,\1,p")

if [ -z "${min_cargo_version}" ]; then
    echo >&2 "No cargo version specified in Build-Depends."
    exit 1
fi

versioned_cargo_wrapper="/usr/lib/rust-${min_cargo_version}/share/cargo/bin/cargo"
versioned_bin_dir="/usr/lib/rust-${min_cargo_version}/bin"
default_cargo_wrapper="/usr/share/cargo/bin/cargo"
default_cargo_version=$(dpkg-query -W -f='${Version}' cargo 2>/dev/null || true)

if [ -x "${default_cargo_wrapper}" ] && \
   dpkg --compare-versions "${default_cargo_version}" ge "${min_cargo_version}" 2>/dev/null; then
    echo >&2 "Using default cargo at ${default_cargo_wrapper} (version ${default_cargo_version})"
    dirname "${default_cargo_wrapper}"
    exit 0
else
    echo >&2 "Default cargo at ${default_cargo_wrapper} does not meet the minimum version requirement of ${min_cargo_version} (found version '${default_cargo_version}')."
fi

if [ ! -x "${versioned_cargo_wrapper}" ]; then
    echo >&2 "Versioned cargo at ${versioned_cargo_wrapper} does not exist or is not executable."
    exit 1
fi

echo >&2 "Using versioned cargo at ${versioned_cargo_wrapper}"
echo "$(dirname "${versioned_cargo_wrapper}"):${versioned_bin_dir}"
