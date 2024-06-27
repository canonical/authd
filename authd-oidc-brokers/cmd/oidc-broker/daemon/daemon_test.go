package daemon_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/oidc-broker/cmd/oidc-broker/daemon"
	"github.com/ubuntu/oidc-broker/internal/consts"
	"github.com/ubuntu/oidc-broker/internal/testutils"
)

var mockProvider *httptest.Server

func TestHelp(t *testing.T) {
	a := daemon.NewForTests(t, nil, mockProvider.URL, "--help")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoErrorf(t, err, "Run should not return an error with argument --help. Stdout: %v", getStdout())
}

func TestCompletion(t *testing.T) {
	a := daemon.NewForTests(t, nil, mockProvider.URL, "completion", "bash")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Completion should not start the daemon. Stdout: %v", getStdout())
}

func TestVersion(t *testing.T) {
	a := daemon.NewForTests(t, nil, mockProvider.URL, "version")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	out := getStdout()

	fields := strings.Fields(out)
	require.Len(t, fields, 2, "wrong number of fields in version: %s", out)

	require.Equal(t, t.Name(), fields[0], "Wrong executable name")
	require.Equal(t, consts.Version, fields[1], "Wrong version")
}

func TestNoUsageError(t *testing.T) {
	a := daemon.NewForTests(t, nil, mockProvider.URL, "completion", "bash")

	getStdout := captureStdout(t)
	err := a.Run()

	require.NoError(t, err, "Run should not return an error, stdout: %v", getStdout())
	isUsageError := a.UsageError()
	require.False(t, isUsageError, "No usage error is reported as such")
}

func TestUsageError(t *testing.T) {
	t.Parallel()

	a := daemon.NewForTests(t, nil, mockProvider.URL, "doesnotexist")

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

	a := daemon.NewForTests(t, nil, mockProvider.URL)

	requireGoroutineStarted(t, a.Quit)
	err := a.Run()
	require.Error(t, err, "Should return an error")

	require.Containsf(t, err.Error(), "grpc: the server has been stopped", "Unexpected error message")
}

func TestAppRunFailsOnComponentsCreationAndQuit(t *testing.T) {
	t.Parallel()
	const (
		// Cache errors
		dirIsFile = iota
		wrongPermission
		noParentDir
	)

	tests := map[string]struct {
		cachePathBehavior int
		configBehavior    int
	}{
		"Error on existing cache path being a file":    {cachePathBehavior: dirIsFile},
		"Error on cache path missing parent directory": {cachePathBehavior: noParentDir},
		"Error on wrong permission on cache path":      {cachePathBehavior: wrongPermission},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache")

			switch tc.cachePathBehavior {
			case dirIsFile:
				err := os.WriteFile(cachePath, []byte("file"), 0600)
				require.NoError(t, err, "Setup: could not create cache file for tests")
			case wrongPermission:
				err := os.Mkdir(cachePath, 0600)
				require.NoError(t, err, "Setup: could not create cache directory for tests")
			case noParentDir:
				cachePath = filepath.Join(tmpDir, "doesnotexist", "cache")
			}

			config := daemon.DaemonConfig{
				Verbosity: 0,
				Paths: daemon.SystemPaths{
					Cache: cachePath,
				},
			}

			a := daemon.NewForTests(t, &config, mockProvider.URL)
			err := a.Run()
			require.Error(t, err, "Run should return an error")
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

	a := daemon.NewForTests(t, nil, mockProvider.URL)

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

	a := daemon.NewForTests(t, nil, mockProvider.URL)
	require.NotNil(t, a.RootCmd(), "Returns root command")
}

func TestConfigLoad(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			Cache:      filepath.Join(tmpDir, "cache"),
		},
	}

	a, wait := startDaemon(t, &config)
	defer wait()
	defer a.Quit()

	require.Equal(t, config, a.Config(), "Config is loaded")
}

func TestAutoDetectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			Cache:      filepath.Join(tmpDir, "cache"),
		},
	}

	configPath := daemon.GenerateTestConfig(t, &config, mockProvider.URL)
	configNextToBinaryPath := filepath.Join(filepath.Dir(os.Args[0]), t.Name()+".yaml")
	err := os.Rename(configPath, configNextToBinaryPath)
	require.NoError(t, err, "Could not relocate authd configuration file in the binary directory")
	// Remove configuration next binary for other tests to not pick it up.
	defer os.Remove(configNextToBinaryPath)

	a := daemon.New(t.Name())
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.Run()
		require.NoError(t, err, "Run should exits without any error")
	}()
	a.WaitReady()
	time.Sleep(50 * time.Millisecond)

	defer wg.Wait()
	defer a.Quit()

	require.Equal(t, config, a.Config(), "Did not load configuration next to binary")
}

func TestNoConfigSetDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_DATA", tmpDir)

	a := daemon.New(t.Name()) // Use version to still run preExec to load no config but without running server
	a.SetArgs("version")

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	require.Equal(t, 0, a.Config().Verbosity, "Default Verbosity")
	require.Equal(t, filepath.Join(consts.DefaultBrokersConfPath, t.Name()), a.Config().Paths.BrokerConf, "Default broker configuration path")
	require.Equal(t, filepath.Join(tmpDir, "cache"), a.Config().Paths.Cache, "Default cache directory")
}

func TestBadConfigReturnsError(t *testing.T) {
	a := daemon.New(t.Name()) // Use version to still run preExec to load no config but without running server
	a.SetArgs("version", "--config", "/does/not/exist.yaml")

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

	a := daemon.NewForTests(t, conf, mockProvider.URL)

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
	// Start system bus mock.
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	// Start provider mock
	providerServer, cleanup := testutils.StartMockProvider("")
	defer cleanup()
	mockProvider = providerServer

	m.Run()
}
