package user_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/envutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/db"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestSetHomeDirCommand(t *testing.T) {
	// We can't run these tests in parallel because the daemon with the example
	// broker which we're using here uses userslocking.Z_ForTests_OverrideLocking()
	// which makes userslocking.WriteLock() return an error immediately when the lock
	// is already held - unlike the normal behavior which tries to acquire the lock
	// for 15 seconds before returning an error.

	// Use a home directory inside a temporary directory so that the success case
	// can actually move it and we can verify its contents afterwards.
	homeBaseDir := t.TempDir()
	oldHome := filepath.Join(homeBaseDir, "user1-old")
	newHome := filepath.Join(homeBaseDir, "user1-new")

	daemonSocket := startAuthdWithUserHome(t, oldHome)

	authctlEnv := []string{
		"AUTHD_SOCKET=" + daemonSocket,
		testutils.CoverDirEnv(),
	}

	tests := map[string]struct {
		args             []string
		authdUnavailable bool

		expectedExitCode int
	}{
		"Error_when_user_does_not_exist": {
			args:             []string{"set-home", "invaliduser", "/home/invaliduser"},
			expectedExitCode: int(codes.NotFound),
		},
		"Error_when_path_is_not_absolute": {
			args:             []string{"set-home", "user1@example.com", "relative/path"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_destination_already_exists": {
			args:             []string{"set-home", "user1@example.com", "/etc"},
			expectedExitCode: int(codes.Unknown),
		},
		"Error_when_authd_is_unavailable": {
			args:             []string{"set-home", "user1@example.com", newHome},
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

	// The success case has dedicated setup and verification: it populates the
	// user's current home directory with some data, runs the command, and then
	// checks that the directory and all of its contents were moved to the new
	// location. It runs after the error cases above, none of which move the
	// directory.
	t.Run("Set_user_home_dir_success", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(oldHome, "subdir"), 0o700),
			"Setup: could not create home directory")
		require.NoError(t, os.WriteFile(filepath.Join(oldHome, "marker"), []byte("top-level data"), 0o600),
			"Setup: could not create marker file")
		require.NoError(t, os.WriteFile(filepath.Join(oldHome, "subdir", "nested"), []byte("nested data"), 0o600),
			"Setup: could not create nested file")

		//nolint:gosec // G204 it's safe to use exec.Command with a variable here
		cmd := exec.Command(authctlPath, "user", "set-home", "user1@example.com", newHome)
		cmd.Env = authctlEnv

		output := &testutils.SyncBuffer{}
		cmd.Stdout = io.MultiWriter(t.Output(), output)
		cmd.Stderr = io.MultiWriter(t.Output(), output)
		require.NoError(t, cmd.Run(), "set-home should succeed")
		require.Equal(t, 0, cmd.ProcessState.ExitCode(), "Unexpected exit code")

		// The output contains the temporary (and therefore non-deterministic)
		// home base directory, so replace it with a stable placeholder before
		// comparing against the golden file.
		got := strings.ReplaceAll(output.String(), homeBaseDir, "{{HOME_BASE_DIR}}")
		golden.CheckOrUpdate(t, got)

		// The old home directory and all of its contents must have been moved to
		// the new location. Compare the moved directory against a golden file
		// tree to ensure the whole structure and every file's content survived
		// the move.
		require.NoDirExists(t, oldHome, "Old home directory should have been moved")
		golden.CheckOrUpdateFileTree(t, newHome, golden.WithSuffix("_tree"))
	})

	// When the current home directory does not exist on disk, the server
	// updates only the database record and returns a warning.  This exercises
	// the warning-printing path in runSetHomeDir.
	t.Run("Set_home_dir_with_warning_when_old_home_missing", func(t *testing.T) {
		// Use a fresh daemon whose user points at a home that does not exist.
		warnHomeBaseDir := t.TempDir()
		warnOldHome := filepath.Join(warnHomeBaseDir, "does-not-exist")
		warnNewHome := filepath.Join(warnHomeBaseDir, "new-home")
		warnSocket := startAuthdWithUserHome(t, warnOldHome)
		warnEnv := []string{
			"AUTHD_SOCKET=" + warnSocket,
			testutils.CoverDirEnv(),
		}

		//nolint:gosec // G204 it's safe to use exec.Command with a variable here
		cmd := exec.Command(authctlPath, "user", "set-home", "user1@example.com", warnNewHome)
		cmd.Env = warnEnv

		output := &testutils.SyncBuffer{}
		cmd.Stdout = io.MultiWriter(t.Output(), output)
		cmd.Stderr = io.MultiWriter(t.Output(), output)
		require.NoError(t, cmd.Run(), "set-home should succeed")
		require.Equal(t, 0, cmd.ProcessState.ExitCode(), "Unexpected exit code")

		got := strings.ReplaceAll(output.String(), warnHomeBaseDir, "{{HOME_BASE_DIR}}")
		golden.CheckOrUpdate(t, got)
	})

	// Shell completion: the first argument offers username completion, the
	// second argument offers directory completion.  Only stdout is compared
	// because cobra prints the "Completion ended with directive" message to
	// stderr, which interleaves non-deterministically with stdout when both
	// are captured in the same buffer.
	t.Run("Completion_first_arg_offers_users", func(t *testing.T) {
		cmd := exec.Command(authctlPath, "__complete", "user", "set-home", "")
		cmd.Env = authctlEnv

		output := &testutils.SyncBuffer{}
		cmd.Stdout = io.MultiWriter(t.Output(), output)
		cmd.Stderr = t.Output()
		require.NoError(t, cmd.Run(), "completion should succeed")
		require.Equal(t, 0, cmd.ProcessState.ExitCode(), "Unexpected exit code")

		golden.CheckOrUpdate(t, output.String())
	})

	t.Run("Completion_second_arg_offers_dirs", func(t *testing.T) {
		cmd := exec.Command(authctlPath, "__complete", "user", "set-home", "user1@example.com", "")
		cmd.Env = authctlEnv

		output := &testutils.SyncBuffer{}
		cmd.Stdout = io.MultiWriter(t.Output(), output)
		cmd.Stderr = t.Output()
		require.NoError(t, cmd.Run(), "completion should succeed")
		require.Equal(t, 0, cmd.ProcessState.ExitCode(), "Unexpected exit code")

		golden.CheckOrUpdate(t, output.String())
	})
}

// startAuthdWithUserHome starts a daemon whose database contains a single user
// (user1@example.com) whose home directory is set to the given path.
func startAuthdWithUserHome(t *testing.T, home string) (socketPath string) {
	t.Helper()

	// authd requires the database directory to have 0700 permissions, so we
	// create a dedicated subdirectory instead of using t.TempDir() directly
	// (which is created with more permissive permissions).
	dbDir := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.MkdirAll(dbDir, 0700), "Setup: could not create database directory")

	dbYAML := fmt.Sprintf(`users:
    - name: user1@example.com
      uid: 1111
      gid: 11111
      gecos: User1
      dir: %s
      shell: /bin/bash
      broker_id: broker-id
groups:
    - name: group1
      gid: 11111
      ugid: "12345678"
users_to_groups:
    - uid: 1111
      gid: 11111
`, home)

	require.NoError(t, db.Z_ForTests_CreateDBFromYAMLReader(strings.NewReader(dbYAML), dbDir),
		"Setup: could not create database with a temporary home directory")

	return testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithDBPath(dbDir),
		testutils.WithCurrentUserAsRoot,
	)
}
