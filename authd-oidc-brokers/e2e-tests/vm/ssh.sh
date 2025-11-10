#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF

    Usage: $0 --release <release> [ssh options]

    SSH into the e2e-test VM for the specified Ubuntu release.

    Example:
      $0 --release questing
EOF
}

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            print_usage
            exit 0
            ;;
        --release|-r)
            RELEASE="$2"
            shift 2
            ;;
        --)
            shift
            break
            ;;
        *)
            # Pass other options to ssh
            break
    esac
done


if [ -z "${RELEASE:-}" ]; then
    echo "Error: Missing required argument <release>"
    usage
    exit 1
fi

VM_NAME="e2e-runner-${RELEASE}"

if [ "${RELEASE}" = "noble" ]; then
    # On noble, sshd does not listen on VSOCK, so we connect via IPv4
    IPADDR=$(virsh domifaddr --domain e2e-runner-noble | awk '/ipv4/ {print $4}' | cut -d/ -f1 | tail -n1)
    if [ -z "${IPADDR}" ]; then
        echo "Error: Could not determine IP address of VM ${VM_NAME}"
        exit 1
    fi

    exec ssh \
      -o UserKnownHostsFile=/dev/null \
      -o StrictHostKeyChecking=no \
      -o LogLevel=ERROR \
      ubuntu@"${IPADDR}" "$@"
fi

CID=$(virsh dumpxml "${VM_NAME}" | \
      xmllint --xpath 'string(//vsock/cid/@address)' -)

exec ssh \
  -o ProxyCommand="socat - VSOCK-CONNECT:${CID}:22" \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  -o LogLevel=ERROR \
  ubuntu@localhost "$@"
