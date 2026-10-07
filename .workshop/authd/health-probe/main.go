// The Workshop health probe checks authd's read-only gRPC health endpoint.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/grpcutils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	conn, err := grpc.NewClient("unix://"+consts.DefaultSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	return grpcutils.WaitForConnection(context.Background(), conn, 5*time.Second)
}
