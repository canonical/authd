#!/usr/bin/env bash
set -euo pipefail
set -x

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
YARF_DIR="${SCRIPT_DIR}/.yarf"

# Ensure that the YARF submodule is initialized
git -C "${SCRIPT_DIR}/.." submodule update --init --depth=1 e2e-tests/.yarf

# Install uv snap if not already installed
if ! command -v uv &> /dev/null; then
    echo "Installing uv snap..."
    sudo snap install --classic astral-uv
else
    echo "uv snap already installed"
fi

# Set up YARF in a virtual environment using uv
cd "$YARF_DIR"

# YARF generates Wayland bindings while it is built. Hatch uses an isolated
# environment for this step, so its unpinned pywayland build dependency can
# differ from the version installed for runtime from uv.lock. Newer
# pywayland releases can generate bindings that are incompatible with older
# runtime releases. That mismatch makes YARF fail while importing generated
# code. Constrain the build dependency to the lockfile version so generated
# code and runtime code always use the same pywayland API.
PYWAYLAND_VERSION=$(
    awk '
        /^\[\[package\]\]$/ { in_package = 0 }
        /^name = "pywayland"$/ { in_package = 1; next }
        in_package && /^version = / {
            gsub(/"/, "", $3)
            print $3
            exit
        }
    ' uv.lock
)
: "${PYWAYLAND_VERSION:?Could not find pywayland in uv.lock}"
BUILD_CONSTRAINTS_FILE=$(mktemp)
trap 'rm -f "$BUILD_CONSTRAINTS_FILE"' EXIT
printf 'pywayland==%s\n' "$PYWAYLAND_VERSION" > "$BUILD_CONSTRAINTS_FILE"
export UV_BUILD_CONSTRAINT="$BUILD_CONSTRAINTS_FILE"

uv sync
uv pip install '.[develop]'
# We need pygobject in the Python environment for some tests
uv pip install pygobject
# We need ansi2html to log colored journalctl output as HTML
uv pip install ansi2html
uv pip install "$YARF_DIR"
