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

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/cmd/authd-oidc/daemon"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
)

var issuerURL string

func TestHelp(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "--help")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoErrorf(t, err, "Run should not return an error with argument --help. Stdout: %v", getStdout())
}

func TestCompletion(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "completion", "bash")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Completion should not start the daemon. Stdout: %v", getStdout())
}

func TestVersion(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "version")

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
	a := daemon.NewForTests(t, nil, issuerURL, "completion", "bash")

	getStdout := captureStdout(t)
	err := a.Run()

	require.NoError(t, err, "Run should not return an error, stdout: %v", getStdout())
	isUsageError := a.UsageError()
	require.False(t, isUsageError, "No usage error is reported as such")
}

func TestUsageError(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "doesnotexist")

	err := a.Run()
	require.Error(t, err, "Run should return an error, stdout: %v")
	isUsageError := a.UsageError()
	require.True(t, isUsageError, "Usage error is reported as such")
}

func TestCanQuitWhenExecute(t *testing.T) {
	a, wait := startDaemon(t, nil)
	defer wait()

	a.Quit()
}

func TestCanQuitTwice(t *testing.T) {
	a, wait := startDaemon(t, nil)

	a.Quit()
	wait()

	require.NotPanics(t, a.Quit)
}

func TestAppCanQuitWithoutExecute(t *testing.T) {
	t.Skipf("This test is skipped because it is flaky. There is no way to guarantee Quit has been called before run.")

	a := daemon.NewForTests(t, nil, issuerURL)

	requireGoroutineStarted(t, a.Quit)
	err := a.Run()
	require.Error(t, err, "Should return an error")

	require.Containsf(t, err.Error(), "grpc: the server has been stopped", "Unexpected error message")
}

func TestAppRunFailsOnComponentsCreationAndQuit(t *testing.T) {
	const (
		// DataDir errors
		dirIsFile = iota
		wrongPermission
		noParentDir
	)

	tests := map[string]struct {
		dataDirBehavior int
		configBehavior  int
	}{
		"Error_on_existing_data_dir_being_a_file":    {dataDirBehavior: dirIsFile},
		"Error_on_data_dir_missing_parent_directory": {dataDirBehavior: noParentDir},
		"Error_on_wrong_permission_on_data_dir":      {dataDirBehavior: wrongPermission},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dataDir := filepath.Join(tmpDir, "data")

			switch tc.dataDirBehavior {
			case dirIsFile:
				err := os.WriteFile(dataDir, []byte("file"), 0600)
				require.NoError(t, err, "Setup: could not create cache file for tests")
			case wrongPermission:
				err := os.Mkdir(dataDir, 0600)
				require.NoError(t, err, "Setup: could not create cache directory for tests")
			case noParentDir:
				dataDir = filepath.Join(dataDir, "doesnotexist", "data")
			}

			config := daemon.DaemonConfig{
				Verbosity: 0,
				Paths: daemon.SystemPaths{
					DataDir: dataDir,
				},
			}

			a := daemon.NewForTests(t, &config, issuerURL)
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

	a := daemon.NewForTests(t, nil, issuerURL)

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
	a := daemon.NewForTests(t, nil, issuerURL)
	require.NotNil(t, a.RootCmd(), "Returns root command")
}

func TestConfigLoad(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	a, wait := startDaemon(t, &config)
	defer wait()
	defer a.Quit()

	require.Equal(t, config, a.Config(), "Config is loaded")
}

func TestConfigHasPrecedenceOverPathsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	overrideBrokerConfPath := filepath.Join(tmpDir, "override", "via", "config", "broker.conf")
	daemon.GenerateBrokerConfig(t, overrideBrokerConfPath, issuerURL)
	a := daemon.NewForTests(t, &config, issuerURL, "--config", overrideBrokerConfPath)

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

	want := config
	want.Paths.BrokerConf = overrideBrokerConfPath
	require.Equal(t, want, a.Config(), "Config is loaded")
}

func TestAutoDetectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	configPath := daemon.GenerateTestConfig(t, &config, issuerURL)
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
	require.Equal(t, filepath.Join(tmpDir, "broker.conf"), a.Config().Paths.BrokerConf, "Default broker configuration path")
	require.Equal(t, tmpDir, a.Config().Paths.DataDir, "Default data directory")
}

func TestBadConfigReturnsError(t *testing.T) {
	a := daemon.New(t.Name()) // Use version to still run preExec to load no config but without running server
	a.SetArgs("version", "--paths-config", "/does/not/exist.yaml")

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

	a := daemon.NewForTests(t, conf, issuerURL)

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
	issuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
