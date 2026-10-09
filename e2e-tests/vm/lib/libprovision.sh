#!/bin/bash

# shellcheck disable=SC2034 # Used by scripts that source this library.
AUTHD_DEFAULT_APT_SOURCE="ppa:ubuntu-enterprise-desktop/authd-edge"

function normalize_apt_source() {
    local source="$1"

    case "${source}" in
        authd|stable|ubuntu-enterprise-desktop/authd|ppa:ubuntu-enterprise-desktop/authd)
            echo "ppa:ubuntu-enterprise-desktop/authd"
            ;;
        authd-edge|edge|ubuntu-enterprise-desktop/authd-edge|ppa:ubuntu-enterprise-desktop/authd-edge)
            echo "ppa:ubuntu-enterprise-desktop/authd-edge"
            ;;
        authd-dev|dev|ubuntu-enterprise-desktop/authd-dev|ppa:ubuntu-enterprise-desktop/authd-dev)
            echo "ppa:ubuntu-enterprise-desktop/authd-dev"
            ;;
        ppa:*)
            echo "Invalid APT source '${source}'." >&2
            return 1
            ;;
        "")
            echo "APT source must not be empty." >&2
            return 1
            ;;
        *)
            if [[ "${source}" =~ ^[a-z0-9][a-z0-9+.-]*$ ]]; then
                echo "${source}"
            else
                echo "Invalid APT source '${source}'." >&2
                return 1
            fi
            ;;
    esac
}

function normalize_apt_sources() {
    local input source normalized existing
    local -a fields=()
    local -a normalized_sources=()
    local duplicate
    local has_input=false

    for input in "$@"; do
        if [[ -n "${input}" ]]; then
            has_input=true
        fi
        input="${input//,/ }"
        read -r -a fields <<<"${input}"
        for source in "${fields[@]}"; do
            if ! normalized="$(normalize_apt_source "${source}")"; then
                return 1
            fi

            duplicate=false
            for existing in "${normalized_sources[@]}"; do
                if [[ "${existing}" == "${normalized}" ]]; then
                    duplicate=true
                    break
                fi
            done
            if [[ "${duplicate}" == false ]]; then
                normalized_sources+=("${normalized}")
            fi
        done
    done

    if ((${#normalized_sources[@]})); then
        printf '%s\n' "${normalized_sources[@]}"
    elif [[ "${has_input}" == true ]]; then
        echo "APT source list must not be empty." >&2
        return 1
    fi
}

function normalize_apt_sources_into_array() {
    local output_name="$1"
    shift

    local normalized_sources
    local -n output_ref="${output_name}"

    normalized_sources="$(normalize_apt_sources "$@")" || return 1
    output_ref=()
    if [[ -n "${normalized_sources}" ]]; then
        mapfile -t output_ref <<<"${normalized_sources}"
    fi
}

function join_apt_sources() {
    local IFS=,
    printf '%s' "$*"
}

function is_ppa_source() {
    [[ "$1" == ppa:* ]]
}

function is_default_archive_source() {
    local source="$1"
    local release="$2"

    [[ "${source}" == "${release}" ||
       "${source}" == "${release}-updates" ||
       "${source}" == "${release}-security" ||
       "${source}" == "${release}-backports" ]]
}

function validate_apt_source_for_release() {
    local source="$1"
    local release="$2"
    local source_name="${3:-APT}"

    if [[ -z "${source}" ]] ||
       is_ppa_source "${source}" ||
       [[ "${source}" == "${release}" || "${source}" == "${release}-"* ]]; then
        return
    fi

    echo "${source_name} APT source '${source}' does not match VM release '${release}'." >&2
    return 1
}

function ppa_has_suite() {
    # Return 0 if published, 1 if missing, and 2 if the check failed.
    local ppa="$1"
    local suite="$2"
    local metadata
    local status
    local url="https://ppa.launchpadcontent.net/${ppa}/ubuntu/dists/${suite}"

    for metadata in InRelease Release; do
        if ! status="$(
            curl \
                --silent \
                --show-error \
                --head \
                --location \
                --output /dev/null \
                --write-out '%{http_code}' \
                --connect-timeout 10 \
                --max-time 30 \
                "${url}/${metadata}"
        )"; then
            echo "Failed to check PPA '${ppa}' for suite '${suite}'." >&2
            return 2
        fi

        case "${status}" in
            200)
                return 0
                ;;
            404)
                ;;
            *)
                echo "Unexpected HTTP status '${status}' checking PPA '${ppa}' for suite '${suite}'." >&2
                return 2
                ;;
        esac
    done

    return 1
}

function source_pin() {
    local source="$1"
    local ppa

    if is_ppa_source "${source}"; then
        ppa="${source#ppa:}"
        ppa="${ppa//\//-}"
        echo "o=LP-PPA-${ppa}"
    else
        echo "a=${source}"
    fi
}

function source_policy_reference() {
    local source="$1"

    if is_ppa_source "${source}"; then
        echo "/${source#ppa:}/ubuntu"
    else
        echo "${source}/"
    fi
}

function assert_env_vars() {
    local template="e2e-tests/vm/config.env.template"
    if [[ "${1:-}" == "--template" ]]; then
        template="$2"
        shift 2
    fi

    local missing=()
    if [ "$#" -eq 0 ]; then
        return
    fi

    for var in "$@"; do
        # treat unset or empty as missing
        if [ -z "${!var:-}" ]; then
            missing+=("$var")
        fi
    done

    if [ "${#missing[@]}" -ne 0 ]; then
        printf 'Missing required env vars: %s\n' "${missing[*]}" >&2
        printf 'Create a config file from the template at %s\n' "${template}" >&2
        printf 'or set the missing variables in the environment.\n' >&2
        exit 1
    fi
}

