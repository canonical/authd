#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

if command -v wrangler >/dev/null 2>&1; then
  wrangler deploy "$@"
else
  npx --yes wrangler deploy "$@"
fi
