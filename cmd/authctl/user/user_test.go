package user_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

var authctlPath string
var daemonPath string

func TestUserCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args             []string
		expectedExitCode int
	}{
		"Usage_message_when_no_args": {expectedExitCode: 0},
		"Help_flag":                  {args: []string{"--help"}, expectedExitCode: 0},

		"Error_on_invalid_command": {args: []string{"invalid-command"}, expectedExitCode: 1},
		"Error_on_invalid_flag":    {args: []string{"--invalid-flag"}, expectedExitCode: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
			t.Logf("Running command: %s", strings.Join(cmd.Args, " "))
			outputBytes, err := cmd.CombinedOutput()
			output := string(outputBytes)
			exitCode := cmd.ProcessState.ExitCode()

			if tc.expectedExitCode == 0 && err != nil {
				t.Logf("Command output:\n%s", output)
				t.Errorf("Expected no error, but got: %v", err)
			}

			if exitCode != tc.expectedExitCode {
				t.Logf("Command output:\n%s", output)
				t.Errorf("Expected exit code %d, got %d", tc.expectedExitCode, exitCode)
			}

			golden.CheckOrUpdate(t, output)
		})
	}
}

func TestUserLockCommand(t *testing.T) {
	t.Parallel()

	daemonSocket := testutils.StartAuthd(t, daemonPath,
		testutils.WithGroupFile(filepath.Join("testdata", "empty.group")),
		testutils.WithPreviousDBState("one_user_and_group"),
		testutils.WithCurrentUserAsRoot,
	)

	err := os.Setenv("AUTHD_SOCKET", daemonSocket)
	require.NoError(t, err, "Failed to set AUTHD_SOCKET environment variable")

	tests := map[string]struct {
		args             []string
		expectedExitCode int
	}{
		"Lock_user_success": {args: []string{"lock", "user1"}, expectedExitCode: 0},

		"Error_locking_invalid_user": {args: []string{"lock", "invaliduser"}, expectedExitCode: int(codes.NotFound)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // G204 it's safe to use exec.Command with a variable here
			cmd := exec.Command(authctlPath, append([]string{"user"}, tc.args...)...)
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

func TestMain(m *testing.M) {
	var authctlCleanup func()
	var err error
	authctlPath, authctlCleanup, err = testutils.BuildAuthctl()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup: %v\n", err)
		os.Exit(1)
	}
	defer authctlCleanup()

	var daemonCleanup func()
	daemonPath, daemonCleanup, err = testutils.BuildAuthdWithExampleBroker()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup: %v\n", err)
		os.Exit(1)
	}
	defer daemonCleanup()

	m.Run()
}
