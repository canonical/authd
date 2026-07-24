#!/bin/bash

# Shared health checks for the authd Workshop environment.
#
# These helpers are used by both the Workshop `check-health` hook and the
# developer-facing `validate` action so the environment has a single source of
# truth for what "healthy" means.

authd_run_workshop_login_shell() {
    local script="$1"

    if [ "$(id -un)" = workshop ]; then
        bash -c "source /project/.workshop/authd/lib/env.sh; ${script}"
        return
    fi

    sudo -u workshop --login bash -c "source /project/.workshop/authd/lib/env.sh; ${script}"
}

authd_check_go() {
    authd_run_workshop_login_shell 'command -v go'
}

authd_check_cargo() {
    authd_run_workshop_login_shell 'command -v cargo'
}

authd_check_protoc() {
    authd_run_workshop_login_shell 'command -v protoc'
}

authd_check_authctl() {
    command -v authctl
}

authd_check_workshop_login_environment() {
    # Verify the workshop user's login shell can resolve accounts through NSS.
    # Use getent rather than id -un: command substitution inside
    # `sudo -u workshop --login bash -lc` loses its controlling tty in the
    # LXD container context, causing `id -un` to return empty even though
    # the underlying lookup works.  getent queries NSS directly and is not
    # affected by the missing tty.
    authd_run_workshop_login_shell 'getent passwd workshop >/dev/null'
}

authd_check_socket() {
    systemctl is-active --quiet authd.socket
}

authd_check_service_activation() {
    # authd.service is socket-activated, so it may legitimately not be running
    # yet; the only failure state that matters here is that it failed to start.
    ! systemctl is-failed --quiet authd.service
}

authd_check_pam_exec() {
    local multiarch
    multiarch="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
    test -f "/usr/lib/${multiarch}/security/pam_authd_exec.so"
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
    systemctl is-active --quiet ssh
}

authd_configured_broker_variants() {
    # /etc/authd/brokers.d is 700 root:root, so an unprivileged existence
    # check here would silently and incorrectly report zero brokers; use
    # sudo for the check too.
    sudo test -d /etc/authd/brokers.d || return 0

    sudo find /etc/authd/brokers.d \
        -maxdepth 1 \
        -type f \
        -name '*.conf' \
        -printf '%f\n' 2>/dev/null |
        sed 's/\.conf$//' |
        sort
}

authd_check_configured_brokers() {
    local variant

    while IFS= read -r variant; do
        [ -n "$variant" ] || continue
        if systemctl is-active --quiet "authd-${variant}.service"; then
            continue
        fi
        systemctl is-enabled --quiet "authd-${variant}.service" 2>/dev/null || continue
        return 1
    done < <(authd_configured_broker_variants)
}

authd_broker_count() {
    authd_configured_broker_variants | wc -l
}

authd_each_health_check() {
    local callback="$1"
    local rc=0

    "$callback" "go toolchain" "go missing" authd_check_go || rc=1
    "$callback" "rust toolchain" "cargo missing" authd_check_cargo || rc=1
    "$callback" "protoc" "protoc missing" authd_check_protoc || rc=1
    "$callback" "authctl" "authctl missing" authd_check_authctl || rc=1
    "$callback" "workshop login environment" "workshop login shell cannot resolve its own user" authd_check_workshop_login_environment || rc=1
    "$callback" "authd.socket active" "authd.socket inactive" authd_check_socket || rc=1
    "$callback" "authd.service activation" "authd.service failed to start (see: journalctl -u authd.service)" authd_check_service_activation || rc=1
    "$callback" "pam_authd_exec.so" "pam_authd_exec.so not installed" authd_check_pam_exec || rc=1
    "$callback" "PAM stack wired" "PAM stack not wired for authd" authd_check_pam_config || rc=1
    "$callback" "libnss_authd.so.2" "libnss_authd.so.2 not installed" authd_check_nss_module || rc=1
    "$callback" "nsswitch wired" "nsswitch.conf not wired" authd_check_nsswitch || rc=1
    "$callback" "sshd active" "sshd inactive" authd_check_sshd || rc=1
    "$callback" "enabled brokers active" "one or more enabled brokers are inactive" authd_check_configured_brokers || rc=1

    return "$rc"
}

AUTHD_HEALTH_PROBLEMS=()

authd_record_failed_check() {
    local _label="$1"
    local problem="$2"
    local checker="$3"

    if ! "$checker" >/dev/null 2>&1; then
        AUTHD_HEALTH_PROBLEMS+=("$problem")
    fi
}

authd_collect_health_problems() {
    AUTHD_HEALTH_PROBLEMS=()
    authd_each_health_check authd_record_failed_check >/dev/null
}

authd_print_ok() {
    printf '  \033[0;32mok\033[0m   %s\n' "$1"
}

authd_print_bad() {
    printf '  \033[0;31mFAIL\033[0m %s\n' "$1"
}

authd_print_validate_line() {
    local label="$1"
    local _problem="$2"
    local checker="$3"

    if "$checker" >/dev/null 2>&1; then
        authd_print_ok "$label"
        return 0
    fi

    authd_print_bad "$label"
    return 1
}

authd_print_validate_report() {
    local rc=0

    authd_each_health_check authd_print_validate_line || rc=1
    local broker_count
    broker_count="$(authd_broker_count)"
    if [ "$broker_count" -eq 0 ]; then
        printf '\n'
        printf '  \033[1;33m┌─────────────────────────────────────────────────────────────────┐\033[0m\n'
        printf '  \033[1;33m│  No broker configured — authd cannot authenticate users yet.    │\033[0m\n'
        printf '  \033[1;33m│                                                                 │\033[0m\n'
        printf '  \033[1;33m│  Install one:                                                   │\033[0m\n'
        printf '  \033[1;33m│    workshop run -- broker google    --client-id ID              │\033[0m\n'
        printf '  \033[1;33m│                         --client-secret-file PATH               │\033[0m\n'
        printf '  \033[1;33m│    workshop run -- broker msentraid --issuer URL --client-id ID │\033[0m\n'
        printf '  \033[1;33m└─────────────────────────────────────────────────────────────────┘\033[0m\n'
    else
        printf '  info %s broker(s) configured\n' "$broker_count"
    fi

    return "$rc"
}
