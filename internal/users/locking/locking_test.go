package userslocking_test

import (
	"context"
	"testing"
	"time"

	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/stretchr/testify/require"
)

func TestUsersLockingOverride(t *testing.T) {
	// This cannot be parallel.

	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	err := userslocking.WriteLock()
	require.NoError(t, err, "Locking should be allowed")

	err = userslocking.WriteLock()
	require.ErrorIs(t, err, userslocking.ErrLock, "Locking again should not be allowed")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	err = userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}

func TestUsersLockingOverrideAsLockedExternally(t *testing.T) {
	// This cannot be parallel.
	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, context.Background())

	lockingExited := make(chan error)
	go func() {
		lockingExited <- userslocking.WriteLock()
	}()

	select {
	case <-time.After(1 * time.Second):
		// If we're time-outing: it's fine, it means we were locked!
	case err := <-lockingExited:
		t.Errorf("We should have not been exited, but we did with error %v", err)
		t.FailNow()
	}

	err := userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	err = <-lockingExited
	require.NoError(t, err, "Previous concurrent locking should have been allowed now")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	err = userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}

func TestUsersLockingOverrideAsLockedExternallyWithContext(t *testing.T) {
	// This cannot be parallel.
	lockCtx, lockCancel := context.WithCancel(context.Background())
	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, lockCtx)

	lockingExited := make(chan error)
	go func() {
		lockingExited <- userslocking.WriteLock()
	}()

	select {
	case <-time.After(1 * time.Second):
		// If we're time-outing: it's fine, it means we were locked!
	case err := <-lockingExited:
		t.Errorf("We should have not been exited, but we did with error %v", err)
		t.FailNow()
	}

	// Remove the "external" lock now.
	lockCancel()

	err := <-lockingExited
	require.NoError(t, err, "Previous concurrent locking should have been allowed now")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	err = userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}
