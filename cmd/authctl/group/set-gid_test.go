package group_test

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestSetGIDCommand(t *testing.T) {
	// We can't run these tests in parallel because the daemon with the example
	// broker which we're using here uses userslocking.Z_ForTests_OverrideLocking()
	// which makes userslocking.WriteLock() return an error immediately when the lock
	// is already held - unlike the normal behavior which tries to acquire the lock
	// for 15 seconds before returning an error.

	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("one_user_and_group"),
		testutils.WithCurrentUserAsRoot,
	)

	err := os.Setenv("AUTHD_SOCKET", daemonSocket)
	require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")

	tests := map[string]struct {
		args             []string
		authdUnavailable bool

		expectedExitCode int
	}{
		"Set_group_gid_success": {
			args:             []string{"set-gid", "group1", "123456"},
			expectedExitCode: 0,
		},

		"Error_when_group_does_not_exist": {
			args:             []string{"set-gid", "invalidgroup", "123456"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_gid_is_invalid": {
			args:             []string{"set-gid", "group1", "invalidgid"},
			expectedExitCode: 1,
		},
		"Error_when_gid_is_too_large": {
			args:             []string{"set-gid", "group1", strconv.Itoa(math.MaxInt32 + 1)},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_gid_is_already_taken": {
			args:             []string{"set-gid", "group1", "0"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_gid_is_negative": {
			args:             []string{"set-gid", "group1", "-1000"},
			expectedExitCode: 1,
		},
		"Error_when_authd_is_unavailable": {
			args:             []string{"set-gid", "group1", "123456"},
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

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"group"}, tc.args...)...)
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
