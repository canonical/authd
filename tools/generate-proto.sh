#!/usr/bin/env bash

set -euo pipefail

if [ -v DEB_HOST_GNU_TYPE ]; then
    echo "generate-proto.sh: Package build environment detected, skipping proto generation"
    exit 0
fi

# TODO: Watch https://github.com/protocolbuffers/protobuf for any changes on the
# experimental status of optional fields, previously described on:
#  https://github.com/protocolbuffers/protobuf/blob/main/docs/implementing_proto3_presence.md.
args=(
    --proto_path=.
    --go_out=.
    --go_opt=paths=source_relative

    # Should it become default, remove the --experimental_allow_proto3_optional
    # flag from the go generate command below.
    --experimental_allow_proto3_optional
)

while [ "$#" -gt 0 ]; do
    case "$1" in
        --with-grpc)
            args+=(
                --go-grpc_out=.
                --go-grpc_opt=paths=source_relative
            )
            shift
        ;;
        --)
            shift
            break
        ;;
        -*)
            args+=("$1")
            shift
        ;;
        *)
            proto_file="$1"
            shift
        ;;
    esac
done

if [ ! -e "$proto_file" ]; then
    echo "No proto or invalid file provided: $proto_file"
    exit 1
fi

PATH="$(go env GOPATH)/bin:$PATH"
export PATH

exec protoc "${args[@]}" "$proto_file" "${@}"
