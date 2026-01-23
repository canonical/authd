#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(readlink -f "$(dirname "$0")")
cd "$SCRIPT_DIR"

GIT_DIR=$(git rev-parse --show-toplevel)

# Verify we are in a git repo and get the top-level dir
if [ -z "${GIT_DIR}" ]; then
  echo >&2 "Error: Not inside a git repository"
  exit 1
fi

cargo install cargo-c cbindgen

cd "${GIT_DIR}/third_party/libhimmelblau"

mkdir -p himmelblau

# Print executed commands to ease debugging
set -x

"${CARGO_HOME:-$HOME/.cargo}"/bin/cbindgen --config ./cbindgen.toml > himmelblau/himmelblau.h

FEATURES="broker,changepassword,on_behalf_of"
# Enable custom_oidc_discovery_url feature when not building a release,
# which is the case when building inside snapcraft or when the RELEASE env
# var is set (the latter can be used during development).
if [ -z "${SNAPCRAFT_PROJECT_NAME:-}" ] && [ -z "${RELEASE:-}" ]; then
  FEATURES="${FEATURES},custom_oidc_discovery_url"
fi
cargo cbuild --release --lib --features="${FEATURES}"

# Copy header and shared library
TARGET_TRIPLE=$(rustc -vV | awk '/host:/ {print $2}')
cp "target/${TARGET_TRIPLE}/release/himmelblau.h" "${SCRIPT_DIR}/"
cp "target/${TARGET_TRIPLE}/release/libhimmelblau.so" "${SCRIPT_DIR}/libhimmelblau.so.0"
ln -sf libhimmelblau.so.0 "${SCRIPT_DIR}/libhimmelblau.so"
