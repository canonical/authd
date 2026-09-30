package testutils

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// MakeReadOnly makes dest read only and restore permission on cleanup.
func MakeReadOnly(t *testing.T, dest string) func() {
	t.Helper()

	// Get current dest permissions
	fi, err := os.Stat(dest)
	require.NoError(t, err, "Cannot stat %s", dest)
	mode := fi.Mode()

	var perms fs.FileMode = 0444
	if fi.IsDir() {
		perms = 0555
	}
	err = os.Chmod(dest, perms)
	require.NoError(t, err)

	return func() {
		_, err := os.Stat(dest)
		if errors.Is(err, os.ErrNotExist) {
			return
		}

		err = os.Chmod(dest, mode)
		require.NoError(t, err)
	}
}
