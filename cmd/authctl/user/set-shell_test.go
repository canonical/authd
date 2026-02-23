package user_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"google.golang.org/grpc/codes"
)

func TestSetShellCommand(t *testing.T) {
	t.Parallel()

	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("one_user_and_group"),
		testutils.WithCurrentUserAsRoot,
	)

	authctlEnv := []string{
		"AUTHD_SOCKET=" + daemonSocket,
		testutils.CoverDirEnv(),
	}

	tests := map[string]struct {
		args []string

		expectedExitCode int
	}{
		"Set_shell_success": {args: []string{"set-shell", "user1", "/bin/bash"}, expectedExitCode: 0},

		"Error_when_user_does_not_exist": {
			args:             []string{"set-shell", "invaliduser", "/bin/bash"},
			expectedExitCode: int(codes.NotFound),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			cmd.Env = authctlEnv
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}
