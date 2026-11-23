package permissions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/users/proc"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// WithUnixPeerCreds returns a ServerOption that sets credentials for server connections.
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
	peerFD := -1
	var peerFDErr error
	err = raw.Control(func(fd uintptr) {
		if fd > math.MaxInt {
			errClosure = fmt.Errorf("file descriptor value %d is too large to convert to int", fd)
			return
		}
		cred, errClosure = unix.GetsockoptUcred(int(fd),
			unix.SOL_SOCKET,
			unix.SO_PEERCRED)
		if errClosure == nil {
			peerFD, peerFDErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
		}
	})
	if errClosure != nil {
		if peerFD >= 0 {
			_ = unix.Close(peerFD)
		}
		return nil, nil, fmt.Errorf("GetsockoptUcred() error: %v", errClosure)
	}
	if err != nil {
		if peerFD >= 0 {
			_ = unix.Close(peerFD)
		}
		return nil, nil, fmt.Errorf("Control() error: %v", err)
	}

	var process *proc.Process
	if peerFDErr == nil && peerFD >= 0 {
		process, _ = proc.ProcessFromFile(os.NewFile(uintptr(peerFD), "peer-pidfd"))
	}
	// Other RPCs do not need a live process or peer-pidfd kernel support.
	return &peerConnection{Conn: conn, process: process}, peerAuthInfo{uid: cred.Uid, pid: cred.Pid, process: process}, nil
}
func (serverPeerCreds) ClientHandshake(_ context.Context, _ string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return conn, nil, nil
}
func (serverPeerCreds) Info() credentials.ProtocolInfo          { return credentials.ProtocolInfo{} }
func (serverPeerCreds) Clone() credentials.TransportCredentials { return nil }
func (serverPeerCreds) OverrideServerName(_ string) error       { return nil }

type peerAuthInfo struct {
	uid     uint32
	pid     int32
	process *proc.Process
}

type peerConnection struct {
	net.Conn
	process *proc.Process
}

func (c *peerConnection) Close() error {
	if c.process != nil {
		_ = c.process.Close()
	}
	return c.Conn.Close()
}

// AuthType returns a string containing the uid and pid of caller.
func (p peerAuthInfo) AuthType() string {
	return fmt.Sprintf("uid: %d, pid: %d", p.uid, p.pid)
}
