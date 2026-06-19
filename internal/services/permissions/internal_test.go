package permissions

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"
)

func TestPeerCredsInfoAuthType(t *testing.T) {
	t.Parallel()

	p := peerAuthInfo{
		uid: 11111,
		pid: 22222,
	}

	require.Equal(t, "uid: 11111, pid: 22222", p.AuthType(), "AuthType returns expected uid and pid")
}

func TestServerPeerCredsHandshake(t *testing.T) {
	t.Parallel()

	s := serverPeerCreds{}

	socket := filepath.Join(t.TempDir(), "authd.sock")
	l, err := net.Listen("unix", socket)
	require.NoError(t, err, "couldn't listen on socket")
	defer l.Close()

	wg := sync.WaitGroup{}
	wg.Add(1)
	var clientErr error
	go func() {
		defer wg.Done()
		unixAddr, err := net.ResolveUnixAddr("unix", socket)
		if err != nil {
			clientErr = fmt.Errorf("Couldn't resolve client socket address: %w", err)
			return
		}
		conn, err := net.DialUnix("unix", nil, unixAddr)
		if err != nil {
			clientErr = fmt.Errorf("Couldn't contact unix socket: %w", err)
			return
		}
		defer conn.Close()
	}()

	conn, err := l.Accept()
	require.NoError(t, err, "Should accept connexion from client")

	// ServerHandshake status check.
	c, i, err := s.ServerHandshake(conn)

	require.NoError(t, err, "ServerHandshake should not fail")
	require.Equal(t, conn, c, "Connexion should match given connection")
	uid := currentUserUID()
	require.Equal(t, fmt.Sprintf("uid: %d, pid: %d", uid, os.Getpid()),
		i.AuthType(), "uid or pid received doesn't match what we expected")

	// ClientHandshake status check.
	c, i, err = s.ClientHandshake(context.Background(), "unused", conn)

	require.NoError(t, err, "ClientHandshake should not fail")
	require.Equal(t, conn, c, "Connexion should match given connection")
	require.Nil(t, i, "No authInfo should be returned")

	err = l.Close()
	require.NoError(t, err, "Teardown: should close listener successfully")
	wg.Wait()

	require.NoError(t, clientErr, "Client should not return an error")
}

func TestServerPeerCredsInvalidSocket(t *testing.T) {
	t.Parallel()

	s := serverPeerCreds{}
	_, _, err := s.ServerHandshake(nil)
	require.Error(t, err, "ServerHandshake should fail when there is no valid connection")
}

func TestServerPeerCredsInterface(t *testing.T) {
	t.Parallel()

	// This only ensure we can call the various methods of the implemented interface
	s := serverPeerCreds{}

	require.Nil(t, s.Clone(), "Clone should return nil")
	require.Nil(t, s.OverrideServerName("unused"), "OverrideServerName is a no-op and should return nil")
	require.Equal(t, credentials.ProtocolInfo{}, s.Info(), "Info should return an empty struct")
}
