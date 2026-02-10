package userslocking_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/internal/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/stretchr/testify/require"
)

var tempDir string
var compileLockerBinaryOnce sync.Once
var lockerBinaryPath string

func TestLockAndWriteUnlock(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	groupFile := filepath.Join("/etc", "group")
	newGroupContents := "testgroup:x:1001:testuser"
	//nolint:gosec // G306 The group file is expected to have permissions 0644
	err := os.WriteFile(groupFile, []byte("root:x:0:\n"+newGroupContents), 0644)
	require.NoError(t, err, "Writing group file")

	// Try using gpasswd to modify the group file. This should succeed, because
	// the group file is not locked.
	output, err := runGPasswd(t, "--add", "root", "testgroup")
	require.NoError(t, err, "Output: %s", output)

	// Lock the group file
	err = userslocking.WriteLock()
	require.NoError(t, err, "Locking database")

	output, err = runCmd(t, "getent", "group", "testgroup")
	require.NoError(t, err, "Output: %s", output)
	require.Equal(t, output, newGroupContents+",root", "Group not found")

	// Try using gpasswd to modify the group file. This should fail, because
	// the group file is locked.
	output, err = runGPasswd(t, "--delete", "root", "testgroup")
	require.Error(t, err, output)
	require.Contains(t, output, "gpasswd: cannot lock /etc/group")

	// Reading is allowed when locked.
	output, err = runCmd(t, "getent", "group", "testgroup")
	require.NoError(t, err, "Output: %s", output)
	require.Equal(t, output, newGroupContents+",root", "Group not found")

	// Unlock the group file
	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking database")

	// Try using gpasswd to modify the group file again. This should succeed,
	// because the group file is unlocked.
	output, err = runGPasswd(t, "--delete", "root", "testgroup")
	require.NoError(t, err, "Output: %s", output)

	output, err = runCmd(t, "getent", "group", "testgroup")
	require.NoError(t, err, "Output: %s", output)
	require.Equal(t, output, newGroupContents, "Group not found")
}

func TestReadWhileLocked(t *testing.T) {
	t.Parallel()

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	groupFile := filepath.Join("/etc", "group")
	groupContents := `root:x:0:
testgroup:x:1001:testuser`

	//nolint:gosec // G306 The group file is expected to have permissions 0644
	err := os.WriteFile(groupFile, []byte(groupContents), 0644)
	require.NoError(t, err, "Writing group file")

	err = userslocking.WriteLock()
	require.NoError(t, err, "Locking once it is allowed")
	t.Cleanup(func() {
		err := userslocking.WriteUnlock()
		require.NoError(t, err, "Unlocking should be allowed")
	})

	output, err := runCmd(t, "getent", "group", "root", "testgroup")
	require.NoError(t, err, "Reading should be allowed")
	require.Equal(t, groupContents, output)
}

func TestLockAndLockAgainGroupFileOverridden(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	userslocking.Z_ForTests_OverrideLocking()
	restoreFunc := userslocking.Z_ForTests_RestoreLocking
	t.Cleanup(func() { restoreFunc() })

	err := userslocking.WriteLock()
	require.NoError(t, err, "Locking once it is allowed")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")

	// Ensure restoring works as expected.
	restoreFunc = func() {}
	userslocking.Z_ForTests_RestoreLocking()

	groupFile := filepath.Join("/etc", "group")
	groupContents := "testgroup:x:1001:testuser"

	//nolint:gosec // G306 The group file is expected to have permissions 0644
	err = os.WriteFile(groupFile, []byte(groupContents), 0644)
	require.NoError(t, err, "Writing group file")

	err = userslocking.WriteLock()
	require.NoError(t, err, "Locking once it is allowed")
	t.Cleanup(func() {
		// Ignore the error here, as it's expected to return an error if the
		// userslocking.WriteUnlock further below is called first.
		_ = userslocking.WriteUnlock()
	})

	gPasswdExited := make(chan error)
	go func() {
		_, err := runGPasswd(t, "--add", "root", "testgroup")
		gPasswdExited <- err
	}()

	select {
	case <-time.After(3 * time.Second):
		// If we're time-outing: it's fine, it means we were locked!
	case err := <-gPasswdExited:
		require.ErrorIs(t, err, userslocking.ErrLock, "GPasswd should fail")
	}

	require.NoError(t, userslocking.WriteUnlock())
	<-gPasswdExited
}

func TestUnlockUnlockedOverridden(t *testing.T) {
	t.Parallel()

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	err := userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}

