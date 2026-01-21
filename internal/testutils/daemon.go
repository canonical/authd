package testutils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/internal/grpcutils"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/localentries"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type daemonOptions struct {
	dbPath                   string
	existentDB               string
	socketPath               string
	pidFile                  string
	saveOutputAsTestArtifact bool
	shared                   bool
	env                      []string
}

// DaemonOption represents an optional function that can be used to override some of the daemon default values.
type DaemonOption func(*daemonOptions)

// WithDBPath overrides the default database path of the daemon.
func WithDBPath(path string) DaemonOption {
	return func(o *daemonOptions) {
		o.dbPath = path
	}
}

// WithPreviousDBState initializes the database of the daemon with a preexistent database.
func WithPreviousDBState(db string) DaemonOption {
	return func(o *daemonOptions) {
		o.existentDB = db
	}
}

// WithSocketPath overrides the default socket path of the daemon.
func WithSocketPath(path string) DaemonOption {
	return func(o *daemonOptions) {
		o.socketPath = path
	}
}

// WithEnvironment overrides the default environment of the daemon.
func WithEnvironment(env ...string) DaemonOption {
	return func(o *daemonOptions) {
		o.env = append(o.env, env...)
	}
}

// WithPidFile sets the path where the process pid will be saved while running.
// The pidFile is also special because when it gets removed, authd is stopped.
func WithPidFile(pidFile string) DaemonOption {
	return func(o *daemonOptions) {
		o.pidFile = pidFile
	}
}

// WithOutputAsTestArtifact saves the daemon output to a test artifact.
func WithOutputAsTestArtifact() DaemonOption {
	return func(o *daemonOptions) {
		o.saveOutputAsTestArtifact = true
	}
}

// WithSharedDaemon sets whether the daemon is shared between tests.
func WithSharedDaemon(shared bool) DaemonOption {
	return func(o *daemonOptions) {
		o.shared = shared
	}
}

// WithHomeBaseDir sets the base path for the user home directories.
func WithHomeBaseDir(baseDir string) DaemonOption {
	return func(o *daemonOptions) {
		o.env = append(o.env, fmt.Sprintf("AUTHD_EXAMPLE_BROKER_HOME_BASE_DIR=%s", baseDir))
	}
}

// WithGroupFile sets the group file.
func WithGroupFile(groupFile string) DaemonOption {
	return func(o *daemonOptions) {
		o.env = slices.DeleteFunc(o.env, func(e string) bool {
			return strings.HasPrefix(e, localentries.Z_ForTests_GroupFilePathEnv+"=")
		})
		o.env = append(o.env, fmt.Sprintf("%s=%s", localentries.Z_ForTests_GroupFilePathEnv, groupFile))
	}
}

// WithGroupFileOutput sets the group output file.
func WithGroupFileOutput(groupFile string) DaemonOption {
	return func(o *daemonOptions) {
		o.env = slices.DeleteFunc(o.env, func(e string) bool {
			return strings.HasPrefix(e, localentries.Z_ForTests_GroupFileOutputPathEnv+"=")
		})
		o.env = append(o.env, fmt.Sprintf("%s=%s", localentries.Z_ForTests_GroupFileOutputPathEnv, groupFile))
	}
}

// WithCurrentUserAsRoot configures authd to accept the current user as root when checking permissions.
// This is useful for integration tests where the current user is not root, but we want to
// test the behavior as if it were root.
var WithCurrentUserAsRoot DaemonOption = func(o *daemonOptions) {
	o.env = append(o.env, "AUTHD_INTEGRATIONTESTS_CURRENT_USER_AS_ROOT=1")
}

// StartAuthd starts authd in a separate process and returns the socket path.
func StartAuthd(t *testing.T, execPath string, args ...DaemonOption) (socketPath string) {
	t.Helper()

	socketPath, cancelFunc := StartAuthdWithCancel(t, execPath, args...)
	t.Cleanup(cancelFunc)
	return socketPath
}

