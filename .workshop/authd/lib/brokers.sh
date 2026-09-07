#!/bin/bash
# Single source of truth for supported OIDC broker variants, shared by
# build-component, install-broker, save-state and restore-state so adding or
# renaming a variant only requires editing this file. Keep in sync with
# authd-oidc-brokers/conf/variants/.

# shellcheck disable=SC2034  # consumed by scripts that source this file
AUTHD_BROKER_VARIANTS=(google msentraid oidc)

# Prints the go build tag for a broker variant on stdout (empty for oidc,
# which has none). Returns non-zero for an unknown variant.
authd_broker_build_tag() {
    case "$1" in
        google)    printf '%s' "-tags=withgoogle" ;;
        msentraid) printf '%s' "-tags=withmsentraid" ;;
        oidc)      printf '%s' "" ;;
        *) return 1 ;;
    esac
}