function resolve_devel_release() {
    local release="$1"

    # If the release is set to "devel", we need to get the actual codename of the devel release.
    if [ "${release}" != "devel" ]; then
        echo "${release}"
        return
    fi

    # Temporarily disable pipefail because wget exits with a "broken pipe"
    # error (exit code 3) when awk exits early after finding the first match.
    set +o pipefail
    codename=$(wget -qO- http://archive.ubuntu.com/ubuntu/dists/devel/Release | awk -F': ' '$1 == "Codename" { print $2; exit }')
    set -o pipefail
    if [ -z "${codename}" ]; then
        echo >&2 "Error: Failed to resolve devel release codename"
        exit 1
    fi

    echo "${codename}"
}

function has_snapshot() {
    local snapshot_name="$1"
    virsh snapshot-list "${VM_NAME}" | grep -q "${snapshot_name}"
}

function print_vm_console_log() {
    local message="$1"

    echo "${message}" >&2
    if [[ -n "${VM_CONSOLE_LOG:-}" && -s "${VM_CONSOLE_LOG}" ]]; then
        echo "Last 200 serial-console lines:" >&2
        tail -n 200 "${VM_CONSOLE_LOG}" >&2
        echo "Full serial-console log: ${VM_CONSOLE_LOG}" >&2
    else
        echo "No serial-console output was captured." >&2
    fi
}

function force_create_snapshot() {
    local snapshot_name="$1"
    local vm_state
    if has_snapshot "${snapshot_name}"; then
        time virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "${snapshot_name}"
    fi

    if ! vm_state="$(virsh domstate "${VM_NAME}")"; then
        print_vm_console_log "Cannot create snapshot '${snapshot_name}': failed to read the state of VM '${VM_NAME}'."
        return 1
    fi
    if [[ "${vm_state}" != running* ]]; then
        print_vm_console_log "Cannot create snapshot '${snapshot_name}': expected VM '${VM_NAME}' to be running, but its state is '${vm_state}'."
        return 1
    fi

    # Libvirt's default disk filename is derived from the snapshot name.
    # A failed or metadata-only-deleted snapshot can leave that file behind.
    local snapshot_id
    snapshot_id="$(date +%s%N)"
    local diskfile="${IMAGE%.qcow2}.${snapshot_name}.${snapshot_id}"
    local memfile="${IMAGE%.qcow2}-${snapshot_name}.${snapshot_id}.mem"
    if time virsh snapshot-create-as \
        --domain "${VM_NAME}" \
        --name "${snapshot_name}" \
        --diskspec "vda,file=${diskfile},snapshot=external" \
        --memspec "${memfile},snapshot=external"; then
        return 0
    else
        local snapshot_status=$?
        print_vm_console_log "Failed to create live snapshot '${snapshot_name}' (virsh exit status ${snapshot_status})."
        return "${snapshot_status}"
    fi
}

function restore_snapshot_and_sync_time() {
    local snapshot_name="$1"
    virsh snapshot-revert "${VM_NAME}" --snapshotname "${snapshot_name}"
    sync_time
}

function sync_time() {
    local cmd="nm-online -q && \
systemctl restart systemd-timesyncd.service && \
timedatectl show -p NTPSynchronized --value | grep -q yes"
    retry --times 10 --delay 3 -- "$SSH" -- "$cmd"
}

function wait_for_system_running() {
    # Wait until we can connect via SSH
    retry --times 30 --delay 3 -- "$SSH" -- true || return $?
    # shellcheck disable=SC2016
    local cmd='output=$(systemctl is-system-running --wait) || [ $output = degraded ]'
    retry --times 3 --delay 3 -- timeout 30 "$SSH" -- "$cmd"
}

function reboot_system() {
    shutdown_system
    boot_system
}

function shutdown_system() {
    # For some reason, `virsh shutdown` sometimes doesn't cause the VM
    # to shut down, so we retry it a few times.
    # `virsh await` is not available in all libvirt client versions.
    local cmd="if virsh domstate \"${VM_NAME}\" | grep -q '^shut off'; then
    exit 0
fi
virsh shutdown \"${VM_NAME}\" && \
timeout 5 retry --delay 1 -- sh -c \
\"virsh domstate \\\"${VM_NAME}\\\" | grep -q '^shut off'\""
    retry --times 3 --delay 1 -- sh -c "$cmd"
}

function boot_system() {
    virsh start "${VM_NAME}"

    # Retain serial output so a later snapshot failure can show the guest's startup log.
    local diagnostics_dir="${E2E_VM_DIAGNOSTICS_DIR:-${ARTIFACTS_DIR:-${TMPDIR:-/tmp}}/diagnostics}"
    mkdir -p "${diagnostics_dir}"
    VM_CONSOLE_LOG="${diagnostics_dir}/${VM_NAME}-serial-console.log"
    : > "${VM_CONSOLE_LOG}"
    # shellcheck disable=SC2016
    VM_NAME="${VM_NAME}" script -q -e -f "${VM_CONSOLE_LOG}" \
        -c 'virsh console "$VM_NAME"' >/dev/null 2>&1 &
    local console_pid=$!

    local status=0
    wait_for_system_running || status=$?

    kill "${console_pid}" 2>/dev/null || true
    wait "${console_pid}" 2>/dev/null || true

    if ((status != 0)); then
        print_vm_console_log "VM '${VM_NAME}' failed readiness checks."
        return "${status}"
    fi
}
