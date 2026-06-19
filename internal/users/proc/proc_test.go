package proc_test

import (
	"os"
	"os/user"
	"testing"

	"github.com/canonical/authd/internal/users/proc"
	"github.com/stretchr/testify/require"
)

func TestCheckUserBusy(t *testing.T) {
	t.Parallel()

	currentUser, err := user.Current()
	require.NoError(t, err, "failed to get current user")

	tests := map[string]struct {
		user      string
		uid       uint32
		wantError bool
	}{
		"The_nobody_user_has_no_processes": {
			user:      "nobody",
			uid:       65534,
			wantError: false,
		},
		"The_current_user_has_processes": {
			user: currentUser.Name,
			//nolint:gosec // G115 UIDs are never negative
			uid:       uint32(os.Getuid()),
			wantError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := proc.CheckUserBusy(tc.user, tc.uid)
			t.Logf("CheckUserBusy returned: %v", err)
			if tc.wantError {
				require.Error(t, err, "CheckUserBusy should return an error")
				return
			}
			require.NoError(t, err, "CheckUserBusy should not return an error")
		})
	}
}
