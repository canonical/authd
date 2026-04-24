package user_test

import (
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/canonical/authd/internal/envutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestSetUIDCommand(t *testing.T) {
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

	authctlEnv := []string{
		"AUTHD_SOCKET=" + daemonSocket,
		testutils.CoverDirEnv(),
	}

	tests := map[string]struct {
		args             []string
		authdUnavailable bool

		expectedExitCode int
	}{
		"Set_user_uid_success": {
			args:             []string{"set-uid", "user1@example.com", "123456"},
			expectedExitCode: 0,
		},

		"Error_when_user_does_not_exist": {
			args:             []string{"set-uid", "invaliduser", "123456"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_uid_is_invalid": {
			args:             []string{"set-uid", "user1@example.com", "invaliduid"},
			expectedExitCode: 1,
		},
		"Error_when_uid_is_too_large": {
			args:             []string{"set-uid", "user1@example.com", strconv.Itoa(math.MaxInt32 + 1)},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_uid_is_already_taken": {
			args:             []string{"set-uid", "user1@example.com", "0"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_uid_is_negative": {
			args:             []string{"set-uid", "user1@example.com", "--", "-1000"},
			expectedExitCode: 1,
		},
		"Error_when_authd_is_unavailable": {
			args:             []string{"set-uid", "user1@example.com", "123456"},
			authdUnavailable: true,
			expectedExitCode: int(codes.Unavailable),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Copy authctlEnv to avoid modifying the original slice.
			authctlEnv := append([]string{}, authctlEnv...)
			if tc.authdUnavailable {
				var err error
				authctlEnv, err = envutils.Setenv(authctlEnv, "AUTHD_SOCKET", "/non-existent")
				require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")
			}

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			cmd.Env = authctlEnv
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
		})
	}
}
