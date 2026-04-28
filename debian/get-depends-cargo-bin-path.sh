#!/bin/sh

set -eu

debian_path=$(dirname "$0")
min_cargo_versions=$(grep-dctrl -s Build-Depends -n - "${debian_path}"/control | \
    grep -v '^\s*#' | \
    grep -oP '(?<=cargo-)[0-9.]+')

if [ -z "${min_cargo_versions}" ]; then
    echo >&2 "No cargo version specified in Build-Depends."
    exit 1
fi

default_cargo_wrapper="/usr/share/cargo/bin/cargo"
default_cargo_wrapper_dir="${default_cargo_wrapper%/cargo}"
default_cargo_version=$(dpkg-query -W -f='${Version}' cargo 2>/dev/null || true)

for min_cargo_version in ${min_cargo_versions}; do
    versioned_cargo_wrapper="/usr/lib/rust-${min_cargo_version}/share/cargo/bin/cargo"
    versioned_cargo_wrapper_dir="${versioned_cargo_wrapper%/cargo}"
    versioned_bin_dir="/usr/lib/rust-${min_cargo_version}/bin"

    if [ -x "${default_cargo_wrapper}" ] && \
       dpkg --compare-versions "${default_cargo_version}" ge "${min_cargo_version}" 2>/dev/null; then
        echo >&2 "Using default cargo at ${default_cargo_wrapper} (version ${default_cargo_version})"
        echo "${default_cargo_wrapper_dir}"
        exit 0
    else
        echo >&2 "Default cargo at ${default_cargo_wrapper} does not meet the minimum version requirement of ${min_cargo_version} (found version '${default_cargo_version}')."
    fi

    if [ -x "${versioned_cargo_wrapper}" ]; then
        echo >&2 "Using versioned cargo at ${versioned_cargo_wrapper}"
        echo "${versioned_cargo_wrapper_dir}:${versioned_bin_dir}"
        exit 0
    fi

    echo >&2 "Versioned cargo at ${versioned_cargo_wrapper} does not exist or is not executable."
done

echo >&2 "No suitable cargo version found for minimum required version ${min_cargo_versions}."
exit 1
