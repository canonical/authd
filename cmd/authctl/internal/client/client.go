// Package client provides a utility function to create a gRPC client for the authd service.
package client

import (
	"fmt"
	"os"
	"regexp"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/proto/authd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewUserServiceClient creates and returns a new [authd.UserServiceClient].
func NewUserServiceClient() (authd.UserServiceClient, error) {
	authdSocket := os.Getenv("AUTHD_SOCKET")
	if authdSocket == "" {
		authdSocket = "unix://" + consts.DefaultSocketPath
	}

	// Check if the socket has a scheme, else default to "unix://"
	schemeRegex := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	if !schemeRegex.MatchString(authdSocket) {
		authdSocket = "unix://" + authdSocket
	}

	conn, err := grpc.NewClient(authdSocket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to authd: %w", err)
	}

	client := authd.NewUserServiceClient(conn)
	return client, nil
}
