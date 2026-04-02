// Package grpcutils provides utility functions for GRPC operations.
package grpcutils

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/log"
	"google.golang.org/grpc"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// WaitForConnection synchronously waits for a [grpc.ClientConn] connection to be established.
func WaitForConnection(ctx context.Context, conn *grpc.ClientConn, timeout time.Duration) (err error) {
	// Block for connection to be started.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Debugf(ctx, "Connecting to %s", conn.Target())
	conn.Connect()

	healthClient := healthgrpc.NewHealthClient(conn)
	hcReq := &healthgrpc.HealthCheckRequest{Service: consts.ServiceName}
	for {
		r, err := healthClient.Check(waitCtx, hcReq, grpc.WaitForReady(true))
		if err != nil {
			return fmt.Errorf("could not connect to %v: %w", conn.Target(), err)
		}
		if r.Status != healthgrpc.HealthCheckResponse_SERVING {
			continue
		}
		return nil
	}
}
