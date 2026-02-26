package testutils

import (
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
)

// CheckCommand runs the given command and:
// * Checks that it exits with the expected exit code.
// * Checks that the output matches the golden file.
func CheckCommand(t *testing.T, cmd *exec.Cmd, expectedExitCode int) {
	t.Helper()
	basename := filepath.Base(cmd.Args[0])

	output := &SyncBuffer{}
	cmd.Stdout = io.MultiWriter(t.Output(), output)
	cmd.Stderr = io.MultiWriter(t.Output(), output)
	testlog.LogCommand(t, "Running "+basename, cmd)
	err := cmd.Run()
	exitcode := cmd.ProcessState.ExitCode()
	testlog.LogEndSeparatorf(t, basename+" finished (exit code %d)", exitcode)

	if expectedExitCode == 0 {
		require.NoError(t, err, basename+" failed unexpectedly")
	}

	require.Equal(t, expectedExitCode, exitcode, "Unexpected exit code")

	golden.CheckOrUpdate(t, output.String())
}
