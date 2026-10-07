#!/bin/bash
# Single source of truth for supported OIDC broker variants, shared by
# build-component, broker, save-state and restore-state so adding or
# renaming a variant only requires editing this file. Keep in sync with
# brokers/conf/variants/.

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

# Atomic single-file installation as root, including when a previous file exists.
authd_install_broker_file() (
    local source="$1" target="$2" mode="$3" stage
    mkdir -p -- "${target%/*}" || return
    stage="$(mktemp -- "${target%/*}/.${target##*/}.XXXXXX")" || return
    trap 'rm -f -- "$stage"' EXIT
    install -m "$mode" -- "$source" "$stage" || return
    mv -fT -- "$stage" "$target"
)

# Preserve files referenced by custom broker units as well as broker.conf.
authd_stage_broker_config() {
    local source="$1" target="$2"
    install -d -m700 -- "$target"
    cp -a -- "${source}/." "$target/"
    chmod 700 -- "$target"
    find "$target" -maxdepth 1 -name 'broker.conf.bak-*' -type f -delete
}

authd_broker_daemon_path_valid() {
    # Paths are interpolated into a quoted systemd ExecStart operand.
    [[ "$1" = /* && "$1" != *[$'\n\r\t"\\%$']* ]] || {
        echo 'Broker daemon path must be absolute and contain no systemd metacharacters.' >&2
        return 2
    }
}

# Publish the unit before recording the path used by rebuild and restore.
# If either step fails, reinstate the old unit (or remove a new one).
authd_update_broker_unit() (
    local unit="$1" path_file="$2" daemons_path="$3" stage
    stage="$(mktemp -d -- "${unit%/*}/.authd-unit.XXXXXX")"
    trap 'rm -rf -- "$stage"' EXIT
    cat > "${stage}/unit"
    printf '%s\n' "$daemons_path" > "${stage}/daemon_path"
    if [ -e "$unit" ] || [ -L "$unit" ]; then
        cp -a -- "$unit" "${stage}/previous"
    fi
    authd_install_broker_file "${stage}/unit" "$unit" 644
    if systemctl daemon-reload && authd_install_broker_file "${stage}/daemon_path" "$path_file" 600; then
        return 0
    fi
    if [ -e "${stage}/previous" ] || [ -L "${stage}/previous" ]; then
        if ! mv -fT -- "${stage}/previous" "$unit"; then
            trap - EXIT
            echo "Could not restore the old unit; it is saved at ${stage}/previous" >&2
            return 1
        fi
    else
        rm -- "$unit"
    fi
    systemctl daemon-reload
    return 1
)
