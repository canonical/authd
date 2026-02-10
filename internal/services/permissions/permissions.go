// Package permissions handles peer user detection and permissions.
package permissions

import (
	"context"
	"errors"

	"google.golang.org/grpc/peer"
)

// Manager is an abstraction of permission process.
type Manager struct {
	rootUID uint32
}

type options struct {
	rootUID uint32
}

var defaultOptions = options{
	rootUID: 0,
}

// Option represents an optional function to override Manager default values.
type Option func(*options)

// New returns a new Manager.
func New(args ...Option) Manager {
	opts := defaultOptions
	for _, arg := range args {
		arg(&opts)
	}

	//nolint:staticcheck // S1016 Those structs are not the same conceptually.
	return Manager{
		rootUID: opts.rootUID,
	}
}

// CheckRequestIsFromRoot checks if the current gRPC request is from a root user and returns an error if not.
// The pid and uid are extracted from peerCredsInfo in the gRPC context.
func (m Manager) CheckRequestIsFromRoot(ctx context.Context) (err error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return errors.New("context request doesn't have gRPC peer information")
	}
	pci, ok := p.AuthInfo.(peerCredsInfo)
	if !ok {
		return errors.New("context request doesn't have valid gRPC peer credential information")
	}

	if pci.uid != m.rootUID {
		return errors.New("only root can perform this operation")
	}

	return nil
}
