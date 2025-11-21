package user_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
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
			t.Logf("Running command: %s", strings.Join(cmd.Args, " "))
			outputBytes, err := cmd.CombinedOutput()
			output := string(outputBytes)
			exitCode := cmd.ProcessState.ExitCode()

			t.Logf("Command output:\n%s", output)

			if tc.expectedExitCode == 0 {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expectedExitCode, exitCode, "Expected exit code does not match actual exit code")

			golden.CheckOrUpdate(t, output)
		})
	}
}
