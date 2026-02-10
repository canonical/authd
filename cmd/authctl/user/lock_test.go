package user_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestUserLockCommand(t *testing.T) {
	t.Parallel()

	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("one_user_and_group"),
		testutils.WithCurrentUserAsRoot,
	)

	err := os.Setenv("AUTHD_SOCKET", daemonSocket)
	require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")

	tests := map[string]struct {
		args             []string
		expectedExitCode int
	}{
		"Lock_user_success": {args: []string{"lock", "user1"}, expectedExitCode: 0},

		"Error_locking_invalid_user": {args: []string{"lock", "invaliduser"}, expectedExitCode: int(codes.NotFound)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}
