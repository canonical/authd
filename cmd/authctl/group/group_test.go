package group_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/canonical/authd/internal/testutils"
)

var authctlPath string
var daemonPath string

func TestGroupCommand(t *testing.T) {
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
			cmd := exec.Command(authctlPath, append([]string{"group"}, tc.args...)...)
			testutils.CheckCommand(t, cmd, tc.expectedExitCode)
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
