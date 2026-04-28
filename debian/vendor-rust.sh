#!/bin/sh
set -eu

CARGO_HOME=${DEB_CARGO_HOME:-$(mktemp --tmpdir -d -t "cargo-home-XXXXXX")}
export CARGO_HOME
trap '[ -z "${DEB_CARGO_HOME:-}" ] && rm -rf "$CARGO_HOME"' EXIT INT HUP

# We need a filtered vendored directory
if ! command -v cargo-vendor-filterer 2>/dev/null; then
    echo "ERROR: could not find cargo-vendor-filterer in PATH to filter vendored dependencies." >&2
    echo "Please install cargo-vendor-filterer to run this script. More info at https://github.com/coreos/cargo-vendor-filterer." >&2
    exit 3
fi

# Print the versions of cargo and cargo-vendor-filterer for debugging purposes.
echo "PATH: $PATH"
echo "Using cargo: $(which cargo)"
echo "Using cargo version: $(cargo --version)"
echo "Using cargo-vendor-filterer version: $(cargo-vendor-filterer --version)"

# Some crates are shipped with .a files, which get removed by the helpers during the package build as a safety measure.
# This results in cargo failing to compile, since the files (which are listed in the checksums) are not there anymore.
# For those crates, we need to replace their checksum with a more general one that only lists the crate checksum, instead of each file.
cargo vendor-filterer "${CARGO_VENDOR_DIR}"
