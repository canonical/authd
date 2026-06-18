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

# Workshop Store SDKs inject Go and Rust via profile.d. Source them explicitly
# so non-login shells (hooks, actions) pick up the toolchain paths. authd-dev.sh
# is excluded: it adds the same ~/go/bin:~/.cargo/bin that the line below does,
# and sourcing both would prepend them twice on the first load.
for profile in /etc/profile.d/go.sh /etc/profile.d/rust.sh; do
    if [ -r "$profile" ]; then
        # shellcheck disable=SC1090
        source "$profile"
    fi
done

export PATH="${HOME}/go/bin:${HOME}/.cargo/bin:${PATH}"
