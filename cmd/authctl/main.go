// Package main implements Cobra commands for management operations on authd.
package main

import (
	"os"

	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/cmd/authctl/root"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	if err := root.RootCmd.Execute(); err != nil {
		s, ok := status.FromError(err)
		if !ok {
			// If the error is not a gRPC status, we print it as is.
			log.Error(err.Error())
			os.Exit(1)
		}

		// If the error is a gRPC status, we print the message and exit with the gRPC status code.
		switch s.Code() {
		case codes.PermissionDenied:
			log.Errorf("Permission denied: %s", s.Message())
		default:
			log.Errorf("Error: %s", s.Message())
		}
		code := int(s.Code())
		if code < 0 || code > 255 {
			// We cannot exit with a negative code or a code greater than 255,
			// so we map it to 1 in that case.
			code = 1
		}

		os.Exit(code)
	}
}
