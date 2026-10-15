package broker

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/stretchr/testify/require"
)

// TestGetSessionDoesNotWaitForCancelledRequest guards the window between
// startAuthenticate and IsAuthenticated's refresh of its session copy. A
// cancel landing there used to park getSession on a cleanupDone channel that
// only the parked call's own deferred cleanup could close, wedging the
// session for good: every later lookup on that ID parked too, and EndSession
// could not release them.
func TestGetSessionDoesNotWaitForCancelledRequest(t *testing.T) {
	b := &Broker{currentSessions: map[string]session{"session": {}}}
	cleanupDone := make(chan struct{})

	_, authState, err := b.startAuthenticate("session", cleanupDone)
	require.NoError(t, err)
	b.CancelIsAuthenticated("session")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := b.getSession("session")
		require.NoError(t, err)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("getSession blocked on a cleanup only its own caller can complete")
	}

	b.clearAuthentication("session", authState)
	close(cleanupDone)
}

func TestUpdateSessionWithAuthContextRejectsCancelledCommit(t *testing.T) {
	b := &Broker{currentSessions: map[string]session{"session": {}}}

	ctx, authState, err := b.startAuthenticate("session", nil)
	require.NoError(t, err)
	pending, err := b.getSession("session")
	require.NoError(t, err)
	pending.nextAuthModes = []string{"committed-after-cancel"}

	b.CancelIsAuthenticated("session")
	b.clearAuthentication("session", authState)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Error(t, b.updateSessionWithAuthContext("session", pending, authState))

	stored, err := b.getSession("session")
	require.NoError(t, err)
	require.Empty(t, stored.nextAuthModes)
	require.Nil(t, stored.isAuthenticating)
}

func TestUpdateSessionRejectsStaleAuthenticationMarker(t *testing.T) {
	b := &Broker{currentSessions: map[string]session{"session": {}}}

	stale, err := b.getSession("session")
	require.NoError(t, err)
	_, authState, err := b.startAuthenticate("session", nil)
	require.NoError(t, err)
	stale.nextAuthModes = []string{"stale"}

	require.Error(t, b.updateSession("session", stale))
	stored, err := b.getSession("session")
	require.NoError(t, err)
	require.Empty(t, stored.nextAuthModes)
	require.Same(t, authState, stored.isAuthenticating)

	b.CancelIsAuthenticated("session")
	b.clearAuthentication("session", authState)
}

func TestAuthenticationCleanupDoesNotCancelReplacement(t *testing.T) {
	b := &Broker{currentSessions: map[string]session{"session": {}}}

	oldCtx, oldState, err := b.startAuthenticate("session", nil)
	require.NoError(t, err)
	b.CancelIsAuthenticated("session")
	require.ErrorIs(t, oldCtx.Err(), context.Canceled)
	b.clearAuthentication("session", oldState)

	newCtx, newState, err := b.startAuthenticate("session", nil)
	require.NoError(t, err)
	b.clearAuthentication("session", oldState)

	require.ErrorIs(t, oldCtx.Err(), context.Canceled)
	require.NoError(t, newCtx.Err())
	stored, err := b.getSession("session")
	require.NoError(t, err)
	require.Same(t, newState, stored.isAuthenticating)

	b.CancelIsAuthenticated("session")
	b.clearAuthentication("session", newState)
	require.ErrorIs(t, newCtx.Err(), context.Canceled)
}

func trackedCommitMFAFlow(released *atomic.Int32) *himmelblau.MFAFlowState {
	flow := &himmelblau.MFAFlowState{}
	releaseField := reflect.ValueOf(flow).Elem().FieldByName("release")
	//nolint:gosec // G103: unsafe pointer required to set an unexported test field.
	reflect.NewAt(releaseField.Type(), unsafe.Pointer(releaseField.UnsafeAddr())).Elem().Set(reflect.ValueOf(func() {
		released.Add(1)
	}))
	return flow
}

func TestDiscardUncommittedMFAFlowKeepsStoredFlow(t *testing.T) {
	var storedReleased, incomingReleased atomic.Int32
	storedFlow := trackedCommitMFAFlow(&storedReleased)
	b := &Broker{
		currentSessions: map[string]session{
			"session": {mfaFlowActive: storedFlow},
		},
	}

	b.discardUncommittedMFAFlow("session", trackedCommitMFAFlow(&incomingReleased))
	require.Equal(t, int32(1), incomingReleased.Load())
	require.Equal(t, int32(0), storedReleased.Load())

	stored, err := b.getSession("session")
	require.NoError(t, err)
	require.Same(t, storedFlow, stored.mfaFlowActive)
}

func TestUpdateSessionRejectsStaleMFAFlowCopy(t *testing.T) {
	var oldReleased, currentReleased, staleReleased atomic.Int32
	b := &Broker{
		currentSessions: map[string]session{
			"session": {mfaFlowActive: trackedCommitMFAFlow(&oldReleased)},
		},
	}

	stale, err := b.getSession("session")
	require.NoError(t, err)

	current := stale
	current.mfaFlowActive = trackedCommitMFAFlow(&currentReleased)
	require.NoError(t, b.updateSession("session", current))
	require.Equal(t, int32(1), oldReleased.Load())

	stale.mfaFlowActive = trackedCommitMFAFlow(&staleReleased)
	require.Error(t, b.updateSession("session", stale))
	require.Equal(t, int32(1), staleReleased.Load())

	stored, err := b.getSession("session")
	require.NoError(t, err)
	require.Same(t, current.mfaFlowActive, stored.mfaFlowActive)
	require.Equal(t, int32(0), currentReleased.Load())

	require.NoError(t, b.EndSession("session"))
	require.Equal(t, int32(1), currentReleased.Load())
}
