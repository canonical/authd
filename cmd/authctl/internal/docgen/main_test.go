package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManPage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "authctl.1")
	//nolint:gosec // G204: the output path is inside the test's temporary directory.
	cmd := exec.Command("go", "run", ".", "-format", "man", "-out", path)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	want, err := os.ReadFile("../../../../man/authctl.1")
	require.NoError(t, err)
	require.Equal(t, string(stripDate(want)), string(stripDate(got)))
}
