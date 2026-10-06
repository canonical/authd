#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 3 ]]; then
    echo "Usage: $0 <release> <output-directory> <label>" >&2
    exit 2
fi

RELEASE="$1"
OUTPUT_DIR="$2"
LABEL="$3"
if [[ ! "${LABEL}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
    echo "Invalid coredump collection label: ${LABEL}" >&2
    exit 2
fi

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
SSH="${SCRIPT_DIR}/ssh.sh"
VM_NAME="${VM_NAME:-e2e-runner-${RELEASE}}"
REPORT="${OUTPUT_DIR}/vm-coredumps.txt"
ARCHIVE_DIR="${OUTPUT_DIR}/vm-coredumps"
ARCHIVE="${ARCHIVE_DIR}/${LABEL}.tar"

mkdir -p "${ARCHIVE_DIR}"
metadata_file="$(mktemp "${OUTPUT_DIR}/.vm-coredumps-metadata.XXXXXX")"
core_file_list="$(mktemp "${OUTPUT_DIR}/.vm-coredumps-list.XXXXXX")"
archive_tmp="${ARCHIVE}.tmp.${BASHPID}"
cleanup() {
    rm -f "${metadata_file}" "${core_file_list}" "${archive_tmp}"
}
trap cleanup EXIT

if ! vm_state="$(virsh domstate "${VM_NAME}" 2>&1)"; then
    printf '\nCoredump collection: %s\n' "${LABEL}" >> "${REPORT}"
    printf 'Could not query VM state: %s\n' "${vm_state}" >> "${REPORT}"
    echo "Warning: could not query ${VM_NAME} state for coredump collection." >&2
    exit 1
fi

if [[ "${vm_state}" != "running" ]]; then
    printf '\nCoredump collection: %s\n' "${LABEL}" >> "${REPORT}"
    printf 'The VM is %s; coredump files were not collected.\n' "${vm_state}" \
        >> "${REPORT}"
    exit 0
fi

if ! timeout 15 env VM_NAME="${VM_NAME}" "${SSH}" \
    --release "${RELEASE}" -- bash -euo pipefail -s \
    > "${core_file_list}" 2>&1 <<'EOF'
if [ -d /var/lib/systemd/coredump ]; then
    find /var/lib/systemd/coredump -maxdepth 1 -type f -printf '%f\n'
fi
EOF
then
    {
        printf '\nCoredump collection: %s\n' "${LABEL}"
        printf 'Failed to check for stored core files:\n'
        cat "${core_file_list}"
    } >> "${REPORT}"
    echo "Warning: could not check for core files from ${VM_NAME}." >&2
    exit 1
fi

if [[ ! -s "${core_file_list}" && "${LABEL}" != "final" ]]; then
    exit 0
fi

printf '\nCoredump collection: %s\n' "${LABEL}" >> "${REPORT}"

if ! timeout 30 env VM_NAME="${VM_NAME}" "${SSH}" \
    --release "${RELEASE}" -- bash -euo pipefail -s \
    > "${metadata_file}" 2>&1 <<'EOF'
if ! coredumpctl --no-pager info; then
    printf 'coredumpctl info failed.\n' >&2
fi
if [ -d /var/lib/systemd/coredump ]; then
    printf '\nStored systemd core files:\n'
    find /var/lib/systemd/coredump -maxdepth 1 -type f \
        -printf '%f (%s bytes)\n' | sort
fi
EOF
then
    {
        printf 'Failed to retrieve coredump information from the VM:\n'
        cat "${metadata_file}"
    } >> "${REPORT}"
    echo "Warning: could not retrieve coredump information from ${VM_NAME}." >&2
    exit 1
fi

cat "${metadata_file}" >> "${REPORT}"

if ! timeout 120 env VM_NAME="${VM_NAME}" "${SSH}" \
    --release "${RELEASE}" -- \
    'mkdir -p /var/lib/systemd/coredump && tar --directory=/var/lib/systemd/coredump --create --file=- .' \
    > "${archive_tmp}"; then
    printf 'Failed to retrieve stored core files from the VM.\n' >> "${REPORT}"
    echo "Warning: could not retrieve core files from ${VM_NAME}." >&2
    exit 1
fi

if ! archive_listing="$(tar --list --file="${archive_tmp}")"; then
    printf 'Could not read the collected core-file archive.\n' >> "${REPORT}"
    echo "Warning: could not inspect core files from ${VM_NAME}." >&2
    exit 1
fi

core_dump_files="$(printf '%s\n' "${archive_listing}" |
    awk '$0 != "." && $0 != "./"')"
if [[ -z "${core_dump_files}" ]]; then
    printf 'No stored systemd core files were collected.\n' >> "${REPORT}"
    echo "No stored systemd core files found for ${LABEL}."
    exit 0
fi

mv -f -- "${archive_tmp}" "${ARCHIVE}"
core_dump_count="$(printf '%s\n' "${core_dump_files}" |
    awk 'NF { count++ } END { print count + 0 }')"
{
    printf 'Collected %s systemd core file(s) in vm-coredumps/%s.tar:\n' \
        "${core_dump_count}" "${LABEL}"
    printf '%s\n' "${core_dump_files}"
} >> "${REPORT}"
echo "Collected ${core_dump_count} systemd core file(s) for ${LABEL}."
