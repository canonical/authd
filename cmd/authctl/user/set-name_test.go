package user_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"google.golang.org/grpc/codes"
)

func TestSetName(t *testing.T) {
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
		args             []string
		expectedExitCode int
	}{
		"Set_name_success": {args: []string{"set-name", "user1", "user1-renamed"}, expectedExitCode: 0},

		"Error_when_user_does_not_exist": {
			args:             []string{"set-name", "invaliduser", "newname"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_new_name_already_exists": {
			args:             []string{"set-name", "user1", "user1"},
			expectedExitCode: int(codes.Unknown),
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
