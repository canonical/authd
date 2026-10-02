#!/bin/bash

# Shared health checks for the authd Workshop environment.
#
# These helpers back the developer-facing `validate` action so the environment
# has a single source of truth for what "healthy" means. (Workshop runs no SDK
# health checks under VM confinement, so there is no check-health hook.)

# go/cargo/protoc and the NSS login-environment check all need the workshop
# user's login shell, so they are run together in a single spawn instead of
# one `sudo -u workshop --login` round-trip each — four separate login shells
# would each re-source /etc/profile.d/* for no benefit.
AUTHD_PROJECT_DIR="${AUTHD_PROJECT_DIR:-/project}"
_authd_login_checks_ran=""
_authd_login_check_go=1
_authd_login_check_cargo=1
_authd_login_check_protoc=1
_authd_login_check_workshop_user=1

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

    local key value
    while IFS='=' read -r key value; do
        case "${key}" in
            go)     [ "${value}" = ok ] && _authd_login_check_go=0 ;;
            cargo)  [ "${value}" = ok ] && _authd_login_check_cargo=0 ;;
            protoc) [ "${value}" = ok ] && _authd_login_check_protoc=0 ;;
            workshop_user) [ "${value}" = ok ] && _authd_login_check_workshop_user=0 ;;
        esac
    done <<< "${output}"
}

authd_check_go() {
    authd_run_login_checks_once
    return "${_authd_login_check_go}"
}

authd_check_cargo() {
    authd_run_login_checks_once
    return "${_authd_login_check_cargo}"
}

authd_check_protoc() {
    authd_run_login_checks_once
    return "${_authd_login_check_protoc}"
}

authd_check_authctl() {
    command -v authctl
}

authd_check_workshop_login_environment() {
    authd_run_login_checks_once
    return "${_authd_login_check_workshop_user}"
}

authd_check_socket() {
    systemctl is-active --quiet authd.socket
}

authd_check_service_activation() {
    # authd.service is socket-activated, so it may legitimately not be running
    # yet; the only failure state that matters here is that it failed to start.
    # `is-failed` also succeeds with a negated result for a missing unit, so
    # check that systemd loaded the service before accepting its state.
    local load_state
    load_state="$(systemctl show -p LoadState --value authd.service 2>/dev/null)" ||
        return 1
    [ "${load_state}" = loaded ] &&
        ! systemctl is-failed --quiet authd.service
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

authd_check_pam_config() {
    # Anchored to exclude commented-out lines (a disabled entry left in place
    # after debugging would otherwise still count as "configured").
    grep -Eq '^[^#]*pam_authd_exec\.so' /etc/pam.d/common-auth &&
    grep -Eq '^[^#]*pam_authd_exec\.so' /etc/pam.d/common-account &&
    grep -Eq '^[^#]*pam_authd_exec\.so' /etc/pam.d/common-password &&
    grep -Eq '^[^#]*pam_mkhomedir\.so' /etc/pam.d/common-session
}

authd_check_nss_module() {
    local multiarch
    multiarch="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
    test -f "/usr/lib/${multiarch}/libnss_authd.so.2"
}

authd_check_nsswitch() {
    grep -Eq '^passwd:.*\bauthd\b' /etc/nsswitch.conf &&
    grep -Eq '^group:.*\bauthd\b' /etc/nsswitch.conf &&
    grep -Eq '^shadow:.*\bauthd\b' /etc/nsswitch.conf
}

authd_check_sshd() {
    # Ubuntu uses socket activation for OpenSSH, so ssh.service may be
    # inactive while ssh.socket is listening and accepting connections.
    systemctl is-active --quiet ssh.socket ||
        systemctl is-active --quiet ssh.service
}

authd_check_gdm_pam() {
    test -f /etc/pam.d/gdm-authd &&
        grep -Eq '^[[:space:]]*auth[[:space:]]+[^#]*[[:space:]]pam_authd\.so([[:space:]]|$)' /etc/pam.d/gdm-authd &&
        grep -Eq '^[[:space:]]*account[[:space:]]+[^#]*[[:space:]]pam_authd\.so([[:space:]]|$)' /etc/pam.d/gdm-authd
}

authd_check_gdm() {
    systemctl is-active --quiet gdm3.service
}

authd_check_gdm_shell() {
    [ "$(dpkg-query -W -f='${Status}' gnome-shell)" = 'install ok installed' ] &&
        dpkg --compare-versions "$(dpkg-query -W -f='${Version}' gnome-shell)" ge \
            '50.1-0ubuntu1.2+authd1~26.04.1'
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
    local variant variants

    variants="$(authd_configured_broker_variants)" || return 1
    [ -n "$variants" ] || return 0

    while IFS= read -r variant; do
        if systemctl is-active --quiet "authd-${variant}.service"; then
            continue
        fi
        systemctl is-enabled --quiet "authd-${variant}.service" 2>/dev/null || continue
        return 1
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
    authd_print_validate_line "authd.service activation" authd_check_service_activation || rc=1
    authd_print_validate_line "pam_authd_exec.so" authd_check_pam_exec || rc=1
    authd_print_validate_line "pam_authd.so" authd_check_pam_native || rc=1
    authd_print_validate_line "PAM stack wired" authd_check_pam_config || rc=1
    authd_print_validate_line "libnss_authd.so.2" authd_check_nss_module || rc=1
    authd_print_validate_line "nsswitch wired" authd_check_nsswitch || rc=1
    authd_print_validate_line "sshd active" authd_check_sshd || rc=1
    authd_print_validate_line "gdm-authd PAM service" authd_check_gdm_pam || rc=1
    authd_print_validate_line "patched gnome-shell" authd_check_gdm_shell || rc=1
    authd_print_validate_line "gdm3 active" authd_check_gdm || rc=1
    authd_print_validate_line "enabled brokers active" authd_check_configured_brokers || rc=1
    local broker_count
    # A failed inspection already reports FAIL above. Only print the
    # no-broker hint when the count itself succeeded.
    if broker_count="$(authd_broker_count)"; then
        if [ "$broker_count" -eq 0 ]; then
            printf '\n'
            printf '%s\n' '  No broker configured — authd cannot authenticate users yet.'
            printf '\n'
            printf '%s\n' '  Install one (replace @example.com with an allowed SSH suffix):'
            printf '%s\n' "    workshop run -- broker google --client-id ID --client-secret-file /project/secret --ssh-suffixes '@example.com'"
            printf '%s\n' "    workshop run -- broker msentraid --issuer URL --client-id ID --register-device --entra-auth --ssh-suffixes '@example.com'"
        else
            printf '  info %s broker(s) configured\n' "$broker_count"
        fi
    fi

    return "$rc"
}