func TestUnlockUnlocked(t *testing.T) {
	t.Parallel()

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	err := userslocking.WriteUnlock()
	require.ErrorIs(t, err, userslocking.ErrUnlock, "Unlocking unlocked should not be allowed")
}

func TestLockAndLockAgainGroupFile(t *testing.T) {
	t.Parallel()

	if !testutils.RunningInBubblewrap() {
		testutils.RunTestInBubbleWrap(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	err := userslocking.WriteLock()
	require.NoError(t, err, "Locking once it is allowed")

	err = userslocking.WriteLock()
	require.ErrorIs(t, err, userslocking.ErrLock, "Locking again should not be allowed")

	err = userslocking.WriteUnlock()
	require.NoError(t, err, "Unlocking should be allowed")
}

func TestLockingLockedDatabase(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	if !testutils.RunningInBubblewrap() {
		testInBubbleWrapWithLockerBinary(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	testLockerUtility := os.Getenv("AUTHD_TESTS_PASSWD_LOCKER_UTILITY")
	require.NotEmpty(t, testLockerUtility, "Setup: Locker utility unset")

	groupFile := filepath.Join("/etc", "group")
	groupContents := "testgroup:x:1001:testuser"

	//nolint:gosec // G306 The group file is expected to have permissions 0644
	err := os.WriteFile(groupFile, []byte(groupContents), 0644)
	require.NoError(t, err, "Writing group file")

	ctx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // G204 It's fine to pass variables to exec.Command here'
	cmd := exec.CommandContext(ctx, testLockerUtility)
	t.Logf("Running command: %s", cmd.Args)

	err = cmd.Start()
	require.NoError(t, err, "Setup: Locker utility should start")
	lockerProcess := cmd.Process

	lockerExited := make(chan error)
	writeLockExited := make(chan error)
	t.Cleanup(func() {
		cancel()
		_ = syscall.Kill(lockerProcess.Pid, syscall.SIGKILL)
		require.Error(t, <-lockerExited, "Stopping locking process")
		require.NoError(t, <-writeLockExited, "Final locking")
		require.NoError(t, userslocking.WriteUnlock(), "Final unlocking")
	})

	go func() {
		lockerExited <- cmd.Wait()
	}()

	select {
	case <-time.After(1 * time.Second):
		t.Cleanup(func() { _ = lockerProcess.Kill() })
		// If we're time-outing: it's fine, it means the test-locker process is running
	case err := <-lockerExited:
		require.NoError(t, err, "test locker should not have failed")
	}

	gPasswdExited := make(chan error)
	go func() {
		_, err := runGPasswd(t, "--add", "root", "testgroup")
		gPasswdExited <- err
	}()

	select {
	case <-time.After(3 * time.Second):
		// If we're time-outing: it's fine, it means we were locked!
	case err := <-gPasswdExited:
		require.ErrorIs(t, err, userslocking.ErrLock, "GPasswd should fail")
	}

	go func() {
		writeLockExited <- userslocking.WriteLock()
	}()

	select {
	case <-time.After(1 * time.Second):
		// If we're time-outing: it's fine, it means the test-locker process is
		// still running and holding the lock.
	case err := <-writeLockExited:
		require.ErrorIs(t, err, userslocking.ErrLock, "Locking should not work")
	}
}

func TestLockingLockedDatabaseFailsAfterTimeout(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	if !testutils.RunningInBubblewrap() {
		testInBubbleWrapWithLockerBinary(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	userslocking.Z_ForTests_SetMaxWaitTime(t, 2*time.Second)

	testLockerUtility := os.Getenv("AUTHD_TESTS_PASSWD_LOCKER_UTILITY")
	require.NotEmpty(t, testLockerUtility, "Setup: Locker utility unset")

	ctx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // G204 It's fine to pass variables to exec.Command here'
	cmd := exec.CommandContext(ctx, testLockerUtility)
	t.Logf("Running command: %s", cmd.Args)

	err := cmd.Start()
	require.NoError(t, err, "Setup: Locker utility should start")
	lockerProcess := cmd.Process

	lockerExited := make(chan error)
	t.Cleanup(func() {
		cancel()
		_ = syscall.Kill(lockerProcess.Pid, syscall.SIGKILL)
		require.Error(t, <-lockerExited, "Stopping locking process")
	})

	go func() {
		lockerExited <- cmd.Wait()
	}()

	select {
	case <-time.After(1 * time.Second):
		t.Cleanup(func() { _ = lockerProcess.Kill() })
		// If we're time-outing: it's fine, it means the test-locker process is running
	case err := <-lockerExited:
		require.NoError(t, err, "test locker should not have failed")
	}

	t.Log("Waiting for lock")
	writeLockExited := make(chan error)
	go func() {
		writeLockExited <- userslocking.WriteLock()
	}()

	err = <-writeLockExited
	t.Log("Done waiting for lock!")
	require.ErrorIs(t, err, userslocking.ErrLock)
	require.ErrorIs(t, err, userslocking.ErrLockTimeout)
}

func TestLockingLockedDatabaseWorksAfterUnlock(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	if !testutils.RunningInBubblewrap() {
		testInBubbleWrapWithLockerBinary(t)
		return
	}

	require.Zero(t, os.Geteuid(), "Not root")

	testLockerUtility := os.Getenv("AUTHD_TESTS_PASSWD_LOCKER_UTILITY")
	require.NotEmpty(t, testLockerUtility, "Setup: Locker utility unset")

	userslocking.Z_ForTests_SetMaxWaitTime(t, 3*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	//nolint:gosec // G204 It's fine to pass variables to exec.Command here'
	lockerCmd := exec.CommandContext(ctx, testLockerUtility)
	t.Logf("Running command: %s", lockerCmd.Args)

	err := lockerCmd.Start()
	require.NoError(t, err, "Setup: Locker utility should start")
	lockerProcess := lockerCmd.Process

	lockerExited := make(chan error)
	go func() {
		lockerExited <- lockerCmd.Wait()
	}()

	select {
	case <-time.After(500 * time.Millisecond):
		// If we're time-outing: it's fine, it means the test-locker process is
		// still running and holding the lock.
		t.Cleanup(func() { _ = lockerProcess.Kill() })
	case err := <-lockerExited:
		require.NoError(t, err, "test locker should not have failed")
	}

	writeLockExited := make(chan error)
	go func() {
		writeLockExited <- userslocking.WriteLock()
	}()

	select {
	case <-time.After(1 * time.Second):
		// If we're time-outing: it's fine, it means the test-locker process is
		// still running and holding the lock.
	case err := <-writeLockExited:
		require.ErrorIs(t, err, userslocking.ErrLock, "Locking should not work")
	}

	writeUnLockExited := make(chan error)
	go func() {
		writeUnLockExited <- userslocking.WriteUnlock()
	}()

	select {
	case <-time.After(500 * time.Millisecond):
		// If we're time-outing: it's fine, it means the test-locker process is
		// still running and holding the lock.
	case err := <-writeUnLockExited:
		require.ErrorIs(t, err, userslocking.ErrUnlock, "Locking should not work")
	}

	t.Log("Killing locking process")
	cancel()
	_ = syscall.Kill(lockerProcess.Pid, syscall.SIGKILL)
	// Do not wait for the locker being exited yet, so that we can ensure that
	// our function call wait is over.

	t.Log("We should get the lock now!")
	err = <-writeLockExited
	require.NoError(t, err, "We should have the lock now")

	t.Log("Ensure locking process has been stopped!")
	err = <-lockerExited
	require.Error(t, err, "Locker should exit with failure")

	err = <-writeUnLockExited
	require.NoError(t, err, "We should be able to unlock now")
}

func runCmd(t *testing.T, command string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")

	t.Logf("Running command: %s", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runGPasswd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	return runCmd(t, "gpasswd", args...)
}

func compileLockerBinary(t *testing.T, tempDir string) {
	t.Helper()

	lockerBinaryPath = filepath.Join(tempDir, "test-locker")
	cmd := exec.Command("go", "build", "-C", "testlocker")
	cmd.Args = append(cmd.Args, []string{
		"-tags", "test_locker", "-o", lockerBinaryPath,
	}...)

	t.Logf("Compiling locker binary: %s", strings.Join(cmd.Args, " "))
	compileOut, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: Cannot compile locker file: %s", compileOut)
}

func testInBubbleWrapWithLockerBinary(t *testing.T) {
	t.Helper()

	testutils.RequireBubblewrap(t)

	compileLockerBinaryOnce.Do(func() {
		compileLockerBinary(t, tempDir)
	})

	testutils.RunTestInBubbleWrap(t,
		"--ro-bind", lockerBinaryPath, lockerBinaryPath,
		"--setenv", "AUTHD_TESTS_PASSWD_LOCKER_UTILITY", lockerBinaryPath,
	)
}

func TestMain(m *testing.M) {
	if testutils.RunningInBubblewrap() {
		m.Run()
		return
	}

	var err error
	tempDir, err = os.MkdirTemp("", "authd-test-*")
	if err != nil {
		panic(err)
	}
	if v := os.Getenv("SKIP_CLEANUP"); v == "" {
		defer func() { _ = os.RemoveAll(tempDir) }()
	}

	m.Run()
}
