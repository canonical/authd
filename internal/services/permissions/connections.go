package permissions

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/stats"
)

type connectionKey struct{}

type connection struct {
	id     string
	cancel context.CancelFunc
}

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
	ctx, cancel := context.WithCancel(ctx)
	return context.WithValue(ctx, connectionKey{}, connection{id: uuid.NewString(), cancel: cancel})
}

// TagRPC leaves the request context untouched.
func (t ConnectionTracker) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

// HandleRPC does nothing, as we only track connections.
func (t ConnectionTracker) HandleRPC(context.Context, stats.RPCStats) {}

// HandleConn notifies the closure of the connection.
func (t ConnectionTracker) HandleConn(ctx context.Context, s stats.ConnStats) {
	if _, closed := s.(*stats.ConnEnd); !closed {
		return
	}

	c, ok := ctx.Value(connectionKey{}).(connection)
	if !ok {
		return
	}

	// gRPC may notify the closure before canceling the requests of the
	// connection, if it was already being closed by something else. Cancel
	// them first, so that they can see that their client went away.
	c.cancel()

	if t.connectionClosed != nil {
		t.connectionClosed(c.id)
	}
}

// ConnectionID returns the ID of the connection that sent the current request.
func ConnectionID(ctx context.Context) (string, bool) {
	c, ok := ctx.Value(connectionKey{}).(connection)
	return c.id, ok && c.id != ""
}
