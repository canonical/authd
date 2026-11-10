package users_test

import (
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/users"
	userstestutils "github.com/canonical/authd/internal/users/testutils"
	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedUpdateWithoutUserManagementLock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		locked  bool
		renamed bool
	}{
		{name: "Unchanged_user"},
		{name: "Locked_user", locked: true},
		{name: "Locked_user_selected_by_provider_ID", locked: true, renamed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newManagerForTests(t, t.TempDir())
			t.Cleanup(func() { _ = m.Stop() })
			user := types.UserInfo{
				Name: "authenticated-user", ProviderUsername: "authenticated-user",
				BrokerID: "broker-id", ProviderID: "provider-id",
				Dir: "/home/authenticated-user", Shell: "/bin/bash",
			}
			stored, err := m.UpdateAuthenticatedUser(user)
			require.NoError(t, err)
			if tc.locked {
				require.NoError(t, m.LockUser(user.Name))
			}
			if tc.renamed {
				user.Name, user.ProviderUsername = "renamed-user", "renamed-user"
			}

			release := sync.OnceFunc(m.HoldUserManagementLock())
			defer release()

			done := make(chan struct{})
			var updated types.UserInfo
			var updateErr error
			go func() {
				defer close(done)
				updated, updateErr = m.UpdateAuthenticatedUser(user)
			}()

			// Mutex waits are not durably blocked in synctest.
			select {
			case <-done:
			case <-time.After(testutils.MultipliedSleepDuration(10 * time.Second)):
				release()
				<-done
				t.Fatal("authenticated update waited for the user-management lock")
			}
			if tc.locked {
				require.ErrorIs(t, updateErr, users.ErrUserLocked)
				row, err := userstestutils.DBManager(m).UserByName(stored.Name)
				require.NoError(t, err)
				require.Equal(t, stored.ProviderUsername, row.ProviderUsername)
			} else {
				require.NoError(t, updateErr)
				require.Equal(t, stored, updated)
			}
		})
	}
}

func TestAuthenticatedUpdateStoresAliasOnlyChange(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })
	user := types.UserInfo{
		Name: "authenticated-user", ProviderUsername: "authenticated-user",
		BrokerID: "broker-id", ProviderID: "provider-id",
		Dir: "/home/authenticated-user", Shell: "/bin/bash",
	}
	_, err := m.UpdateAuthenticatedUser(user)
	require.NoError(t, err)
	user.ProviderUsername = "new-provider-username"

	_, err = m.UpdateAuthenticatedUser(user)
	require.NoError(t, err)

	row, err := userstestutils.DBManager(m).UserByName(user.Name)
	require.NoError(t, err)
	require.Equal(t, user.ProviderUsername, row.ProviderUsername)
}
