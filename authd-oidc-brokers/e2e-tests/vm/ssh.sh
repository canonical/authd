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

CID=$(virsh dumpxml "${VM_NAME}" | \
      xmllint --xpath 'string(//vsock/cid/@address)' -)

exec ssh \
  -o ProxyCommand="socat - VSOCK-CONNECT:${CID}:22" \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  -o LogLevel=ERROR \
  ubuntu@localhost "$@"
