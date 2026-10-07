#!/bin/bash

# Shared health checks for the authd Workshop environment.
#
# These helpers back the developer-facing `validate` action so the environment
# has a single source of truth for what "healthy" means. There is no
# check-health hook; `validate` is the only health entry point.

# go/cargo/protoc and the NSS login-environment check all need the workshop
# user's login shell, so they are run together in a single spawn instead of
# one `sudo -u workshop --login` round-trip each — four separate login shells
# would each re-source /etc/profile.d/* for no benefit.
AUTHD_PROJECT_DIR="${AUTHD_PROJECT_DIR:-/project}"
_authd_login_checks_ran=""
_authd_login_output=""

authd_run_login_checks_once() {
    [ -z "${_authd_login_checks_ran}" ] || return 0
    _authd_login_checks_ran=1

    # getent verifies that the local workshop account resolves in its login
    # shell. Unlike id -un, it avoids an empty command-substitution result.
    # This does not test authd users; the module and nsswitch wiring are checked
    # separately below.
    local script='
        source '"${AUTHD_PROJECT_DIR}"'/.workshop/authd/lib/env.sh
        command -v go >/dev/null 2>&1 && echo go=ok || echo go=fail
        command -v cargo >/dev/null 2>&1 && echo cargo=ok || echo cargo=fail
        command -v protoc >/dev/null 2>&1 && echo protoc=ok || echo protoc=fail
        getent passwd workshop >/dev/null 2>&1 && echo workshop_user=ok || echo workshop_user=fail
    '
    local output
    if [ "$(id -un)" = workshop ]; then
        output="$(bash -c "${script}")"
    else
        # -n: fail fast on a password prompt instead of blocking (setup-base
        # provisions passwordless sudo).
        output="$(sudo -n -u workshop --login bash -c "${script}")"
    fi

    _authd_login_output="${output}"
}

authd_check_go() {
    authd_run_login_checks_once
    grep -q '^go=ok$' <<< "${_authd_login_output}"
}

authd_check_cargo() {
    authd_run_login_checks_once
    grep -q '^cargo=ok$' <<< "${_authd_login_output}"
}

authd_check_protoc() {
    authd_run_login_checks_once
    grep -q '^protoc=ok$' <<< "${_authd_login_output}"
}

authd_check_authctl() {
    command -v authctl
}

authd_check_workshop_login_environment() {
    authd_run_login_checks_once
    grep -q '^workshop_user=ok$' <<< "${_authd_login_output}"
}

authd_check_socket() {
    systemctl is-active --quiet authd.socket
}

authd_check_grpc_health() {
    # The health RPC activates authd without requiring a remote broker or a login.
    /usr/libexec/authd-workshop-health
}

authd_check_pam_exec() {
    local multiarch
    multiarch="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
    test -s "/usr/lib/${multiarch}/security/pam_authd_exec.so"
}

authd_check_pam_native() {
    local multiarch
    multiarch="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
    test -s "/usr/lib/${multiarch}/security/pam_authd.so"
}

