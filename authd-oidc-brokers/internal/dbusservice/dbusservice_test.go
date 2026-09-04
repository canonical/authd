package dbusservice

import (
	"errors"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
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

	_, _, dbusErr := iface.NewSession("user@example.com", "en", "login", "provider-id")
	require.NotNil(t, dbusErr)
	require.Equal(t, brokerUnavailableDBusErrorName, dbusErr.Name)
	require.Equal(t, []any{wantErr.Error()}, dbusErr.Body)
}
