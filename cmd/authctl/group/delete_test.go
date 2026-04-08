package group_test

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

func TestGroupDeleteCommand(t *testing.T) {
	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("multiple_users_and_groups"),
		testutils.WithCurrentUserAsRoot,
	)

	err := os.Setenv("AUTHD_SOCKET", daemonSocket)
	require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")

	tests := map[string]struct {
		args             []string
		stdin            string
		authdUnavailable bool

		expectedExitCode int
	}{
		"Delete_group_success": {
			args:             []string{"delete", "--yes", "group3-nonprimary"},
			expectedExitCode: 0,
		},

		"Confirmation_prompt_accepted_with_y": {
			args:             []string{"delete", "group4-nonprimary"},
			stdin:            "y\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_accepted_with_yes": {
			args:             []string{"delete", "group5-nonprimary"},
			stdin:            "yes\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_accepted_case_insensitively": {
			args:             []string{"delete", "group6-nonprimary"},
			stdin:            "YES\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_aborted_with_n": {
			args:             []string{"delete", "group1"},
			stdin:            "n\n",
			expectedExitCode: 0,
		},
		"Confirmation_prompt_aborted_with_empty_input": {
			args:             []string{"delete", "group1"},
			stdin:            "\n",
			expectedExitCode: 0,
		},

		"Error_when_group_does_not_exist": {
			args:             []string{"delete", "--yes", "nonexistent"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_authd_is_unavailable": {
			args:             []string{"delete", "--yes", "group1"},
			authdUnavailable: true,
			expectedExitCode: int(codes.Unavailable),
		},
		"Error_when_group_is_primary_group_of_a_user": {
			args:             []string{"delete", "--yes", "group1"},
			expectedExitCode: int(codes.FailedPrecondition),
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

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"group"}, tc.args...)...)
			if tc.stdin != "" {
				cmd.Stdin = strings.NewReader(tc.stdin)
			}
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}
