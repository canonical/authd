// TiCS: disabled // This is to pin the tools versions that we use.

//go:build tools

package tools

import (
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/msteinert/pam/v2/cmd/pam-moduler"
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "github.com/golang/protobuf/protoc-gen-go"
)
