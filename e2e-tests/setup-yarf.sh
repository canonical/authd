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
uv sync
uv pip install '.[develop]'
# We need pygobject in the Python environment for some tests
uv pip install pygobject
# We need ansi2html to log colored journalctl output as HTML
uv pip install ansi2html
uv pip install "$YARF_DIR"
