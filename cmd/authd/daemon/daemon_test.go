package daemon_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/cmd/authd/daemon"
	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/users"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

func TestHelp(t *testing.T) {
	a := daemon.NewForTests(t, nil, "--help")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoErrorf(t, err, "Run should not return an error with argument --help. Stdout: %v", getStdout())
}

func TestVersion(t *testing.T) {
	a := daemon.NewForTests(t, nil, "version")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	out := getStdout()

	fields := strings.Fields(out)
	require.Len(t, fields, 2, "wrong number of fields in version: %s", out)

	want := "authd"

	require.Equal(t, want, fields[0], "Wrong executable name")
	require.Equal(t, consts.Version, fields[1], "Wrong version")
}

func TestNoUsageError(t *testing.T) {
	a := daemon.NewForTests(t, nil, "version")

	getStdout := captureStdout(t)
	err := a.Run()

	require.NoError(t, err, "Run should not return an error, stdout: %v", getStdout())
	isUsageError := a.UsageError()
	require.False(t, isUsageError, "No usage error is reported as such")
}

func TestUsageError(t *testing.T) {
	t.Parallel()

	a := daemon.NewForTests(t, nil, "doesnotexist")

	err := a.Run()
	require.Error(t, err, "Run should return an error, stdout: %v")
	isUsageError := a.UsageError()
	require.True(t, isUsageError, "Usage error is reported as such")
}

func TestCanQuitWhenExecute(t *testing.T) {
	t.Parallel()

	a, wait := startDaemon(t, nil)
	defer wait()

	a.Quit()
}

func TestCanQuitTwice(t *testing.T) {
	t.Parallel()

	a, wait := startDaemon(t, nil)

	a.Quit()
	wait()

	require.NotPanics(t, a.Quit)
}

func TestAppCanQuitWithoutExecute(t *testing.T) {
	t.Skipf("This test is skipped because it is flaky. There is no way to guarantee Quit has been called before run.")

	t.Parallel()

	a := daemon.NewForTests(t, nil)

	requireGoroutineStarted(t, a.Quit)
	err := a.Run()
	require.Error(t, err, "Should return an error")

	require.Containsf(t, err.Error(), "grpc: the server has been stopped", "Unexpected error message")
}

func TestAppRunFailsOnComponentsCreationAndQuit(t *testing.T) {
	t.Parallel()
	// Trigger the error with a database directory that cannot be created over an
	// existing file

	const (
		ok = iota
		dirIsFile
		hasWrongPermission
		parentDirDoesNotExists
	)

	testCases := map[string]struct {
		dbBehavior         int
		dbPathBehavior     int
		socketPathBehavior int
	}{
		"Error_on_existing_db_path_not_being_a_directory":    {dbPathBehavior: dirIsFile},
		"Error_on_existing_db_path_with_invalid_permissions": {dbPathBehavior: hasWrongPermission},
		"Error_on_missing_parent_db_directory":               {dbPathBehavior: parentDirDoesNotExists},

		"Error_on_grpc_daemon_creation_failure": {socketPathBehavior: dirIsFile},

		"Error_on_manager_creationg_failure": {dbBehavior: hasWrongPermission},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// We are using our own temporary directory for the unix socket path being too long.
			shortTmp, err := os.MkdirTemp("", "authd-tests")
			require.NoError(t, err, "Setup: could not create temporary directory")
			t.Cleanup(func() { _ = os.RemoveAll(shortTmp) })

			filePath := filepath.Join(shortTmp, "file")
			err = os.WriteFile(filePath, []byte("I'm here to break the service"), 0600)
			require.NoError(t, err, "Setup: failed to write file")

			worldAccessDir := filepath.Join(shortTmp, "opened-to-world")
			//nolint: gosec // This is a directory with invalid permission for tests.
			err = os.MkdirAll(worldAccessDir, 0777)
			require.NoError(t, err, "Setup: failed to write file")

			var config daemon.DaemonConfig
			switch tc.dbPathBehavior {
			case dirIsFile:
				config.Paths.Database = filePath
			case hasWrongPermission:
				config.Paths.Database = worldAccessDir
			case parentDirDoesNotExists:
				config.Paths.Database = filepath.Join(shortTmp, "not-exists", "db")
			}
			switch tc.socketPathBehavior {
			case dirIsFile:
				config.Paths.Socket = filepath.Join(filePath, "mysocket")
			default:
				config.Paths.Socket = filepath.Join(shortTmp, "mysocket")
			}
			switch tc.dbBehavior {
			case hasWrongPermission:
				config.Paths.Database = filepath.Join(shortTmp, "db")
				err := os.MkdirAll(config.Paths.Database, 0700)
				require.NoError(t, err, "Setup: could not create database directory")
				err = fileutils.Touch(filepath.Join(config.Paths.Database, consts.DefaultDatabaseFileName))
				require.NoError(t, err, "Setup: could not create database")
				//nolint: gosec // This is a file with invalid permission for tests.
				err = os.Chmod(filepath.Join(config.Paths.Database, consts.DefaultDatabaseFileName), 0666)
				require.NoError(t, err, "Setup: could not set file permissions")
			}

			a := daemon.NewForTests(t, &config)

			err = a.Run()
			require.Error(t, err, "Run should exit with an error")
			a.Quit()
		})
	}
}

func TestAppCanSigHupWhenExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a, wait := startDaemon(t, nil)

	defer wait()
	defer a.Quit()

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppCanSigHupAfterExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a, wait := startDaemon(t, nil)
	a.Quit()
	wait()

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppCanSigHupWithoutExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a := daemon.NewForTests(t, nil)

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppGetRootCmd(t *testing.T) {
	t.Parallel()

	a := daemon.NewForTests(t, nil)
	require.NotNil(t, a.RootCmd(), "Returns root command")
}

func TestConfigLoad(t *testing.T) {
	wantUsersConfig := &users.Config{UIDMin: 10001, UIDMax: 19000, GIDMax: 9999}
	customizedSocketPath := filepath.Join(t.TempDir(), "mysocket")
	var config daemon.DaemonConfig
	config.Verbosity = 1
	config.Paths.Socket = customizedSocketPath
	config.UsersConfig = wantUsersConfig

	a, wait := startDaemon(t, &config)
	defer wait()
	defer a.Quit()

	_, err := os.Stat(customizedSocketPath)
	require.NoError(t, err, "Socket should exist")
	require.Equal(t, 1, a.Config().Verbosity, "Verbosity is set from config")
	require.Equal(t, wantUsersConfig, a.Config().UsersConfig, "Unexpected users config")
}

func TestAutoDetectConfig(t *testing.T) {
	var config daemon.DaemonConfig
	config.Verbosity = 1

	configPath := daemon.GenerateTestConfig(t, &config)
	configNextToBinaryPath := filepath.Join(filepath.Dir(os.Args[0]), "authd.yaml")
	err := os.Rename(configPath, configNextToBinaryPath)
	require.NoError(t, err, "Could not relocate authd configuration file in the binary directory")
	// Remove configuration next binary for other tests to not pick it up.
	defer os.Remove(configNextToBinaryPath)

	// Avoid that an existing config file in the config directory is picked up.
	opt := daemon.WithConfigDir(t.TempDir())

	a := daemon.New(opt)
	a.SetArgs("--check-config")

	err = a.Run()
	require.NoError(t, err, "Run should not return an error")

	require.Equal(t, 1, a.Config().Verbosity, "Verbosity is set from config")
	require.Equal(t, &users.DefaultConfig, a.Config().UsersConfig, "Default Users Config")
}

func TestNoConfigSetDefaults(t *testing.T) {
	a := daemon.New()

	a.SetArgs("--check-config")

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	require.Equal(t, 0, a.Config().Verbosity, "Default Verbosity")
	require.Equal(t, consts.DefaultBrokersConfPath, a.Config().Paths.BrokersConf, "Default brokers configuration path")
	require.Equal(t, consts.DefaultDatabaseDir, a.Config().Paths.Database, "Default database directory")
	require.Equal(t, &users.DefaultConfig, a.Config().UsersConfig, "Default Users Config")
	require.Equal(t, "", a.Config().Paths.Socket, "No socket address as default")
}

func TestBadConfigReturnsError(t *testing.T) {
	a := daemon.New()
	a.SetArgs("--check-config", "--config", "/does/not/exist.yaml")

	err := a.Run()
	require.Error(t, err, "Run should return an error on config file")
}

// requireGoroutineStarted starts a goroutine and blocks until it has been launched.
func requireGoroutineStarted(t *testing.T, f func()) {
	t.Helper()

	launched := make(chan struct{})

	go func() {
		close(launched)
		f()
	}()

	<-launched
}

// startDaemon prepares and starts the daemon in the background. The done function should be called
// to wait for the daemon to stop.
func startDaemon(t *testing.T, conf *daemon.DaemonConfig) (app *daemon.App, done func()) {
	t.Helper()

	a := daemon.NewForTests(t, conf)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.Run()
		require.NoError(t, err, "Run should exits without any error")
	}()
	a.WaitReady()
	time.Sleep(50 * time.Millisecond)

	return a, func() {
		wg.Wait()
	}
}

// captureStdout capture current process stdout and returns a function to get the captured buffer.
func captureStdout(t *testing.T) func() string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	orig := os.Stdout
	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = orig
		w.Close()
	})

	var out bytes.Buffer
	errch := make(chan error)
	go func() {
		_, err = io.Copy(&out, r)
		errch <- err
		close(errch)
	}()

	return func() string {
		w.Close()
		w = nil
		require.NoError(t, <-errch, "Couldn't copy stdout to buffer")

		return out.String()
	}
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	// Start system bus mock.
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	userslocking.Z_ForTests_OverrideLocking()
	defer userslocking.Z_ForTests_RestoreLocking()

	m.Run()
}
