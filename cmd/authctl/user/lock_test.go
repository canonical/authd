package user_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"google.golang.org/grpc/codes"
)

type userLockCommandTestCase struct {
	name             string
	username         string
	expectedExitCode int
}

//nolint:tparallel // The subtests share one daemon and user database.
func TestUserLockCommand(t *testing.T) {
	t.Parallel()

	tests := []userLockCommandTestCase{
		{name: "Lock_user_success", username: "user1"},
		{name: "Error_locking_invalid_user", username: "invaliduser", expectedExitCode: int(codes.NotFound)},
		{name: "Lock_user_via_provider_username", username: "user1@example.com"},
	}
	runUserLockCommand(t, "lock", tests)
}

//nolint:tparallel // The subtests share one daemon and user database.
func TestUserUnlockCommand(t *testing.T) {
	t.Parallel()

	tests := []userLockCommandTestCase{
		{name: "Unlock_user_success", username: "user1"},
		{name: "Error_unlocking_invalid_user", username: "invaliduser", expectedExitCode: int(codes.NotFound)},
		{name: "Unlock_user_via_provider_username", username: "user1@example.com"},
	}
	runUserLockCommand(t, "unlock", tests)
}

func runUserLockCommand(t *testing.T, command string, tests []userLockCommandTestCase) {
	t.Helper()

	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("multiple_users_and_groups_with_tmp_home"),
		testutils.WithCurrentUserAsRoot,
	)

	authctlEnv := []string{
		"AUTHD_SOCKET=" + daemonSocket,
		testutils.CoverDirEnv(),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, "user", command, tc.username)
			cmd.Env = authctlEnv
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}
