#!/bin/bash

function assert_env_vars() {
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
        printf 'Create a config file from the template at e2e-tests/vm/config.sh.template\n' >&2
        printf 'or set the missing variables in the environment.\n' >&2
        exit 1
    fi
}

function has_snapshot() {
    local snapshot_name="$1"
    virsh snapshot-list "${VM_NAME}" | grep -q "${snapshot_name}"
}

function force_create_snapshot() {
    local snapshot_name="$1"
    if has_snapshot "${snapshot_name}"; then
        time virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "${snapshot_name}"
    fi

    if virsh domstate "${VM_NAME}" | grep -q '^running'; then
        # If the VM is running, we have to use --memspec to create the snapshot
        local memfile="${IMAGE%.qcow2}-${snapshot_name}.mem"
        time virsh snapshot-create-as --domain "${VM_NAME}" --name "${snapshot_name}" \
          --memspec "${memfile},snapshot=external"
        return
    fi

    time virsh snapshot-create-as --domain "${VM_NAME}" --name "${snapshot_name}" --disk-only
}

function restore_snapshot_and_sync_time() {
    local snapshot_name="$1"
    virsh snapshot-revert "${VM_NAME}" --snapshotname "${snapshot_name}"
    sync_time
}

function sync_time() {
    local cmd="nm-online -q && \
sudo systemctl restart systemd-timesyncd.service && \
timedatectl show -p NTPSynchronized --value | grep -q yes"
    retry --times 10 --delay 3 -- "$SSH" -- "$cmd"
}

function wait_for_system_running() {
    # Wait until we can connect via SSH
    retry --times 30 --delay 3 -- "$SSH" -- true
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
    local cmd="virsh shutdown \"${VM_NAME}\" && \
virsh await \"${VM_NAME}\" --condition domain-inactive --timeout 5"
    retry --times 3 --delay 1 -- sh -c "$cmd"
}

function boot_system() {
    virsh start "${VM_NAME}"
    wait_for_system_running
}