authd_pam_has_module() {
    awk -v type="$2" -v module="$3" '
        { sub(/#.*/, "") }
        $1 == type || $1 == "-" type {
            sub(/^[[:space:]]*[^[:space:]]+[[:space:]]+/, "")
            if (!sub(/^(\[[^]]+\]|required|requisite|sufficient|optional)[[:space:]]+/, "")) next
            if ($1 ~ /^\//) sub(/^.*\//, "", $1)
            if ($1 == module) found = 1
        }
        END { exit !found }
    ' "$1"
}

authd_check_pam_config() {
    authd_pam_has_module /etc/pam.d/common-auth auth pam_authd_exec.so &&
        authd_pam_has_module /etc/pam.d/common-account account pam_authd_exec.so &&
        authd_pam_has_module /etc/pam.d/common-password password pam_authd_exec.so &&
        authd_pam_has_module /etc/pam.d/common-session session pam_mkhomedir.so
}

authd_check_nss_module() {
    local multiarch
    multiarch="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
    test -f "/usr/lib/${multiarch}/libnss_authd.so.2"
}

authd_check_nsswitch() {
    awk '
        { sub(/#.*/, "") }
        /^[[:space:]]*(passwd|group|shadow)[[:space:]]*:/ {
            match($0, /passwd|group|shadow/)
            db = substr($0, RSTART, RLENGTH)
            sub(/^[^:]*:/, "")
            gsub(/\[[^]]*\]/, " ")
            for (i = 1; i <= NF; i++)
                if ($i == "authd") wired[db] = 1
        }
        END { exit !(wired["passwd"] && wired["group"] && wired["shadow"]) }
    ' /etc/nsswitch.conf
}

authd_check_sshd() {
    # Ubuntu uses socket activation for OpenSSH, so ssh.service may be
    # inactive while ssh.socket is listening and accepting connections.
    if ! systemctl is-active --quiet ssh.socket && ! systemctl is-active --quiet ssh.service; then
        return 1
    fi

    # Extended test mode validates the config and keys and prints effective options.
    local config
    config="$(sudo -n /usr/sbin/sshd -T)" || return 1
    grep -Fxq 'usepam yes' <<< "$config" &&
        grep -Fxq 'kbdinteractiveauthentication yes' <<< "$config"
}

authd_check_gdm_pam() {
    authd_pam_has_module /etc/pam.d/gdm-authd auth pam_authd.so &&
        authd_pam_has_module /etc/pam.d/gdm-authd account pam_authd.so
}

authd_check_gdm() {
    systemctl is-active --quiet gdm3.service
}

authd_check_gdm_shell() {
    [ "$(dpkg-query -W -f='${Status}' gnome-shell)" = 'install ok installed' ] &&
        dpkg --compare-versions "$(dpkg-query -W -f='${Version}' gnome-shell)" ge \
            '50.1-0ubuntu1.3'
}

authd_configured_broker_variants() {
    # /etc/authd/brokers.d is 700 root:root. Check sudo separately so a
    # missing passwordless rule cannot masquerade as an empty broker list.
    sudo -n true || {
        echo 'Cannot inspect brokers: passwordless sudo is unavailable.' >&2
        return 1
    }
    sudo -n test -d /etc/authd/brokers.d || return 0

    local configs
    configs="$(sudo -n find /etc/authd/brokers.d \
        -maxdepth 1 \
        -type f \
        -name '*.conf' \
        -printf '%f\n')" || return 1
    [ -n "$configs" ] || return 0
    printf '%s\n' "$configs" | sed 's/\.conf$//' | sort
}

authd_check_configured_brokers() {
    local variant variants unit load_state

    variants="$(authd_configured_broker_variants)" || return 1
    [ -n "$variants" ] || return 0

    while IFS= read -r variant; do
        unit="authd-${variant}.service"
        load_state="$(systemctl show -p LoadState --value -- "$unit")" || return 1
        [ "$load_state" = loaded ] || return 1
        if systemctl is-active --quiet -- "$unit"; then
            continue
        fi
        [ "$(systemctl show -p UnitFileState --value -- "$unit")" = disabled ] || return 1
    done <<< "$variants"
}

authd_broker_count() {
    local variants
    variants="$(authd_configured_broker_variants)" || return 1
    if [ -z "$variants" ]; then
        echo 0
        return 0
    fi
    printf '%s\n' "$variants" | wc -l
}

authd_print_ok() {
    printf '  \033[0;32mok\033[0m   %s\n' "$1"
}

authd_print_bad() {
    printf '  \033[0;31mFAIL\033[0m %s\n' "$1"
}

authd_print_validate_line() {
    local label="$1" checker="$2"

    if "$checker" >/dev/null 2>&1; then
        authd_print_ok "$label"
        return 0
    fi

    authd_print_bad "$label"
    return 1
}

authd_print_validate_report() {
    local rc=0

    authd_print_validate_line "go toolchain" authd_check_go || rc=1
    authd_print_validate_line "rust toolchain" authd_check_cargo || rc=1
    authd_print_validate_line "protoc" authd_check_protoc || rc=1
    authd_print_validate_line "authctl" authd_check_authctl || rc=1
    authd_print_validate_line "workshop login environment" authd_check_workshop_login_environment || rc=1
    authd_print_validate_line "authd.socket active" authd_check_socket || rc=1
    authd_print_validate_line "authd gRPC health" authd_check_grpc_health || rc=1
    authd_print_validate_line "pam_authd_exec.so" authd_check_pam_exec || rc=1
    authd_print_validate_line "pam_authd.so" authd_check_pam_native || rc=1
    authd_print_validate_line "PAM stack wired" authd_check_pam_config || rc=1
    authd_print_validate_line "libnss_authd.so.2" authd_check_nss_module || rc=1
    authd_print_validate_line "nsswitch wired" authd_check_nsswitch || rc=1
    authd_print_validate_line "sshd active and configured for PAM" authd_check_sshd || rc=1
    authd_print_validate_line "gdm-authd PAM service" authd_check_gdm_pam || rc=1
    authd_print_validate_line "gnome-shell with authd greeter support" authd_check_gdm_shell || rc=1
    authd_print_validate_line "gdm3 active" authd_check_gdm || rc=1
    authd_print_validate_line "registered broker units present; enabled brokers active" authd_check_configured_brokers || rc=1
    local broker_count
    # A failed inspection already reports FAIL above. Only print the
    # no-broker hint when the count itself succeeded.
    if broker_count="$(authd_broker_count)"; then
        if [ "$broker_count" -eq 0 ]; then
            printf '\n'
            printf '%s\n' '  No broker configured — authd cannot authenticate users yet.'
            printf '\n'
            printf '%s\n' '  Install one (replace @example.com with an allowed SSH suffix):'
            printf '%s\n' "    workshop run -- broker configure google --client-id ID --client-secret-file /home/workshop/.authd-client-secret --ssh-suffixes '@example.com'"
            printf '%s\n' "    workshop run -- broker configure msentraid --issuer URL --client-id ID --register-device --entra-auth --ssh-suffixes '@example.com'"
        else
            printf '  info %s broker(s) configured\n' "$broker_count"
        fi
    fi

    return "$rc"
}
