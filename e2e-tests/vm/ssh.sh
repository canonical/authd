#!/usr/bin/env bash

set -euo pipefail

usage(){
    cat << EOF

    Usage: $0 --release <release> --user <user> [--] [ssh_options]

    SSH into the e2e-test VM for the specified Ubuntu release.

    Options:
      --release, -r <release>    The Ubuntu release to connect to (e.g., "noble", "resolute").
      --user, -u <user>          The SSH user to connect as (default: "root").

    Example:
      $0 --release resolute
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
        -u|--user)
            SSH_USER="$2"
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

if [ -z "${VM_NAME:-}" ] && [ -z "${RELEASE:-}" ]; then
    echo >&2 "Error: Missing required argument <release>"
    usage >&2
    exit 1
fi

SSH_USER=${SSH_USER:-"root"}

VM_NAME=${VM_NAME:-"e2e-runner-${RELEASE}"}

CID=$(virsh dumpxml "${VM_NAME}" | \
      xmllint --xpath 'string(//vsock/cid/@address)' -)

exec ssh \
  -o ProxyCommand="socat - VSOCK-CONNECT:${CID}:22" \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  -o LogLevel=ERROR \
  "${SSH_USER}@localhost" "$@"
