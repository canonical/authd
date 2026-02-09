package localentries_test

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/users/localentries"
	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestEntriesWithLockInvalidActions(t *testing.T) {
	// This cannot be parallel

	require.Panics(t, func() { (&localentries.UserDBLocked{}).MustBeLocked() },
		"MustBeLocked should panic but did not")
	require.Panics(t, func() { _, _ = (&localentries.UserDBLocked{}).GetUserEntries() },
		"GetUserEntries should panic but did not")
	require.Panics(t, func() { _, _ = (&localentries.UserDBLocked{}).GetGroupEntries() },
		"GetGroupEntries should panic but did not")
	require.Panics(t, func() { _, _ = (&localentries.UserDBLocked{}).GetLocalGroupEntries() },
		"GetLocalGroupEntries should panic but did not")

	le, unlock, err := localentries.WithUserDBLock()
	require.NotNil(t, le, "GetWithLock should not return nil but it did")

	require.NoError(t, err, "Setup: failed to lock the users group")
	err = unlock()
	require.NoError(t, err, "Unlock should not fail to lock the users group")

	err = unlock()
	require.Error(t, err, "Unlocking twice should fail")

	require.Panics(t, func() { _, _ = le.GetUserEntries() },
		"GetUserEntries should panic but did not")
	require.Panics(t, func() { _, _ = le.GetGroupEntries() },
		"GetGroupEntries should panic but did not")
	require.Panics(t, func() { _, _ = le.GetLocalGroupEntries() },
		"GetLocalGroupEntries should panic but did not")

	// This is to ensure that we're in a good state, despite the actions above
	for range 10 {
		_, unlock, err = localentries.WithUserDBLock()
		require.NoError(t, err, "Failed to lock the users group")
		defer func() {
			err := unlock()
			require.NoError(t, err, "Unlock should not fail to lock the users group")
		}()
	}
}

//nolint:tparallel // This can't be parallel, but subtests can.
func TestRacingEntriesLockingActions(t *testing.T) {
	const nIterations = 50

	testFilePath := filepath.Join("testdata", "no_users_in_our_groups.group")
	testUserDBLocked := &localentries.UserDBLocked{}

	wg := sync.WaitGroup{}
	wg.Add(nIterations)

	// Lock and get the values in parallel.
	for idx := range nIterations {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			t.Cleanup(wg.Done)

			var opts []localentries.Option
			wantGroup := types.GroupEntry{Name: "root", GID: 0, Passwd: "x"}
			useTestGroupFile := idx%3 == 0

			if useTestGroupFile {
				// Mix the requests with test-only code paths...
				opts = append(opts,
					localentries.WithGroupPath(testFilePath),
					localentries.WithMockUserDBLocking(),
					localentries.WithUserDBLockedInstance(testUserDBLocked),
				)
				wantGroup = types.GroupEntry{Name: "localgroup1", GID: 41, Passwd: "x"}
			}

			ctx, entriesUnlock, err := localentries.WithUserDBLock(opts...)
			require.NoError(t, err, "Failed to lock the local entries")

			groups, err := localentries.GetGroupEntries(ctx)
			require.NoError(t, err, "GetEntries should not return an error, but did")
			require.NotEmpty(t, groups, "Got empty groups (test groups: %v)", useTestGroupFile)
			require.Contains(t, groups, wantGroup, "Expected group was not found  (test groups: %v)", useTestGroupFile)
			err = entriesUnlock()
			require.NoError(t, err, "EntriesUnlock() should not fail to lock the users group (test groups: %v)", useTestGroupFile)
		})
	}
}

//nolint:dupl  // This is not a duplicated test.
func TestIsUniqueUserName(t *testing.T) {
	t.Parallel()

	le, unlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Setup: NewUserDBLocked should not fail to lock the users group")

	t.Cleanup(func() {
		err := unlock()
		require.NoError(t, err, "TearDown: Unlock should not fail, but it did")
	})

	users, err := le.GetUserEntries()
	require.NoError(t, err, "Setup: GetUserEntries should not fail, but it did")

	for _, u := range users {
		t.Run(fmt.Sprintf("user_%s", u.Name), func(t *testing.T) {
			t.Parallel()

			unique, err := le.IsUniqueUserName(u.Name)
			require.NoError(t, err, "IsUniqueUserName should not fail, but it did")
			require.False(t, unique, "IsUniqueUserName should not return true for user %q", u.Name)

			bytes := make([]byte, 16)
			_, err = rand.Read(bytes)
			require.NoError(t, err, "Setup: Rand should not fail, but it did")

			otherName := fmt.Sprintf("%s-%x", u.Name, bytes)
			unique, err = le.IsUniqueUserName(otherName)
			require.NoError(t, err, "IsUniqueUserName should not fail, but it did")
			require.True(t, unique, "IsUniqueUserName should not return false for user %q", otherName)
		})
	}
}

