// Package permissions handles peer user detection and permissions.
package permissions

import (
	"context"
	"errors"
	"os/user"
	"strconv"
	"strings"

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
// The pid and uid are extracted from peerAuthInfo in the gRPC context.
func (m Manager) CheckRequestIsFromRoot(ctx context.Context) (err error) {
	pci, err := peerCredentials(ctx)
	if err != nil {
		return err
	}

	if pci.uid != m.rootUID {
		return errors.New("only root can perform this operation")
	}

	return nil
}

// CheckRequestIsFromRootOrUser checks if the current gRPC request is from a
// root user or from the given user, and returns an error if not.
func (m Manager) CheckRequestIsFromRootOrUser(ctx context.Context, username string) (err error) {
	pci, err := peerCredentials(ctx)
	if err != nil {
		return err
	}

	if pci.uid == m.rootUID {
		return nil
	}

	u, err := user.LookupId(strconv.FormatUint(uint64(pci.uid), 10))
	if err != nil || !isRequestedUser(u.Username, username) {
		return errors.New("only root can perform this operation for other users")
	}

	return nil
}

// isRequestedUser returns whether a request about username is about the user
// named peerUsername.
//
// The services only handle lowercase usernames, so a request is only about the
// user whose name is exactly the lowercase form of the requested one. Comparing
// the names case-insensitively instead would match different users, as some
// characters fold or lowercase to the ones of another name.
func isRequestedUser(peerUsername, username string) bool {
	return username != "" && strings.ToLower(username) == peerUsername
}

func peerCredentials(ctx context.Context) (peerAuthInfo, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return peerAuthInfo{}, errors.New("context request doesn't have gRPC peer information")
	}
	pci, ok := p.AuthInfo.(peerAuthInfo)
	if !ok {
		return peerAuthInfo{}, errors.New("context request doesn't have valid gRPC peer credential information")
	}

	return pci, nil
}
