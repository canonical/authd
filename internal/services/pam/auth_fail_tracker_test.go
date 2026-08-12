package pam

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthFailTracker_ResetWindow_Zero_DisablesReset(t *testing.T) {
	t.Parallel()

	cfg := BruteForceMitigationConfig{
		AuthFailDelayThreshold: 3,
		AuthFailDelay:          time.Second,
		AuthFailResetWindow:    0,
	}
	tracker := newAuthFailTracker()

	// Three consecutive failures should each increment the counter rather than
	// resetting it. With the bug (resetWindow == 0 always resets), count would
	// stay at 1 on every call.
	require.Equal(t, 1, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "first failure")
	require.Equal(t, 2, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "second failure")
	require.Equal(t, 3, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "third failure: counter must not have been reset")
}

func TestAuthFailTracker_ResetWindow_NonZero_ResetsAfterInactivity(t *testing.T) {
	t.Parallel()

	cfg := BruteForceMitigationConfig{
		AuthFailDelayThreshold: 3,
		AuthFailDelay:          time.Second,
		AuthFailResetWindow:    50 * time.Millisecond,
	}
	tracker := newAuthFailTracker()

	require.Equal(t, 1, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "first failure")
	require.Equal(t, 2, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "second failure")

	// After sleeping past the reset window the entry expires and the counter resets.
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, tracker.recordFailure("", "user", cfg.AuthFailResetWindow), "counter should reset after inactivity")
}
