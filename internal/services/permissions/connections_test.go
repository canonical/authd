package permissions_test

import (
	"context"
	"testing"

	"github.com/canonical/authd/internal/services/permissions"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/stats"
)

func TestConnectionTracker(t *testing.T) {
	t.Parallel()

	var closedID string
	var requestCanceledBeforeClosed bool

	var requestCtx context.Context
	tracker := permissions.NewConnectionTracker(func(connectionID string) {
		closedID = connectionID
		requestCanceledBeforeClosed = requestCtx.Err() != nil
	})

	connCtx := tracker.TagConn(context.Background(), &stats.ConnTagInfo{})
	connectionID, ok := permissions.ConnectionID(connCtx)
	require.True(t, ok, "The connection should have an ID")
	require.NotEmpty(t, connectionID, "The connection ID should not be empty")

	otherCtx := tracker.TagConn(context.Background(), &stats.ConnTagInfo{})
	otherID, _ := permissions.ConnectionID(otherCtx)
	require.NotEqual(t, connectionID, otherID, "Each connection should have its own ID")

	// Requests use contexts derived from the connection one.
	requestCtx, cancel := context.WithCancel(tracker.TagRPC(connCtx, &stats.RPCTagInfo{}))
	t.Cleanup(cancel)
	requestID, ok := permissions.ConnectionID(requestCtx)
	require.True(t, ok, "The requests should have the ID of their connection")
	require.Equal(t, connectionID, requestID, "The requests should have the ID of their connection")

	tracker.HandleConn(connCtx, &stats.ConnBegin{})
	require.Empty(t, closedID, "The connection should not be notified as closed when it begins")

	tracker.HandleConn(connCtx, &stats.ConnEnd{})
	require.Equal(t, connectionID, closedID, "The closed connection should be notified")
	require.True(t, requestCanceledBeforeClosed,
		"The requests of the connection should be canceled before notifying its closure")
	require.NoError(t, otherCtx.Err(), "Other connections should not be affected")
}
