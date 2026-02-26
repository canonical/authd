package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/db/bbolt"
	localgrouptestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
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

func TestMaybeMigrateBBoltToSQLite(t *testing.T) {
	validTestdata := "testdata/multiple_users_and_groups.db.yaml"
	invalidTestdata := "testdata/invalid.db.yaml"
	groupFile := "testdata/group"

	// Make the userslocking package use a locking mechanism which doesn't
	// require root privileges.
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	testCases := map[string]struct {
		bboltExists      bool
		sqliteExists     bool
		bboltUnreadable  bool
		sqliteUnreadable bool
		bboltInvalid     bool

		wantMigrated bool
		wantError    bool
	}{
		"Migration_if_bbolt_exists_and_sqlite_does_not_exist": {bboltExists: true, wantMigrated: true},

		"No_migration_if_bbolt_does_not_exist":        {bboltExists: false, wantMigrated: false},
		"No_migration_if_both_bbolt_and_sqlite_exist": {bboltExists: true, sqliteExists: true},
		"No_migration_if_bbolt_is_unreadable":         {bboltUnreadable: true, wantMigrated: false},

		"Error_if_bbolt_contains_invalid_data": {bboltInvalid: true, wantError: true},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			// These tests can't be run in parallel, because they set the group file
			// used by the db package, which is a global variable.

			dbDir := t.TempDir()

			if tc.bboltExists {
				err := bbolt.Z_ForTests_CreateDBFromYAML(validTestdata, dbDir)
				require.NoError(t, err, "failed to create bbolt database")
			}

			if tc.bboltInvalid {
				err := bbolt.Z_ForTests_CreateDBFromYAML(invalidTestdata, dbDir)
				require.NoError(t, err, "failed to create bbolt database")
			}

			if tc.sqliteExists {
				err := fileutils.Touch(filepath.Join(dbDir, consts.DefaultDatabaseFileName))
				require.NoError(t, err, "failed to create sqlite file")
			}

			if tc.bboltUnreadable {
				err := os.Chmod(dbDir, 0000)
				require.NoError(t, err, "failed to make bbolt dir unreadable")
				// Ensure that the directory is readable after the test, to avoid issues with cleanup
				defer func() {
					//nolint:gosec // G302 Permissions 0700 are not insecure for a directory
					err := os.Chmod(dbDir, 0700)
					require.NoError(t, err, "failed to make bbolt dir readable")
				}()
			}

			// Create a temporary user group file for testing
			tempGroupFile := filepath.Join(t.TempDir(), "groups")
			err := fileutils.CopyFile(groupFile, tempGroupFile)
			require.NoError(t, err, "failed to copy group file for testing")

			// Make the localentries package use the test group file.
			tempGroupFile = localgrouptestutils.SetupGroupMock(t, tempGroupFile)

			migrated, err := maybeMigrateBBoltToSQLite(dbDir)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantMigrated, migrated)

			if !migrated {
				return
			}

			// Check that the bbolt database has been removed
			exists, err := fileutils.FileExists(filepath.Join(dbDir, bbolt.DBFilename()))
			require.NoError(t, err)
			require.False(t, exists)

			// Check the content of the SQLite database
			database, err := db.New(dbDir)
			t.Cleanup(func() {
				err := database.Close()
				require.NoError(t, err)
			})
			require.NoError(t, err)

			yamlData, err := db.Z_ForTests_DumpNormalizedYAML(database)
			require.NoError(t, err)
			golden.CheckOrUpdate(t, yamlData, golden.WithPath("db"))

			golden.CheckOrUpdateFileTree(t, tempGroupFile, golden.WithPath("group"))
		})
	}
}
