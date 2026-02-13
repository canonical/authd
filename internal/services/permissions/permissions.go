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
	isRoot, err := m.isRequestFromRoot(ctx)
	if err != nil {
		return err
	}
	if !isRoot {
		return errors.New("only root can perform this operation")
	}
	return nil
}

// CheckRequestIsFromRootOrUID checks if the current gRPC request is from a root user
// or a specified user and returns an error if not.
func (m Manager) CheckRequestIsFromRootOrUID(ctx context.Context, uid uint32) (err error) {
	isRoot, err := m.isRequestFromRoot(ctx)
	if err != nil {
		return err
	}
	if isRoot {
		return nil
	}

	isFromUID, err := m.isRequestFromUID(ctx, uid)
	if err != nil {
		return err
	}
	if !isFromUID {
		return errors.New("only root or the specified user can perform this operation")
	}
	return nil
}

func (m Manager) isRequestFromRoot(ctx context.Context) (bool, error) {
	return m.isRequestFromUID(ctx, m.rootUID)
}

func (m Manager) isRequestFromUID(ctx context.Context, uid uint32) (bool, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return false, errors.New("context request doesn't have gRPC peer information")
	}
	pci, ok := p.AuthInfo.(peerCredsInfo)
	if !ok {
		return false, errors.New("context request doesn't have valid gRPC peer credential information")
	}

	return pci.uid == uid, nil
}
