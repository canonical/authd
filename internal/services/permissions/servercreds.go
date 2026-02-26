package permissions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"

	"github.com/canonical/authd/internal/decorate"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// WithUnixPeerCreds returns the credentials of the caller.
func WithUnixPeerCreds() grpc.ServerOption {
	return grpc.Creds(serverPeerCreds{})
}

// serverPeerCreds encapsulates a TransportCredentials which extracts uid and pid of caller via Unix Socket SO_PEERCRED.
type serverPeerCreds struct{}

func (serverPeerCreds) ServerHandshake(conn net.Conn) (n net.Conn, c credentials.AuthInfo, err error) {
	defer decorate.OnError(&err, "server handshake failed")

	var cred *unix.Ucred
	// net.Conn is an interface. Expect only *net.UnixConn types
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, nil, errors.New("unexpected socket type")
	}

	// Fetches raw network connection from UnixConn
	raw, err := uc.SyscallConn()
	if err != nil {
		return nil, nil, fmt.Errorf("error opening raw connection: %v", err)
	}

	// The raw.Control() callback does not return an error directly.
	// In order to capture errors, we wrap already defined variable 'errClosure'.
	// 'err' is then the error returned by Control() itself.
	var errClosure error
	err = raw.Control(func(fd uintptr) {
		if fd > math.MaxInt {
			errClosure = fmt.Errorf("file descriptor value %d is too large to convert to int", fd)
			return
		}
		cred, errClosure = unix.GetsockoptUcred(int(fd),
			unix.SOL_SOCKET,
			unix.SO_PEERCRED)
	})
	if errClosure != nil {
		return nil, nil, fmt.Errorf("GetsockoptUcred() error: %v", errClosure)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("Control() error: %v", err)
	}

	return conn, peerCredsInfo{uid: cred.Uid, pid: cred.Pid}, nil
}
func (serverPeerCreds) ClientHandshake(_ context.Context, _ string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return conn, nil, nil
}
func (serverPeerCreds) Info() credentials.ProtocolInfo          { return credentials.ProtocolInfo{} }
func (serverPeerCreds) Clone() credentials.TransportCredentials { return nil }
func (serverPeerCreds) OverrideServerName(_ string) error       { return nil }

type peerCredsInfo struct {
	uid uint32
	pid int32
}

// AuthType returns a string containing the uid and pid of caller.
func (p peerCredsInfo) AuthType() string {
	return fmt.Sprintf("uid: %d, pid: %d", p.uid, p.pid)
}
