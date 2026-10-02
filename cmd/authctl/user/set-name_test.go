package user_test

import (
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestSetName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args             []string
		dbState          string
		expectedExitCode int
	}{
		// The primary group of this fixture is not named after the user, so the output reports
		// only the user rename.
		"Set_name_success": {
			args:             []string{"set-name", "user1@example.com", "user1-renamed@example.com"},
			expectedExitCode: 0,
		},
		// authd names private groups after the user, so this output additionally reports the
		// private group rename.
		"Set_name_renames_private_group": {
			args:             []string{"set-name", "user1@example.com", "user1-renamed@example.com"},
			dbState:          "one_user_with_private_group",
			expectedExitCode: 0,
		},
		// The username is lowercased before it reaches the daemon.
		"Set_name_with_uppercase_succeeds": {
			args:             []string{"set-name", "USER1@example.com", "USER1-RENAMED@example.com"},
			expectedExitCode: 0,
		},

		"Error_when_user_does_not_exist": {
			args:             []string{"set-name", "invaliduser", "newname"},
			expectedExitCode: int(codes.NotFound),
		},
		// Without a stable provider ID there is nothing to tie the new name to, so a later broker
		// login would create a second account under the old name instead of keeping the rename.
		"Error_when_user_has_no_provider_id": {
			args:             []string{"set-name", "user1@example.com", "user1-renamed@example.com"},
			dbState:          "one_user_and_group",
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_old_and_new_names_are_same": {
			args:             []string{"set-name", "user1@example.com", "user1@example.com"},
			expectedExitCode: int(codes.Unknown),
		},
		// "root" exists in /etc/passwd, so the uniqueness check against the system rejects it.
		"Error_when_new_name_already_exists_in_the_system": {
			args:             []string{"set-name", "user1@example.com", "root"},
			expectedExitCode: int(codes.Unknown),
		},
		// ':' and ',' are the field and member separators of the group file, so a name carrying
		// them has to be rejected before anything is written.
		"Error_when_new_name_contains_a_colon": {
			args:             []string{"set-name", "user1@example.com", "user1:x:0:0"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_new_name_contains_a_comma": {
			args:             []string{"set-name", "user1@example.com", "user1,user2"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_old_name_is_missing": {
			args:             []string{"set-name"},
			expectedExitCode: 1,
		},
		"Error_when_new_name_is_missing": {
			args:             []string{"set-name", "user1@example.com"},
			expectedExitCode: 1,
		},
		"Error_when_too_many_arguments": {
			args:             []string{"set-name", "user1@example.com", "newname", "extra"},
			expectedExitCode: 1,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.dbState == "" {
				tc.dbState = "one_user_and_group_with_provider_id"
			}

			// Each case needs its own daemon, because a successful rename changes
			// the user the other cases rely on.
			daemonSocket := testutils.StartAuthd(t, daemonPath,
				testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
				testutils.WithPreviousDBState(tc.dbState),
				testutils.WithCurrentUserAsRoot,
			)

			authctlEnv := []string{
				"AUTHD_SOCKET=" + daemonSocket,
				testutils.CoverDirEnv(),
			}

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			cmd.Env = authctlEnv
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}

// TestSetNameCompletion checks the shell completion of the command. Only stdout is compared,
// because cobra prints the "Completion ended with directive" message to stderr, which interleaves
// non-deterministically with stdout when both are captured in the same buffer.
func TestSetNameCompletion(t *testing.T) {
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
	}{
		// The first argument is the user to rename, so it is completed with the known users.
		"First_arg_offers_users": {args: []string{"__complete", "user", "set-name", ""}},
		// The second argument is a brand new name, so there is nothing to complete it with.
		"Second_arg_offers_nothing": {args: []string{"__complete", "user", "set-name", "user1@example.com", ""}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, tc.args...)
			cmd.Env = authctlEnv

			output := &testutils.SyncBuffer{}
			cmd.Stdout = io.MultiWriter(t.Output(), output)
			cmd.Stderr = t.Output()
			require.NoError(t, cmd.Run(), "completion should succeed")
			require.Equal(t, 0, cmd.ProcessState.ExitCode(), "Unexpected exit code")

			golden.CheckOrUpdate(t, output.String())
		})
	}
}
