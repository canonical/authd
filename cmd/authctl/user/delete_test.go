package user_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

const homeBasePath = "/tmp/authd-delete-cmd-test/home"

func TestUserDeleteCommand(t *testing.T) {
	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("multiple_users_and_groups_with_tmp_home"),
		testutils.WithCurrentUserAsRoot,
	)
	t.Cleanup(func() { _ = os.RemoveAll(homeBasePath) })

	err := os.Setenv("AUTHD_SOCKET", daemonSocket)
	require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")

	tests := map[string]struct {
		args             []string
		stdin            string
		authdUnavailable bool

		createHomeDir      bool
		wantHomeDirRemoved bool

		expectedExitCode int
	}{
		"Delete_user_success": {
			args:             []string{"delete", "--yes", "user1@example.com"},
			expectedExitCode: 0,
		},

		"Confirmation_prompt_accepted_with_y": {
			args:             []string{"delete", "user2@example.com"},
			stdin:            "y\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_accepted_with_yes": {
			args:             []string{"delete", "user3@example.com"},
			stdin:            "yes\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_accepted_case_insensitively": {
			args:             []string{"delete", "user4@example.com"},
			stdin:            "YES\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_aborted_with_n": {
			args:             []string{"delete", "user1@example.com"},
			stdin:            "n\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_aborted_with_empty_input": {
			args:             []string{"delete", "user1@example.com"},
			stdin:            "\n",
			expectedExitCode: 0,
		},

		"Delete_with_remove_flag_removes_home_dir": {
			args:               []string{"delete", "--yes", "--remove", "user5@example.com"},
			createHomeDir:      true,
			wantHomeDirRemoved: true,
			expectedExitCode:   0,
		},
		"Delete_without_remove_flag_keeps_home_dir": {
			args:             []string{"delete", "--yes", "user6@example.com"},
			createHomeDir:    true,
			expectedExitCode: 0,
		},
		"Delete_with_remove_flag_succeeds_when_home_dir_does_not_exist": {
			args:               []string{"delete", "--yes", "--remove", "user7@example.com"},
			wantHomeDirRemoved: true,
			expectedExitCode:   0,
		},

		"Error_when_user_does_not_exist": {
			args:             []string{"delete", "--yes", "nonexistent@example.com"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_authd_is_unavailable": {
			args:             []string{"delete", "--yes", "user1@example.com"},
			authdUnavailable: true,
			expectedExitCode: int(codes.Unavailable),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.authdUnavailable {
				origValue := os.Getenv("AUTHD_SOCKET")
				err := os.Setenv("AUTHD_SOCKET", "/non-existent")
				require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")
				t.Cleanup(func() {
					err := os.Setenv("AUTHD_SOCKET", origValue)
					require.NoError(t, err, "Failed to restore AUTHD_SOCKET environment variable")
				})
			}

			// Extract the username from the last element of args
			username := tc.args[len(tc.args)-1]
			homeDir := filepath.Join(homeBasePath, username)

			if tc.createHomeDir {
				err := os.MkdirAll(homeDir, 0o700)
				require.NoError(t, err, "Setup: failed to create home directory %q", homeDir)
				t.Cleanup(func() { _ = os.RemoveAll(homeDir) })
			}

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			if tc.stdin != "" {
				cmd.Stdin = strings.NewReader(tc.stdin)
			}
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)

			if tc.wantHomeDirRemoved {
				require.NoDirExists(t, homeDir, "Home directory %q should have been removed", homeDir)
			} else if tc.createHomeDir {
				require.DirExists(t, homeDir, "Home directory %q should still exist", homeDir)
			}
		})
	}
}
