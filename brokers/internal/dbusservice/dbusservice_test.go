package dbusservice

import (
	"errors"
	"testing"
	"time"

	"github.com/canonical/authd/brokers/internal/broker"
	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

func TestBrokerForCallWaitsForInitialization(t *testing.T) {
	service := &Service{initializing: make(chan struct{})}
	iface := &Interface{service: service}
	entered := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		close(entered)
		_, _ = iface.brokerForCall()
		close(finished)
	}()

	<-entered
	select {
	case <-finished:
		t.Fatal("method call was not gated")
	default:
	}

	iface.broker = &broker.Broker{}
	service.initializationDone()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("method call remained gated after initialization")
	}
}

func TestBrokerForCallReturnsInitializationError(t *testing.T) {
	service := &Service{initializing: make(chan struct{})}
	iface := &Interface{service: service}
	wantErr := errors.New("initialization failed")

	service.initializationFailed(wantErr)

	b, err := iface.brokerForCall()
	require.Nil(t, b)
	require.ErrorIs(t, err, wantErr)

	var unavailableErr *brokerUnavailableError
	require.ErrorAs(t, err, &unavailableErr)
}

func TestNewSessionReturnsBrokerUnavailableErrorAfterInitializationFailure(t *testing.T) {
	service := &Service{initializing: make(chan struct{})}
	iface := &Interface{service: service}
	wantErr := errors.New("initialization failed")

	service.initializationFailed(wantErr)

	_, _, dbusErr := iface.NewSession(dbus.Sender(""), "user@example.com", "en", "login", "provider-id")
	require.NotNil(t, dbusErr)
	require.Equal(t, brokerUnavailableDBusErrorName, dbusErr.Name)
	require.Equal(t, []any{wantErr.Error()}, dbusErr.Body)
}

func TestHandleNameOwnerChangedOnlyTrustsTheBus(t *testing.T) {
	tests := []struct {
		name        string
		sender      string
		wantIgnored bool
	}{
		{
			name:   "the bus disconnecting a client",
			sender: "org.freedesktop.DBus",
		},
		{
			name:        "a client forging a disconnection",
			sender:      ":1.1000",
			wantIgnored: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iface := &Interface{
				iface:    "test",
				sessions: map[string]dbus.Sender{"session-id": ":1.1"},
			}
			service := &Service{interfaces: []*Interface{iface}}

			service.handleNameOwnerChanged(&dbus.Signal{
				Sender: tc.sender,
				Path:   "/org/freedesktop/DBus",
				Name:   "org.freedesktop.DBus.NameOwnerChanged",
				Body:   []any{":1.1", ":1.1", ""},
			})

			iface.sessionsMu.Lock()
			defer iface.sessionsMu.Unlock()

			if tc.wantIgnored {
				require.Contains(t, iface.sessions, "session-id",
					"Sessions must be kept for signals that do not come from the bus")
				return
			}
			require.Empty(t, iface.sessions, "The sessions of the vanished client should be dropped")
		})
	}
}
