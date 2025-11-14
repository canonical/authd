#!/usr/bin/env bash
set -euo pipefail
set -x

YARF_REPO_URL="https://github.com/adombeck/yarf"

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
YARF_DIR="${SCRIPT_DIR}/.yarf"

# Clone the YARF repository if it doesn't exist
if [ ! -d "$YARF_DIR" ]; then
    echo "Cloning YARF repository into $YARF_DIR..."
    git clone --depth=1 "$YARF_REPO_URL" "$YARF_DIR"
else
    echo "YARF repository already exists at $YARF_DIR, pulling latest changes..."
    cd "$YARF_DIR"
    git pull
fi

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
