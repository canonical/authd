#!/usr/bin/env bash

set -euo pipefail

if [ -z "${VM_NAME:-}" ] && [ -z "${RELEASE:-}" ]; then
    echo >&2 "Error: Missing VM_NAME or RELEASE"
    exit 1
fi

if [ "$#" -ne 2 ]; then
    echo >&2 "Usage: $0 <local_path> <remote_path>"
    exit 1
fi

VM_NAME=${VM_NAME:-"e2e-runner-${RELEASE}"}

CID=$(virsh dumpxml "${VM_NAME}" | \
      xmllint --xpath 'string(//vsock/cid/@address)' -)

exec scp \
  -o ProxyCommand="socat - VSOCK-CONNECT:${CID}:22" \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  -o LogLevel=ERROR \
  "$1" "root@localhost:$2"
