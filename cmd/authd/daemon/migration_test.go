package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/stretchr/testify/require"
)

func TestMaybeMigrateOldDBDir(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		oldDirExists     bool
		newDirExists     bool
		oldDirUnreadable bool
		newDirUnreadable bool
		wantDbInNewDir   bool

		wantedErr        error
		wantOldDirExists bool
		wantNewDirExists bool
	}{
		"Success_if_old_dir_does_not_exist": {oldDirExists: false, newDirExists: false},
		"Success_if_old_dir_exists_and_new_dir_does_not": {
			oldDirExists:     true,
			newDirExists:     false,
			wantOldDirExists: false,
			wantNewDirExists: true,
			wantDbInNewDir:   true,
		},
		"Success_if_old_dir_exists_and_new_dir_exists": {
			oldDirExists:     true,
			newDirExists:     true,
			wantOldDirExists: true,
			wantNewDirExists: true,
		},
		"Success_if_old_dir_exists_but_is_unreadable": {
			oldDirExists:     true,
			oldDirUnreadable: true,
		},

		"Error_if_new_dir_exists_but_is_unreadable": {
			oldDirExists:     true,
			newDirExists:     true,
			newDirUnreadable: true,
			wantedErr:        os.ErrPermission,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			oldParentDir := t.TempDir()
			newParentDir := t.TempDir()
			oldDir := filepath.Join(oldParentDir, "db")
			newDir := filepath.Join(newParentDir, "db")
			dbFilename := "authd.db"

			if tc.oldDirExists {
				err := os.Mkdir(oldDir, 0700)
				require.NoError(t, err, "failed to create old dir")
				err = fileutils.Touch(filepath.Join(oldDir, dbFilename))
				require.NoError(t, err, "failed to create db file")
			}

			if tc.oldDirUnreadable {
				err := os.Chmod(oldParentDir, 0000)
				require.NoError(t, err, "failed to make old dir unreadable")
				// Ensure that the directory is readable after the test, to avoid issues with cleanup
				defer func() {
					//nolint:gosec // G302 Permissions 0700 are not insecure for a directory
					err := os.Chmod(oldParentDir, 0700)
					require.NoError(t, err, "failed to make old dir readable")
				}()
			}

			if tc.newDirExists {
				err := os.Mkdir(newDir, 0700)
				require.NoError(t, err, "failed to create new dir")
			}

			if tc.newDirUnreadable {
				err := os.Chmod(newParentDir, 0000)
				require.NoError(t, err, "failed to make new dir unreadable")
				// Ensure that the directory is readable after the test, to avoid issues with cleanup
				defer func() {
					//nolint:gosec // G302 Permissions 0700 are not insecure for a directory
					err := os.Chmod(newParentDir, 0700)
					require.NoError(t, err, "failed to make new dir readable")
				}()
			}

			err := maybeMigrateOldDBDir(oldDir, newDir)
			require.ErrorIs(t, err, tc.wantedErr)

			if tc.wantOldDirExists {
				_, err := os.Stat(oldDir)
				require.NoError(t, err, "old dir does not exist")
			}

			if tc.wantNewDirExists {
				_, err := os.Stat(newDir)
				require.NoError(t, err, "new dir does not exist")
			}

			if tc.wantDbInNewDir {
				_, err := os.Stat(filepath.Join(newDir, dbFilename))
				require.NoError(t, err, "db file does not exist in new dir")
			}
		})
	}
}
