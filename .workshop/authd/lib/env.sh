#!/bin/bash
# Shared non-interactive environment for authd Workshop hooks and actions.

# Avoid prepending the profile paths again if several helpers source this file.
if [ -n "${AUTHD_ENV_LOADED:-}" ]; then
    # shellcheck disable=SC2317  # || true is reachable when executed directly (not sourced)
    return 0 2>/dev/null || true
fi
AUTHD_ENV_LOADED=1

# The Rust and Go Store SDKs add their toolchains via profile.d. Source
# them explicitly for non-login hooks and actions.
# authd-dev.sh is excluded because it duplicates the per-user paths below.
if [ -r /etc/profile.d/rust.sh ]; then
    # shellcheck disable=SC1091
    source /etc/profile.d/rust.sh
fi
if [ -r /etc/profile.d/go.sh ]; then
    # shellcheck disable=SC1091
    source /etc/profile.d/go.sh
fi

export PATH="${HOME}/go/bin:${HOME}/.cargo/bin:${PATH}"
# Keep the Go build cache across refreshes. Leave the module cache to the Go SDK.
AUTHD_PROJECT_DIR="${AUTHD_PROJECT_DIR:-/project}"
export GOCACHE="${GOCACHE:-${AUTHD_PROJECT_DIR}/target/.authd-go-cache}"

# Ignore stale vendor trees without rewriting go.mod.
export GOFLAGS="-mod=readonly${GOFLAGS:+ ${GOFLAGS}}"
# In linked worktrees, .git can point to a path that only exists on the host.
if ! git -C "$AUTHD_PROJECT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    export GOFLAGS="${GOFLAGS} -buildvcs=false"
fi
