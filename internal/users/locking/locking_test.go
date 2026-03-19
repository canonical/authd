//go:build !bubblewrap_test

package userslocking_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd/internal/fileutils"
	"github.com/ubuntu/authd/internal/testutils"
	userslocking "github.com/ubuntu/authd/internal/users/locking"
)

func TestUsersLockingInBubbleWrap(t *testing.T) {
	t.Parallel()

	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	testutils.SkipIfCannotRunBubbleWrap(t)

	// Create a binary for the bubbletea tests.
	mainTestBinary := compileTestBinary(t)

	//nolint:gosec // G204 we define the parameters here.
	testsList, err := exec.Command(mainTestBinary, "-test.list", ".*").CombinedOutput()
	require.NoError(t, err, "Setup: Checking for test: %s", testsList)
	testsListStr := strings.TrimSpace(string(testsList))
	require.NotEmpty(t, testsListStr, "Setup: test not found", testsListStr)

	lockerBinary := compileLockerBinary(t)

	for _, name := range strings.Split(testsListStr, "\n") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var testEnv []string

			// Create a temporary folder for tests.
			testDataPath := t.TempDir()
			testBinary := filepath.Join(testDataPath, filepath.Base(mainTestBinary))
			err := fileutils.CopyFile(mainTestBinary, testBinary)
			require.NoError(t, err, "Setup: Copying test binary to local test path")

			testLockerBinary := filepath.Join(testDataPath, filepath.Base(lockerBinary))
			err = fileutils.CopyFile(lockerBinary, testLockerBinary)
			require.NoError(t, err, "Setup: Copying locker binary to local test path")
			testEnv = append(testEnv, "AUTHD_TESTS_PASSWD_LOCKER_UTILITY="+testLockerBinary)

			nameRegex := fmt.Sprintf("^%s$", regexp.QuoteMeta(name))
			//nolint:gosec // G204 we define the parameters here.
			testsList, err := exec.Command(testBinary, "-test.list", nameRegex).CombinedOutput()
			require.NoError(t, err, "Setup: Checking for test: %s", testsList)
			testsListStr := strings.TrimSpace(string(testsList))
			require.NotEmpty(t, testsListStr, "Setup: %q test not found", name)
			require.Len(t, strings.Split(testsListStr, "\n"), 1,
				"Setup: Too many tests defined for %s", testsListStr)

			testCommand := []string{testBinary, "-test.run", nameRegex}
			if testutils.IsVerbose() {
				testCommand = append(testCommand, "-test.v")
			}
			if c := testutils.CoverDirForTests(); c != "" {
				testCommand = append(testCommand, fmt.Sprintf("-test.gocoverdir=%s", c))
			}
			out, err := testutils.RunInBubbleWrapWithEnv(t, testDataPath, testEnv,
				testCommand...)
			require.NoError(t, err, "Running test: %s\n%s", name, out)
		})
	}
}

func compileTestBinary(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("go", "test")
	// These are positional arguments.
	if testutils.CoverDirForTests() != "" {
		cmd.Args = append(cmd.Args, "-cover")
	}
	if testutils.IsAsan() {
		cmd.Args = append(cmd.Args, "-asan")
	}
	if testutils.IsRace() {
		cmd.Args = append(cmd.Args, "-race")
	}

	testBinary := filepath.Join(t.TempDir(), "test-binary")
	cmd.Args = append(cmd.Args, []string{
		"-tags", "bubblewrap_test", "-c", "-o", testBinary,
	}...)

	t.Logf("Compiling test binary: %s", strings.Join(cmd.Args, " "))
	compileOut, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: Cannot compile test file: %s", compileOut)

	return testBinary
}

func compileLockerBinary(t *testing.T) string {
	t.Helper()

	testLocker := filepath.Join(t.TempDir(), "test-locker")
	cmd := exec.Command("go", "build", "-C", "testlocker")
	cmd.Args = append(cmd.Args, []string{
		"-tags", "test_locker", "-o", testLocker,
	}...)

	t.Logf("Compiling locker binary: %s", strings.Join(cmd.Args, " "))
	compileOut, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: Cannot compile locker file: %s", compileOut)

	return testLocker
}

func TestUsersLockingOverride(t *testing.T) {
	// This cannot be parallel.

	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	userslocking.Z_ForTests_OverrideLocking()
	t.Cleanup(userslocking.Z_ForTests_RestoreLocking)

	err := userslocking.WriteLock()
	require.NoError(t, err, "Locking should be allowed")

	err = userslocking.WriteLock()
	require.ErrorIs(t, err, userslocking.ErrLock, "Locking again should not be allowed")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	err = userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}
