package testutils_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestDaemonRetainsPreAuthDescriptorsAcrossRestart(t *testing.T) {
	executable, cleanup, err := testutils.BuildAuthdWithExampleBroker()
	require.NoError(t, err)
	t.Cleanup(cleanup)
	dbDir := filepath.Join(t.TempDir(), "db")
	groupFile := filepath.Join(t.TempDir(), "group")
	require.NoError(t, os.WriteFile(groupFile, nil, 0600))
	start := func() (authd.UserServiceClient, func()) {
		t.Helper()
		socket, stop := testutils.StartAuthdWithCancel(t, executable,
			testutils.WithDBPath(dbDir), testutils.WithGroupFile(groupFile), testutils.WithCurrentUserAsRoot)
		conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return authd.NewUserServiceClient(conn), stop
	}
	client, stop := start()
	t.Cleanup(stop)
	first, err := client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{
		Name: "user-pre-check@example.com", ShouldPreCheck: true,
	})
	require.NoError(t, err)
	firstEntry, err := client.GetUserByID(context.Background(), &authd.GetUserByIDRequest{Id: first.Uid})
	require.NoError(t, err)
	stop()
	client, stop = start()
	t.Cleanup(stop)
	restoredEntry, err := client.GetUserByID(context.Background(), &authd.GetUserByIDRequest{Id: first.Uid})
	require.NoError(t, err)
	require.Equal(t, firstEntry.Name, restoredEntry.Name,
		"a restarted daemon must restore the stored temporary user name")
	second, err := client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{
		Name: "user-pre-check@example.com", ShouldPreCheck: true,
	})
	require.NoError(t, err)
	require.Equal(t, first.Uid, second.Uid)
	require.Equal(t, first.Gid, second.Gid)
	require.Equal(t, first.Name, second.Name)
}

func TestSharedDaemonRetainsPreAuthDescriptorsAfterStartingTest(t *testing.T) {
	executable, cleanup, err := testutils.BuildAuthdWithExampleBroker()
	require.NoError(t, err)
	t.Cleanup(cleanup)
	dbDir := filepath.Join(t.TempDir(), "db")
	groupFile := filepath.Join(t.TempDir(), "group")
	require.NoError(t, os.WriteFile(groupFile, nil, 0600))

	var client authd.UserServiceClient
	var first *authd.User
	parent := t
	t.Run("starting_test", func(t *testing.T) {
		socket, stop := testutils.StartAuthdWithCancel(t, executable,
			testutils.WithDBPath(dbDir), testutils.WithGroupFile(groupFile),
			testutils.WithCurrentUserAsRoot, testutils.WithSharedDaemon(true))
		// The final shared user, not this starting test, stops the daemon.
		parent.Cleanup(stop)
		conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		parent.Cleanup(func() { _ = conn.Close() })
		client = authd.NewUserServiceClient(conn)
		first, err = client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{
			Name: "user-pre-check@example.com", ShouldPreCheck: true,
		})
		require.NoError(t, err)
	})

	second, err := client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{
		Name: "user-pre-check@example.com", ShouldPreCheck: true,
	})
	require.NoError(t, err)
	require.Equal(t, first.Uid, second.Uid)
	require.Equal(t, first.Gid, second.Gid)
	_, err = client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{
		Name: "user-integration-pre-check-shared@example.com", ShouldPreCheck: true,
	})
	require.NoError(t, err, "new prechecks must still be able to retain descriptors")
}
