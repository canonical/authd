package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

func TestCheckBrokerConfigFilePermissions(t *testing.T) {
	tests := map[string]struct {
		mode       os.FileMode
		wrongOwner bool
		symlink    bool
		wantIssue  string
	}{
		"secure":      {mode: 0600},
		"broad_mode":  {mode: 0644, wantIssue: "should be -rw-------"},
		"read_only":   {mode: 0400, wantIssue: "should be -rw-------"},
		"wrong_owner": {mode: 0600, wrongOwner: true, wantIssue: "should be owned by"},
		"symlink":     {mode: 0600, symlink: true, wantIssue: "must not be a symlink"},
	}

	var warnings []string
	log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
		if level == log.WarnLevel {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
	})
	t.Cleanup(func() { log.SetHandler(nil) })

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "broker.conf")
			require.NoError(t, os.WriteFile(path, nil, 0600))
			require.NoError(t, os.Chmod(path, tc.mode))
			target := path
			if tc.symlink {
				path += ".link"
				require.NoError(t, os.Symlink(target, path))
			}
			owner := os.Geteuid()
			if tc.wrongOwner {
				owner++
			}

			for _, legacy := range []bool{false, true} {
				warnings = nil
				err := checkBrokerConfigFilePermissions(path, owner, legacy)
				if tc.wantIssue != "" && !legacy {
					require.ErrorIs(t, err, errInvalidConfigPermissions)
					require.ErrorContains(t, err, tc.wantIssue)
					require.ErrorContains(t, err, path)
				} else {
					require.NoError(t, err)
				}
				if tc.wantIssue != "" && legacy {
					require.Len(t, warnings, 1)
					require.Contains(t, warnings[0], tc.wantIssue)
					require.Contains(t, warnings[0], path)
					require.Contains(t, warnings[0], "without changing permissions")
				} else {
					require.Empty(t, warnings)
				}
				info, err := os.Stat(target)
				require.NoError(t, err)
				require.Equal(t, tc.mode, info.Mode().Perm(), "validation must not change permissions")
			}
		})
	}
}

func TestPermissionValidationDoesNotIgnoreOperationalErrors(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))

	for _, path := range []string{"invalid\x00path", dir, fifo} {
		for _, legacy := range []bool{false, true} {
			err := checkBrokerConfigFilePermissions(path, os.Geteuid(), legacy)
			require.Error(t, err)
			require.NotErrorIs(t, err, errInvalidConfigPermissions)
		}
	}
}

func TestCheckTrustedDir(t *testing.T) {
	dir := t.TempDir()
	//nolint:gosec // This is a directory, which needs execute permission.
	require.NoError(t, os.Chmod(dir, 0700))
	owner := os.Geteuid()
	require.NoError(t, checkTrustedDir(dir, owner))
	require.ErrorIs(t, checkTrustedDir(dir, owner+1), errInvalidConfigPermissions)

	//nolint:gosec // Exercise validation of a directory writable by other users.
	require.NoError(t, os.Chmod(dir, 0777))
	err := checkTrustedDir(dir, owner)
	require.ErrorIs(t, err, errInvalidConfigPermissions)
	require.ErrorContains(t, err, "must not be writable by group or others")
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0777), info.Mode().Perm())

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))
	require.ErrorIs(t, checkTrustedDir(link, owner), errInvalidConfigPermissions)
	require.Error(t, checkTrustedDir(filepath.Join(dir, "missing"), owner))
}
