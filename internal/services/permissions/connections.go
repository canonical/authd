package permissions

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/stats"
)

type connectionIDKey struct{}

// ConnectionTracker is a gRPC stats handler that assigns an ID to every client connection.
type ConnectionTracker struct {
	connectionClosed func(connectionID string)
}

var _ stats.Handler = ConnectionTracker{}

// NewConnectionTracker returns a stats handler that identifies client connections
// and calls connectionClosed with the ID of the connections that are closed.
func NewConnectionTracker(connectionClosed func(connectionID string)) ConnectionTracker {
	return ConnectionTracker{connectionClosed: connectionClosed}
}

// TagConn assigns a unique ID to the connection, inherited by all its streams.
func (t ConnectionTracker) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return context.WithValue(ctx, connectionIDKey{}, uuid.NewString())
}

// TagRPC leaves the request context untouched.
func (t ConnectionTracker) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

// HandleRPC does nothing, as we only track connections.
func (t ConnectionTracker) HandleRPC(context.Context, stats.RPCStats) {}

// HandleConn notifies the closure of the connection.
func (t ConnectionTracker) HandleConn(ctx context.Context, s stats.ConnStats) {
	if _, closed := s.(*stats.ConnEnd); !closed || t.connectionClosed == nil {
		return
	}

	if connectionID, ok := ConnectionID(ctx); ok {
		t.connectionClosed(connectionID)
	}
}

// ConnectionID returns the ID of the connection that sent the current request.
func ConnectionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(connectionIDKey{}).(string)
	return id, ok && id != ""
}
