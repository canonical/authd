#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(readlink -f "$(dirname "$0")")
BROKERS_DIR=$(readlink -f "${SCRIPT_DIR}/../../../..")
LIBHIMMELBLAU_DIR="${BROKERS_DIR}/third_party/libhimmelblau"

if [ ! -f "${LIBHIMMELBLAU_DIR}/Cargo.toml" ]; then
  echo "libhimmelblau source is missing. On the host, run: git submodule update --init --recursive -- brokers/third_party/libhimmelblau" >&2
  exit 1
fi

"${BROKERS_DIR}/tools/install-cargo-c"
cargo install --locked cbindgen

cd "$LIBHIMMELBLAU_DIR"

mkdir -p himmelblau

# Print executed commands to ease debugging
set -x

"${CARGO_HOME:-$HOME/.cargo}"/bin/cbindgen --config ./cbindgen.toml > himmelblau/himmelblau.h

FEATURES="broker,changepassword,on_behalf_of,set_timeout"
# Enable custom_oidc_discovery_url feature when not building a release,
# which is the case when building inside snapcraft or when the RELEASE env
# var is set (the latter can be used during development).
if [ -z "${SNAPCRAFT_PROJECT_NAME:-}" ] && [ -z "${RELEASE:-}" ]; then
  FEATURES="${FEATURES},custom_oidc_discovery_url"
fi
TARGET_DIR="${CARGO_TARGET_DIR:-target}"
TARGET_TRIPLE=$(rustc -vV | awk '$1 == "host:" {print $2}')
cargo cbuild --release --lib --features="${FEATURES}" --target-dir "$TARGET_DIR" --target "$TARGET_TRIPLE"

# Copy header and shared library
cp "${TARGET_DIR}/${TARGET_TRIPLE}/release/himmelblau.h" "${SCRIPT_DIR}/"
cp "${TARGET_DIR}/${TARGET_TRIPLE}/release/libhimmelblau.so" "${SCRIPT_DIR}/libhimmelblau.so.0"
ln -sf libhimmelblau.so.0 "${SCRIPT_DIR}/libhimmelblau.so"
