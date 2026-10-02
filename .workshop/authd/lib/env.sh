#!/bin/bash
# Shared non-interactive environment for authd Workshop hooks and actions.

# Guard against being sourced multiple times (e.g. setup-project sources this
# directly, then authd_prepare_build_environment sources it again via
# build-component). Without the guard the profile.d scripts keep prepending to
# PATH and it grows to thousands of characters.
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
# Keep the Go build cache in the ignored project mount so it survives a
# Workshop refresh. This trades faster guest-disk I/O for warm rebuilds
# after refresh. The module cache is left to the Go SDK, which persists
# it on the host; the project mount below is only a fallback for
# environments without the SDK.
AUTHD_PROJECT_DIR="${AUTHD_PROJECT_DIR:-/project}"
export GOCACHE="${GOCACHE:-${AUTHD_PROJECT_DIR}/target/.authd-go-cache}"
export GOMODCACHE="${GOMODCACHE:-${AUTHD_PROJECT_DIR}/target/.authd-go-mod-cache}"

# vendor/ can drift out of sync with go.mod (e.g. after a dependency bump or
# switching branches), which makes any plain `go build`/`test`/`generate` in
# the module fail with "inconsistent vendoring" instead of just building.
# Fall back to the module cache instead of requiring vendor/ to be exact, the
# same workaround already used for the authd-oidc-brokers build.
# Append rather than overwrite so caller- or SDK-provided flags survive.
export GOFLAGS="${GOFLAGS:+"${GOFLAGS} "}-mod=mod"
