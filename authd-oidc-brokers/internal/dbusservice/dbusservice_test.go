package dbusservice

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

func TestGateIncomingCallsWaitsForInitialization(t *testing.T) {
	service := &Service{initializing: make(chan struct{})}
	entered := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		close(entered)
		service.gateIncomingCalls(&dbus.Message{Type: dbus.TypeMethodCall})
		close(finished)
	}()

	<-entered
	select {
	case <-finished:
		t.Fatal("method call was not gated")
	default:
	}

	service.initializationDone()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("method call remained gated after initialization")
	}
}

func TestGateIncomingCallsDoesNotBlockReplies(t *testing.T) {
	service := &Service{initializing: make(chan struct{})}

	require.NotPanics(t, func() {
		service.gateIncomingCalls(&dbus.Message{Type: dbus.TypeMethodReply})
	})
}
