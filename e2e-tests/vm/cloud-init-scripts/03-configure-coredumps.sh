#!/usr/bin/env bash

set -euo pipefail

echo "Running ${0##*/}"

if [[ "${EUID}" -ne 0 ]]; then
    echo "This script must run as root." >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive

package_is_installed() {
    local status

    if ! status="$(dpkg-query -W -f='${db:Status-Status}' "$1" 2>/dev/null)"; then
        return 1
    fi
    [[ "${status}" == "installed" ]]
}

if ! package_is_installed systemd-coredump; then
    apt-get install -y --no-install-recommends systemd-coredump
fi

systemd_config_changed=false
write_config() {
    local path="$1"
    local content="$2"

    mkdir -p "$(dirname "${path}")"
    if [[ -f "${path}" ]] && cmp -s <(printf '%s\n' "${content}") "${path}"; then
        return
    fi

    printf '%s\n' "${content}" > "${path}"
    systemd_config_changed=true
}

write_config /etc/systemd/coredump.conf.d/90-e2e-tests.conf '[Coredump]
Storage=external
Compress=yes
ProcessSizeMax=1G
ExternalSizeMax=1G
MaxUse=1G
KeepFree=1G'

write_config /etc/systemd/system.conf.d/90-e2e-tests.conf '[Manager]
DefaultLimitCORE=infinity'

write_config /etc/systemd/user.conf.d/90-e2e-tests.conf '[Manager]
DefaultLimitCORE=infinity'

write_config /etc/sysctl.d/99-e2e-tests-coredump.conf \
    'kernel.core_pattern=|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h %d'

if [[ "${systemd_config_changed}" == true ]]; then
    systemctl daemon-reexec
fi
sysctl --system