// StartAuthdWithCancel starts authd in a separate process and returns the socket path and a cancel function.
func StartAuthdWithCancel(t *testing.T, execPath string, args ...DaemonOption) (socketPath string, cancelFunc func()) {
	t.Helper()

	opts := &daemonOptions{}
	for _, opt := range args {
		opt(opts)
	}

	// Socket name has a maximum size, so we can't use t.TempDir() directly.
	tempDir, err := os.MkdirTemp("", "authd-daemon4tests")
	require.NoError(t, err, "Setup: failed to create temp dir for tests")

	if opts.dbPath == "" {
		opts.dbPath = filepath.Join(tempDir, "db")
	}

	if opts.existentDB != "" {
		require.NoError(t, os.MkdirAll(opts.dbPath, 0700), "Setup: failed to create database dir")
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", opts.existentDB+".db.yaml"), opts.dbPath)
		require.NoError(t, err, "Setup: could not create database from testdata")
	}

	if opts.socketPath == "" {
		opts.socketPath = filepath.Join(tempDir, "authd.socket")
	}

	config := fmt.Sprintf(`
verbosity: 2
paths:
  database: %s
  socket: %s
`, opts.dbPath, opts.socketPath)

	configPath := filepath.Join(tempDir, "testconfig.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600), "Setup: failed to create config file for tests")

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelFunc = func() {
		t.Log("Stopping authd...")
		cancel(nil)
		<-stopped
	}

	// #nosec:G204 - we control the command arguments in tests
	cmd := exec.CommandContext(ctx, execPath, "-c", configPath)
	opts.env = append(opts.env, fmt.Sprintf("AUTHD_EXAMPLE_BROKER_SLEEP_MULTIPLIER=%f", SleepMultiplier()))
	cmd.Env = append(AppendCovEnv(opts.env), MinimalPathEnv)

	// This is the function that is called by CommandContext when the context is cancelled.
	cmd.Cancel = func() error {
		defer os.RemoveAll(tempDir)
		return cmd.Process.Signal(os.Signal(syscall.SIGTERM))
	}

	// Start authd
	start := time.Now()
	processPid := make(chan int)
	go func() {
		defer close(stopped)

		// For shared authd instances, we can't redirect the output to the test log,
		// because the instance could still be running after the test finishes.
		if opts.shared {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stdout = t.Output()
			cmd.Stderr = t.Output()
		}

		if opts.saveOutputAsTestArtifact {
			authdOutput := &SyncBuffer{}
			cmd.Stdout = io.MultiWriter(cmd.Stdout, authdOutput)
			cmd.Stderr = io.MultiWriter(cmd.Stderr, authdOutput)
			MaybeSaveBufferAsArtifactOnCleanup(t, authdOutput, "authd.log")
		}

		testlog.LogCommand(t, "Starting authd", cmd)
		err := cmd.Start()
		require.NoError(t, err, "Setup: authd failed to start")
		if opts.pidFile != "" {
			processPid <- cmd.Process.Pid
		}

		// For shared authd instances, stop using `t` beyond this point,
		// because the test which it refers to might have already finished.
		t := t
		logger := t.Logf
		errorIs := func(err, target error, format string, args ...any) {
			require.ErrorIs(t, err, target, fmt.Sprintf(format, args...))
		}
		if opts.shared {
			// Unset the testing value, since it's wrong to use it from!
			t = nil
			logger = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
			errorIs = func(err, target error, format string, args ...any) {
				if errors.Is(err, target) {
					return
				}
				panic(fmt.Sprintf("Error %v is not matching %v: %s", err, target, fmt.Sprintf(format, args...)))
			}
		}

		err = cmd.Wait()
		errorIs(err, context.Canceled, "Setup: authd stopped unexpectedly")
		if opts.pidFile != "" {
			defer cancel(nil)
			if err := os.Remove(opts.pidFile); err != nil {
				logger("TearDown: failed to remove pid file %q: %v", opts.pidFile, err)
			}
		}
		logger("authd exited (%v)", err)
	}()

	conn, err := grpc.NewClient("unix://"+opts.socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(errmessages.FormatErrorMessage))
	require.NoError(t, err, "Setup: could not connect to authd on %s", opts.socketPath)
	defer conn.Close()

	// Block until authd has started and is ready to accept connections.
	err = grpcutils.WaitForConnection(ctx, conn, time.Second*30)
	require.NoError(t, err, "Setup: timeout waiting for authd to start")
	duration := time.Since(start)
	testlog.LogEndSeparatorf(t, "authd started in %.3fs", duration.Seconds())

	if opts.pidFile != "" {
		err := os.WriteFile(opts.pidFile, []byte(fmt.Sprint(<-processPid)), 0600)
		require.NoError(t, err, "Setup: cannot create PID file")

		// In case the pid file gets removed externally, close authd!
		// fsnotify watcher doesn't seem to work here, so let's go manual.
		go func() {
			for {
				f, err := os.Open(opts.pidFile)
				if err != nil {
					cancel(err)
					return
				}
				defer f.Close()
				<-time.After(time.Millisecond * 200)
			}
		}()
	}

	return opts.socketPath, cancelFunc
}

// BuildAuthdWithExampleBroker builds the authd executable and returns the binary path.
func BuildAuthdWithExampleBroker() (execPath string, cleanup func(), err error) {
	projectRoot := ProjectRoot()

	tempDir, err := os.MkdirTemp("", "authd-tests-daemon")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(tempDir) }

	execPath = filepath.Join(tempDir, "authd")
	cmd := exec.Command("go", "build")
	cmd.Dir = projectRoot
	cmd.Args = append(cmd.Args, GoBuildFlags()...)
	cmd.Args = append(cmd.Args, "-gcflags=all=-N -l")
	cmd.Args = append(cmd.Args, "-tags=withexamplebroker,integrationtests")
	cmd.Args = append(cmd.Args, "-o", execPath, "./cmd/authd")

	fmt.Fprintln(os.Stderr, "Running command:", cmd.String())
	if err := testlog.RunWithTiming(nil, "Building authd", cmd); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to build authd: %v", err)
	}

	return execPath, cleanup, err
}
