package daemon

import (
	"context"
	"errors"
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
		mode               os.FileMode
		wrongOwner         bool
		symlink            bool
		parentDirOwnerOnly bool
		legacyRecoverable  bool
		wantIssue          string
	}{
		"secure":                   {mode: 0600},
		"broad_mode":               {mode: 0644, legacyRecoverable: true, wantIssue: "should be -rw-------"},
		"read_only":                {mode: 0400, legacyRecoverable: true, wantIssue: "should be -rw-------"},
		"group_writable":           {mode: 0660, wantIssue: "must not be writable by group or others"},
		"owner_unreadable":         {mode: 0200, wantIssue: "must be readable by its owner"},
		"wrong_owner":              {mode: 0600, wrongOwner: true, wantIssue: "should be owned by"},
		"symlink":                  {mode: 0600, symlink: true},
		"symlink_broad_mode":       {mode: 0644, symlink: true, legacyRecoverable: true, wantIssue: "should be -rw-------"},
		"symlink_read_only":        {mode: 0400, symlink: true, legacyRecoverable: true, wantIssue: "should be -rw-------"},
		"symlink_group_writable":   {mode: 0660, symlink: true, wantIssue: "must not be writable by group or others"},
		"symlink_owner_unreadable": {mode: 0200, symlink: true, wantIssue: "must be readable by its owner"},
		"symlink_wrong_owner":      {mode: 0600, symlink: true, wrongOwner: true, wantIssue: "should be owned by"},
		"private_symlink":          {mode: 0600, symlink: true, parentDirOwnerOnly: true},
		"private_symlink_broad_mode": {
			mode: 0644, symlink: true, parentDirOwnerOnly: true, legacyRecoverable: true, wantIssue: "should be -rw-------",
		},
		"private_symlink_group_writable": {
			mode: 0660, symlink: true, parentDirOwnerOnly: true, wantIssue: "must not be writable by group or others",
		},
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
				path = filepath.Join(t.TempDir(), "broker.conf.link")
				require.NoError(t, os.Symlink(target, path))
			}
			owner := os.Geteuid()
			if tc.wrongOwner {
				owner++
			}

			for _, legacy := range []bool{false, true} {
				warnings = nil
				warned, err := checkBrokerConfigFilePermissions(context.Background(), path, owner, legacy, tc.parentDirOwnerOnly)
				if tc.wantIssue == "" {
					require.False(t, warned)
					require.NoError(t, err)
				} else if legacy && tc.legacyRecoverable {
					require.True(t, warned)
					require.NoError(t, err)
				} else {
					require.False(t, warned)
					var permissionErr *invalidConfigPermissionsError
					require.ErrorAs(t, err, &permissionErr)
					if tc.legacyRecoverable {
						var safeModeMismatchErr *safeFileModeMismatchError
						require.ErrorAs(t, err, &safeModeMismatchErr)
						require.EqualError(t, err, fmt.Sprintf("file %q has permissions: %v (should be %v)",
							path, tc.mode, os.FileMode(0600)))
					}
					require.ErrorContains(t, err, tc.wantIssue)
					require.ErrorContains(t, err, path)
				}
				if tc.wantIssue != "" && legacy && tc.legacyRecoverable {
					require.Len(t, warnings, 1)
					require.Contains(t, warnings[0], tc.wantIssue)
					require.Contains(t, warnings[0], path)
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

func TestCheckBrokerConfigFilePermissionsInOwnerOnlyDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0777} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "broker.conf")
			require.NoError(t, os.WriteFile(path, nil, 0600))
			require.NoError(t, os.Chmod(path, mode))

			for _, legacy := range []bool{false, true} {
				warned, err := checkBrokerConfigFilePermissions(context.Background(), path, os.Geteuid(), legacy, true)
				require.False(t, warned)
				require.NoError(t, err)
			}
		})
	}
}

func TestEnsureDirWithOwnerReportsUID(t *testing.T) {
	dir := t.TempDir()
	owner := os.Geteuid() + 1

	err := ensureDirWithOwner(dir, 0700, owner)
	var permissionErr *invalidConfigPermissionsError
	require.ErrorAs(t, err, &permissionErr)
	require.ErrorContains(t, err, "owned by UID")
	require.ErrorContains(t, err, fmt.Sprintf("should be owned by %d", owner))
}

func TestPermissionValidationDoesNotIgnoreOperationalErrors(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))
	dirLink := filepath.Join(dir, "dir.link")
	require.NoError(t, os.Symlink(dir, dirLink))
	fifoLink := filepath.Join(dir, "fifo.link")
	require.NoError(t, os.Symlink(fifo, fifoLink))
	loop := filepath.Join(dir, "loop")
	require.NoError(t, os.Symlink(loop, loop))

	for _, path := range []string{"invalid\x00path", dir, fifo, dirLink, fifoLink, loop} {
		for _, legacy := range []bool{false, true} {
			warned, err := checkBrokerConfigFilePermissions(context.Background(), path, os.Geteuid(), legacy, false)
			require.False(t, warned)
			require.Error(t, err)
			var permissionErr *invalidConfigPermissionsError
			require.False(t, errors.As(err, &permissionErr))
		}
	}
}

func TestCheckTrustedDir(t *testing.T) {
	dir := t.TempDir()
	//nolint:gosec // This is a directory, which needs execute permission.
	require.NoError(t, os.Chmod(dir, 0700))
	owner := os.Geteuid()
	private, err := checkTrustedDir(dir, owner)
	require.NoError(t, err)
	require.True(t, private)
	_, err = checkTrustedDir(dir, owner+1)
	var permissionErr *invalidConfigPermissionsError
	require.ErrorAs(t, err, &permissionErr)
	require.ErrorContains(t, err, "owned by")

	//nolint:gosec // Exercise validation of a directory writable by other users.
	require.NoError(t, os.Chmod(dir, 0777))
	private, err = checkTrustedDir(dir, owner)
	require.False(t, private)
	require.ErrorAs(t, err, &permissionErr)
	require.ErrorContains(t, err, "must not be writable by group or others")
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0777), info.Mode().Perm())

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))
	_, err = checkTrustedDir(link, owner)
	require.ErrorAs(t, err, &permissionErr)
	require.ErrorContains(t, err, "must not be a symlink")
	_, err = checkTrustedDir(filepath.Join(dir, "missing"), owner)
	require.Error(t, err)
}
