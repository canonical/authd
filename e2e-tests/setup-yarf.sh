#!/usr/bin/env bash
set -euo pipefail
set -x

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
YARF_DIR="${SCRIPT_DIR}/.yarf"
# shellcheck source=yarf-env.sh
source "${SCRIPT_DIR}/yarf-env.sh"

# Ensure that the YARF submodule is initialized
git -C "${SCRIPT_DIR}/.." submodule update --init --depth=1 e2e-tests/.yarf

# Install uv snap if not already installed
if ! command -v uv &> /dev/null; then
    echo "Installing uv snap..."
    sudo snap install --classic astral-uv
else
    echo "uv snap already installed"
fi

# Clear the marker before changing the environment so a failed sync is retried.
REVISION_FILE="${YARF_DIR}/.venv/.authd-yarf-revision"
rm -f "${REVISION_FILE}"
REVISION=$(yarf_environment_revision)

UV_PROJECT_ENVIRONMENT="${YARF_DIR}/.venv" \
    uv sync --project "${SCRIPT_DIR}" --locked --no-editable --no-default-groups \
        --reinstall-package yarf --python "$(cat "${YARF_DIR}/.python-version")"
printf '%s\n' "${REVISION}" > "${REVISION_FILE}"