//nolint:dupl  // This is not a duplicated test.
func TestIsUniqueGroupName(t *testing.T) {
	t.Parallel()

	le, unlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Setup: NewUserDBLocked should not fail to lock the users group")

	t.Cleanup(func() {
		err := unlock()
		require.NoError(t, err, "TearDown: Unlock should not fail, but it did")
	})

	groups, err := le.GetGroupEntries()
	require.NoError(t, err, "Setup: GetGroupEntries should not fail, but it did")

	for _, g := range groups {
		t.Run(fmt.Sprintf("group_%s", g.Name), func(t *testing.T) {
			t.Parallel()

			unique, err := le.IsUniqueGroupName(g.Name)
			require.NoError(t, err, "IsUniqueGroupName should not fail, but it did")
			require.False(t, unique, "IsUniqueGroupName should not return true for user %q", g.Name)

			bytes := make([]byte, 16)
			_, err = rand.Read(bytes)
			require.NoError(t, err, "Setup: Rand should not fail, but it did")

			otherName := fmt.Sprintf("%s-%x", g.Name, bytes)
			unique, err = le.IsUniqueGroupName(otherName)
			require.NoError(t, err, "IsUniqueGroupName should not fail, but it did")
			require.True(t, unique, "IsUniqueGroupName should not return false for user %q", otherName)
		})
	}
}

//nolint:dupl  // This is not a duplicated test.
func TestIsUniqueUID(t *testing.T) {
	t.Parallel()

	le, unlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Setup: NewUserDBLocked should not fail to lock the users group")

	t.Cleanup(func() {
		err := unlock()
		require.NoError(t, err, "TearDown: Unlock should not fail, but it did")
	})

	users, err := le.GetUserEntries()
	require.NoError(t, err, "Setup: GetUserEntries should not fail, but it did")

	for _, u := range users {
		t.Run(fmt.Sprintf("user_%s", u.Name), func(t *testing.T) {
			t.Parallel()

			unique, err := le.IsUniqueUID(u.UID)
			require.NoError(t, err, "IsUniqueUID should not fail, but it did")
			require.False(t, unique, "IsUniqueUID should not return true for user %q", u.Name)
		})
	}

	t.Run("at_least_an_unique_id", func(t *testing.T) {
		t.Parallel()

		maxUIDUser := slices.MaxFunc(users, func(a types.UserEntry, b types.UserEntry) int {
			return int(max(a.UID, b.UID))
		})

		// This has to return one day...
		for uid := maxUIDUser.UID; ; uid++ {
			unique, err := le.IsUniqueUID(uid)
			require.NoError(t, err, "IsUniqueUID should not fail, but it did")
			if unique {
				t.Logf("Found unique ID %d", uid)
				break
			}
		}
	})
}

//nolint:dupl  // This is not a duplicated test.
func TestIsUniqueGID(t *testing.T) {
	t.Parallel()

	le, unlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Setup: NewUserDBLocked should not fail to lock the users group")

	t.Cleanup(func() {
		err := unlock()
		require.NoError(t, err, "TearDown: Unlock should not fail, but it did")
	})

	groups, err := le.GetGroupEntries()
	require.NoError(t, err, "Setup: GetUserEntries should not fail, but it did")

	for _, g := range groups {
		t.Run(fmt.Sprintf("group_%s", g.Name), func(t *testing.T) {
			t.Parallel()

			unique, err := le.IsUniqueGID(g.GID)
			require.NoError(t, err, "IsUniqueGID should not fail, but it did")
			require.False(t, unique, "IsUniqueGID should not return true for user %q", g.Name)
		})
	}

	t.Run("at_least_an_unique_id", func(t *testing.T) {
		t.Parallel()

		maxGIDGroup := slices.MaxFunc(groups, func(a types.GroupEntry, b types.GroupEntry) int {
			return int(max(a.GID, b.GID))
		})

		// This has to return one day...
		for gid := maxGIDGroup.GID; ; gid++ {
			unique, err := le.IsUniqueGID(gid)
			require.NoError(t, err, "IsUniqueGID should not fail, but it did")
			if unique {
				t.Logf("Found unique ID %d", gid)
				break
			}
		}
	})
}
