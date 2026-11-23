// Package permissions handles peer user detection and permissions.
package permissions

import (
	"context"
	"errors"

	"github.com/canonical/authd/internal/users/proc"
	"google.golang.org/grpc/peer"
)

// Manager is an abstraction of permission process.
type Manager struct {
	rootUID uint32
}

// PeerProcess returns the process identity captured from Unix peer credentials
// during the server handshake.
func PeerProcess(ctx context.Context) (*proc.Process, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, errors.New("missing gRPC peer information")
	}
	info, ok := p.AuthInfo.(peerAuthInfo)
	if !ok || info.process == nil {
		return nil, errors.New("missing kernel peer pidfd")
	}
	alive, err := info.process.Alive()
	if err != nil {
		return nil, err
	}
	if !alive {
		return nil, errors.New("peer process has exited")
	}
	return info.process, nil
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
// The pid and uid are extracted from peerAuthInfo in the gRPC context.
func (m Manager) CheckRequestIsFromRoot(ctx context.Context) (err error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return errors.New("context request doesn't have gRPC peer information")
	}
	pci, ok := p.AuthInfo.(peerAuthInfo)
	if !ok {
		return errors.New("context request doesn't have valid gRPC peer credential information")
	}

	if pci.uid != m.rootUID {
		return errors.New("only root can perform this operation")
	}

	return nil
}
