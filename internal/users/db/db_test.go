package db_test

import (
	"context"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/localentries"
	localgrouptestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	perm0666 := os.FileMode(0666)
	perm0000 := os.FileMode(0000)

	tests := map[string]struct {
		dbFile          string
		perm            *fs.FileMode
		corruptedDbFile bool

		wantErr bool
	}{
		"New_without_any_initialized_database": {},
		"New_with_already_existing_database":   {dbFile: "multiple_users_and_groups"},

		"Error_on_non_existent_db_dir":                   {dbFile: "-", wantErr: true},
		"Error_on_corrupted_db_file":                     {corruptedDbFile: true, wantErr: true},
		"Error_on_insecure_permissions_on_database_file": {dbFile: "multiple_users_and_groups", perm: &perm0666, wantErr: true},
		"Error_on_unreadable_database_file":              {dbFile: "multiple_users_and_groups", perm: &perm0000, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			dbDestPath := filepath.Join(dbDir, consts.DefaultDatabaseFileName)

			var m *db.Manager

			if tc.dbFile == "-" {
				err := os.RemoveAll(dbDir)
				require.NoError(t, err, "Setup: could not remove temporary database directory")
			} else if tc.dbFile != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", tc.dbFile+".db.yaml"), dbDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}
			if tc.corruptedDbFile {
				err := os.WriteFile(dbDestPath, []byte("corrupted"), 0600)
				require.NoError(t, err, "Setup: could not write corrupted database file")
			}

			if tc.perm != nil {
				err := os.Chmod(dbDestPath, *tc.perm)
				require.NoError(t, err, "Setup: could not change mode of database file")

				if *tc.perm == perm0666 {
					currentUser, err := user.Current()
					require.NoError(t, err)
					if os.Getenv("AUTHD_SKIP_ROOT_TESTS") != "" && currentUser.Username == "root" {
						t.Skip("Can't do permission checks as root")
					}
				}
			}

			m, err := db.New(dbDir)
			if tc.wantErr {
				require.Error(t, err, "New should return an error but didn't")
				return
			}
			require.NoError(t, err)
			defer m.Close()

			got, err := db.Z_ForTests_DumpNormalizedYAML(m)
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)

			// check database permission
			fileInfo, err := os.Stat(dbDestPath)
			require.NoError(t, err, "Failed to stat database")
			perm := fileInfo.Mode().Perm()
			require.Equal(t, fs.FileMode(0600), perm, "Database permission should be 0600")
		})
	}
}

func TestDatabaseRemovedWhenSchemaCreationFails(t *testing.T) {
	// Don't run this test in parallel because it writes a global variable (via db.SetCreateSchemaQuery)
	origQuery := db.GetCreateSchemaQuery()
	db.SetCreateSchemaQuery("invalid query")
	t.Cleanup(func() {
		db.SetCreateSchemaQuery(origQuery)
	})

	dbDir := t.TempDir()
	dbDestPath := filepath.Join(dbDir, consts.DefaultDatabaseFileName)

	_, err := db.New(dbDir)
	require.Error(t, err, "New should return an error when schema creation fails")

	exists, err := fileutils.FileExists(dbDestPath)
	require.NoError(t, err, "Failed to check if database file exists")
	require.False(t, exists, "Database file should not exist after failed schema creation")
}

func TestMigrationToLowercaseUserAndGroupNames(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_with_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	err = os.WriteFile(groupsFilePath, []byte(`root:x:0:
other-local-group:x:1234:
other-local-group-with-users:x:4321:user-foo,user-bar
LocalTestGroup:x:12345:TestUser
`), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(groupsFilePath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))
}

func TestMigrationToLowercaseUserAndGroupNamesEmptyDB(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	dbFile := "empty.db.yaml"
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", dbFile), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	err = fileutils.Touch(groupsFilePath)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	groupsFilePath = localgrouptestutils.SetupGroupMock(t, groupsFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	require.NoFileExists(t, groupsFilePath, "No new group file should have been created")
}

func TestMigrationToLowercaseUserAndGroupNamesAlreadyUpdated(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_with_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	err = os.WriteFile(groupsFilePath, []byte("LocalTestGroup:x:12345:testuser\n"), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	groupsFilePath = localgrouptestutils.SetupGroupMock(t, groupsFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	require.NoFileExists(t, groupsFilePath, "No new group file should have been created")
}

func TestMigrationToLowercaseUserAndGroupNamesWithSymlinkedGroupFile(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_with_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	realGroupsPath := filepath.Join(t.TempDir(), "real-groups")
	err = os.WriteFile(realGroupsPath, []byte("LocalTestGroup:x:12345:TestUser\n"), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	err = os.Symlink(realGroupsPath, groupsFilePath)
	require.NoError(t, err, "Setup: could not symlink group file")

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	// Check the group file is still a symlink
	groupsLink, err := os.Readlink(groupsFilePath)
	require.Equal(t, realGroupsPath, groupsLink)
	require.NoError(t, err)

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(realGroupsPath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))

	// Check the content of the backup group file
	backupFilePath := groupsFilePath + "-"
	usersGroupBackupContent, err := os.ReadFile(backupFilePath)
	require.NoError(t, err)
	golden.CheckOrUpdate(t, string(usersGroupBackupContent), golden.WithPath("groups-backup"))
}

func TestMigrationToLowercaseUserAndGroupNamesWithPreviousBackup(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_fully_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	originalGroupFileContents := []byte("LOCALTESTGROUP:x:12345:TESTUSER\n")
	err = os.WriteFile(groupsFilePath, originalGroupFileContents, 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	backupFilePath := groupsFilePath + "-"
	// Create a temporary user group file backup for testing
	err = os.WriteFile(backupFilePath, []byte("LocalTestGroup:x:12345:TestUserBackup\n"), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(groupsFilePath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))

	// Check the content of the backup group file
	userGroupBackupContent, err := os.ReadFile(backupFilePath)
	require.NoError(t, err)
	golden.CheckOrUpdate(t, string(userGroupBackupContent), golden.WithPath("groups-backup"))
}

func TestMigrationToLowercaseUserAndGroupNamesWithSymlinkedPreviousBackup(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_fully_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	originalGroupFileContents := []byte("LOCALTESTGROUP:x:12345:TESTUSER\n")
	err = os.WriteFile(groupsFilePath, originalGroupFileContents, 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	// Create a temporary user group file backup for testing
	realGroupsBackup := filepath.Join(t.TempDir(), "groups-backup")
	err = os.WriteFile(realGroupsBackup, []byte("LocalTestGroup:x:12345:TestUserBackup\n"), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Symlink it to the backup path
	backupFilePath := groupsFilePath + "-"
	err = os.Symlink(realGroupsBackup, backupFilePath)
	require.NoError(t, err, "Setup: could not create group file backup symlink")

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(groupsFilePath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))

	// Ensure the backup is not anymore a symlink
	fi, err := os.Lstat(backupFilePath)
	require.NoError(t, err)
	require.Zero(t, fi.Mode()&os.ModeSymlink, "Group file backup must not be a symlink")

	// Check the content of the backup group file
	userGroupBackupContent, err := os.ReadFile(backupFilePath)
	require.NoError(t, err)
	golden.CheckOrUpdate(t, string(userGroupBackupContent), golden.WithPath("groups-backup"))
}

func TestMigrationToLowercaseUserAndGroupNamesFails(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_fully_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	originalGroupFileContents := []byte("LOCALTESTGROUP:x:12345:USER\n")
	err = os.WriteFile(groupsFilePath, originalGroupFileContents, 0600)
	require.NoError(t, err, "Setup: could not create group file")

	err = os.Chmod(groupsFilePath, 0000)
	require.NoError(t, err, "Setup: setting chmod to %q", groupsFilePath)

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.Error(t, err, "Updating db fails")
	require.Nil(t, m, "Db should be unset")

	err = os.Chmod(groupsFilePath, 0600)
	require.NoError(t, err, "Setup: setting chmod to %q", groupsFilePath)

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(groupsFilePath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))
}

func TestMigrationToLowercaseUserAndGroupNamesWithBackupFailure(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationToLowercaseUserAndGroupNames/one_users_multiple_groups_with_uppercase.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Create a temporary user group file for testing
	groupsFilePath := filepath.Join(t.TempDir(), "groups")
	err = os.WriteFile(groupsFilePath, []byte("LocalTestGroup:x:12345:TestUser\n"), 0600)
	require.NoError(t, err, "Setup: could not create group file")

	// Make the localentries package use the test group file.
	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
	localentries.Z_ForTests_SetGroupPath(groupsFilePath, groupsFilePath)

	// To trigger all the errors we should handle, we create a non-empty
	// directory as backup file. So that we cannot copy or remove it.
	backupFilePath := groupsFilePath + "-"
	err = os.Mkdir(backupFilePath, 0700)
	require.NoError(t, err, "Setup: creating directory %q", backupFilePath)

	err = fileutils.Touch(filepath.Join(backupFilePath, "a-file"))
	require.NoError(t, err, "Setup: touching a file in %q", backupFilePath)

	// Make the userutils package to use test locking for the group file
	userslocking.Z_ForTests_OverrideLockingWithCleanup(t)

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent, golden.WithPath("db"))

	// Check the content of the user group file
	userGroupContent, err := os.ReadFile(groupsFilePath)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, string(userGroupContent), golden.WithPath("groups"))
}

func TestMigrationAddLockedColumnToUsersTable(t *testing.T) {
	// Create a database from the testdata
	dbDir := t.TempDir()
	sqlDump := "TestMigrationAddLockedColumnToUsersTable/one_user_and_group_without_locked_column.sql"
	err := db.Z_ForTests_CreateDBFromDump(filepath.Join("testdata", sqlDump), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	// Run the migrations
	m, err := db.New(dbDir)
	require.NoError(t, err)

	// Check the content of the SQLite database
	dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, dbContent)
}

func TestUpdateUserEntry(t *testing.T) {
	t.Parallel()

	userCases := map[string]db.UserRow{
		"user1": {
			Name:  "user1",
			UID:   1111,
			Gecos: "User1 gecos\nOn multiple lines",
			Dir:   "/home/user1",
			Shell: "/bin/bash",
		},
		"user1-new-attributes": {
			Name:  "user1",
			UID:   1111,
			Gecos: "New user1 gecos",
			Dir:   "/home/user1",
			Shell: "/bin/dash",
		},
		"user1-new-name": {
			Name:  "newuser1",
			UID:   1111,
			Gecos: "User1 gecos\nOn multiple lines",
			Dir:   "/home/user1",
			Shell: "/bin/bash",
		},
		"user1-new-homedir": {
			Name:  "user1",
			UID:   1111,
			Gecos: "User1 gecos\nOn multiple lines",
			Dir:   "/new/home/user1",
			Shell: "/bin/bash",
		},
		"user1-new-shell": {
			Name:  "user1",
			UID:   1111,
			Gecos: "User1 gecos\nOn multiple lines",
			Dir:   "/new/home/user1",
			Shell: "/new/shell",
		},
		"user1-without-gecos": {
			Name:  "user1",
			UID:   1111,
			Dir:   "/home/user1",
			Shell: "/bin/bash",
		},
		"user3": {
			Name:  "user3",
			UID:   3333,
			Gecos: "User3 gecos",
			Dir:   "/home/user3",
			Shell: "/bin/zsh",
		},
	}
	groupCases := map[string]db.GroupRow{
		"group1":                        {"group1", 11111, "12345678"},
		"group1-different-gid":          {"group1", 99999, "12345678"},
		"group1-different-ugid":         {"group1", 11111, "99999999"},
		"group1-different-gid-and-ugid": {"group1", 99999, "99999999"},
		"new-group-same-gid":            {"new-group-same-gid", 11111, "99999999"},
		"new-group-same-ugid":           {"new-group-same-ugid", 99999, "12345678"},
		"new-group-same-gid-and-ugid":   {"new-group-same-gid", 11111, "12345678"},
		"group2":                        {"group2", 22222, "56781234"},
		"group3":                        {"group3", 33333, "34567812"},
	}

	tests := map[string]struct {
		userCase    string
		groupCases  []string
		localGroups []string
		dbFile      string

		wantErr bool
	}{
		// New user
		"Insert_new_user": {},
		"Insert_new_user_without_optional_gecos_field": {userCase: "user1-without-gecos"},

		// User and Group updates
		"Update_user_by_changing_attributes":                      {userCase: "user1-new-attributes", dbFile: "one_user_and_group"},
		"Update_user_does_not_change_homedir_if_it_exists":        {userCase: "user1-new-homedir", dbFile: "one_user_and_group"},
		"Update_user_does_not_change_shell_if_it_exists":          {userCase: "user1-new-shell", dbFile: "one_user_and_group"},
		"Update_user_by_removing_optional_gecos_field_if_not_set": {userCase: "user1-without-gecos", dbFile: "one_user_and_group"},

		// Group updates
		"Update_user_by_adding_a_new_group":         {groupCases: []string{"group1", "group2"}, dbFile: "one_user_and_group"},
		"Update_user_by_adding_a_new_default_group": {groupCases: []string{"group2", "group1"}, dbFile: "one_user_and_group"},
		"Update_user_by_renaming_a_group":           {groupCases: []string{"new-group-same-gid-and-ugid"}, dbFile: "one_user_and_group"},
		"Remove_group_from_user":                    {groupCases: []string{"group2"}, dbFile: "one_user_and_group"},
		"Update_user_by_adding_a_new_local_group":   {localGroups: []string{"localgroup1"}, dbFile: "one_user_and_group"},

		// Multi users handling
		"Update_only_user_even_if_we_have_multiple_of_them":     {dbFile: "multiple_users_and_groups"},
		"Add_user_to_group_from_another_user":                   {groupCases: []string{"group1", "group2"}, dbFile: "multiple_users_and_groups"},
		"Remove_user_from_a_group_still_part_from_another_user": {userCase: "user3", groupCases: []string{"group3"}, dbFile: "multiple_users_and_groups"},

		// Renaming errors
		"Error_when_user_has_conflicting_uid": {userCase: "user1-new-name", dbFile: "one_user_and_group", wantErr: true},

		// Error cases
		"Error_when_new_group_has_conflicting_gid":                  {groupCases: []string{"new-group-same-gid"}, dbFile: "one_user_and_group", wantErr: true},
		"Error_when_new_group_has_conflicting_ugid":                 {groupCases: []string{"new-group-same-ugid"}, dbFile: "one_user_and_group", wantErr: true},
		"Error_when_group_has_same_name_and_ugid_but_different_gid": {groupCases: []string{"group1-different-gid"}, dbFile: "one_user_and_group", wantErr: true},
		"Error_when_group_has_same_name_and_gid_but_different_ugid": {groupCases: []string{"group1-different-ugid"}, dbFile: "one_user_and_group", wantErr: true},
		"Error_when_group_has_same_name_but_different_gid_and_ugid": {groupCases: []string{"group1-different-gid-and-ugid"}, dbFile: "one_user_and_group", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			if tc.userCase == "" {
				tc.userCase = "user1"
			}
			if tc.groupCases == nil {
				tc.groupCases = []string{"group1"}
			}

			user := userCases[tc.userCase]
			var groups []db.GroupRow
			for _, g := range tc.groupCases {
				groups = append(groups, groupCases[g])
			}
			user.GID = groups[0].GID

			err := c.UpdateUserEntry(user, groups, tc.localGroups)
			if err != nil {
				log.Errorf(context.Background(), "UpdateUserEntry error: %v", err)
			}
			if tc.wantErr {
				require.Error(t, err, "UpdateFromUserInfo should return an error but didn't")
				return
			}
			require.NoError(t, err)

			got, err := db.Z_ForTests_DumpNormalizedYAML(c)
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestUserByID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_existing_user": {dbFile: "one_user_and_group"},

		"Error_on_missing_user": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.UserByID(1111)
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestUserByName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErrType error
		wantErr     bool
	}{
		"Get_existing_user": {dbFile: "one_user_and_group"},

		"Error_on_missing_user": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.UserByName("user1")
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestAllUsers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr bool
	}{
		"Get_one_user":       {dbFile: "one_user_and_group"},
		"Get_multiple_users": {dbFile: "multiple_users_and_groups"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.AllUsers()
			requireGetAssertions(t, got, tc.wantErr, nil, err)
		})
	}
}

func TestGroupByID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_existing_group": {dbFile: "one_user_and_group"},

		"Error_on_missing_group": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.GroupByID(11111)
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestGroupWithMembersByID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_existing_group": {dbFile: "one_user_and_group"},

		"Error_on_missing_group": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.GroupWithMembersByID(11111)
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestGroupByName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_existing_group": {dbFile: "one_user_and_group"},

		"Error_on_missing_group": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.GroupByName("group1")
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestGroupWithMembersByName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_existing_group": {dbFile: "one_user_and_group"},

		"Error_on_missing_group": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.GroupWithMembersByName("group1")
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestUserGroups(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_groups_of_existing_user": {dbFile: "one_user_and_group"},

		"Error_on_missing_user": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.UserGroups(1111)
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestAllGroupsWithMembers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Get_one_group":       {dbFile: "one_user_and_group"},
		"Get_multiple_groups": {dbFile: "multiple_users_and_groups"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			got, err := c.AllGroupsWithMembers()
			requireGetAssertions(t, got, tc.wantErr, tc.wantErrType, err)
		})
	}
}

func TestUpdateBrokerForUser(t *testing.T) {
	t.Parallel()

	c := initDB(t, "one_user_and_group")

	// Update broker for existent user
	err := c.UpdateBrokerForUser("user1", "ExampleBrokerID")
	require.NoError(t, err, "UpdateBrokerForUser for an existent user should not return an error")

	// Error when updating broker for nonexistent user
	err = c.UpdateBrokerForUser("nonexistent", "ExampleBrokerID")
	require.Error(t, err, "UpdateBrokerForUser for a nonexistent user should return an error")
}

func TestUpdateLockedFieldForUser(t *testing.T) {
	t.Parallel()

	c := initDB(t, "one_user_and_group")

	// Update broker for existent user
	err := c.UpdateLockedFieldForUser("user1", true)
	require.NoError(t, err, "UpdateLockedFieldForUser for an existent user should not return an error")

	// Error when updating broker for nonexistent user
	err = c.UpdateLockedFieldForUser("nonexistent", false)
	require.Error(t, err, "UpdateLockedFieldForUser for a nonexistent user should return an error")
}

func TestSetUserID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		nonExistentUser bool
		uidAlreadyInUse bool
		uidAlreadySet   bool

		wantErr         bool
		wantErrType     error
		wantUnchangedDB bool
	}{
		"Set_user_id_for_existing_user": {},
		"No_op_if_uid_is_already_set":   {uidAlreadySet: true, wantUnchangedDB: true},

		"Error_on_nonexistent_user":   {nonExistentUser: true, wantErrType: db.NoDataFoundError{}},
		"Error_if_uid_already_in_use": {uidAlreadyInUse: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := initDB(t, "multiple_users_and_groups")

			username := "user1"
			if tc.nonExistentUser {
				username = "nonexistent"
			}

			var newUID uint32 = 1234
			if tc.uidAlreadyInUse {
				newUID = 2222
			}
			if tc.uidAlreadySet {
				newUID = 1111
			}

			var oldDBContent string
			var err error
			if tc.wantUnchangedDB {
				oldDBContent, err = db.Z_ForTests_DumpNormalizedYAML(m)
				require.NoError(t, err)
			}

			err = m.SetUserID(username, newUID)
			log.Infof(context.Background(), "SetUserID error: %v", err)

			if tc.wantErrType != nil {
				require.ErrorIs(t, err, tc.wantErrType, "SetUserID should return expected error")
				return
			}
			if tc.wantErr {
				require.Error(t, err, "SetUserID should return an error but didn't")
				return
			}
			require.NoError(t, err, "SetUserID should not return an error on existing user")

			dbContent, err := db.Z_ForTests_DumpNormalizedYAML(m)
			require.NoError(t, err)

			if tc.wantUnchangedDB {
				require.Equal(t, oldDBContent, dbContent, "SetUserID should not change the database content")
				return
			}

			golden.CheckOrUpdate(t, dbContent)
		})
	}
}

func TestRemoveDb(t *testing.T) {
	t.Parallel()

	c := initDB(t, "multiple_users_and_groups")
	dbDir := filepath.Dir(c.Path())

	// First call should return with no error.
	require.NoError(t, db.RemoveDB(dbDir), "RemoveDB should not return an error on the first call")
	require.NoFileExists(t, dbDir, "RemoveDB should remove the database file")

	// Second call should return ErrNotExist as the database file was already removed.
	require.ErrorIs(t, db.RemoveDB(dbDir), fs.ErrNotExist, "RemoveDB should return os.ErrNotExist on the second call")
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Deleting_last_user_from_a_group_keeps_the_group_record":  {dbFile: "one_user_and_group"},
		"Deleting_existing_user_keeps_other_group_members_intact": {dbFile: "multiple_users_and_groups"},

		"Error_on_missing_user": {wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := initDB(t, tc.dbFile)

			err := c.DeleteUser(1111)
			log.Debugf(context.Background(), "DeleteUser error: %v", err)
			if tc.wantErr {
				require.Error(t, err, "DeleteUser should return an error but didn't")
				return
			}
			if tc.wantErrType != nil {
				require.ErrorIs(t, err, tc.wantErrType, "DeleteUser should return expected error")
				return
			}
			require.NoError(t, err)

			got, err := db.Z_ForTests_DumpNormalizedYAML(c)
			require.NoError(t, err)
			golden.CheckOrUpdate(t, got)
		})
	}
}

// TestBackwardCompatibilityAndMigrations covers loading legacy schemas (e.g., v2 with INT ugid)
// and migrating older schemas (e.g., v1 without 'locked' column) to the latest schema.
func TestBackwardCompatibilityAndMigrations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dump string
	}{
		"SchemaV2_IntUGID":        {dump: filepath.Join("testdata", "TestLoadSchemaV2WithIntUGID", "one_user_and_group_v2.sql")},
		"SchemaV1_NoLockedColumn": {dump: filepath.Join("testdata", "TestMigrationAddLockedColumnToUsersTable", "one_user_and_group_without_locked_column.sql")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()

			err := db.Z_ForTests_CreateDBFromDump(tc.dump, tempDir)
			require.NoError(t, err, "Setup: could not create database from dump")

			// Open using current manager, it'll trigger a migration depending on schema_version.
			m, err := db.New(tempDir)
			require.NoError(t, err, "Setup: could not open manager for database")
			t.Cleanup(func() { _ = m.Close() })

			// Validate user can be read and that locked is false (either set or default).
			u, err := m.UserByID(1111)
			require.NoError(t, err, "Should read user from DB")
			require.Equal(t, "user1", u.Name)
			require.EqualValues(t, 11111, u.GID)
			require.False(t, u.Locked, "locked should be false in both old and migrated schemas")

			// Validate group and members. ugid should read as string regardless of underlying type.
			g, err := m.GroupWithMembersByID(11111)
			require.NoError(t, err, "Should read group from DB")
			require.Equal(t, "group1", g.Name)
			require.Equal(t, "12345678", g.UGID)
			require.Len(t, g.Users, 1)
			require.Equal(t, "user1", g.Users[0])

			// Also ensure lookup by UGID works with string input.
			gByUGID, err := m.GroupByUGID("12345678")
			require.NoError(t, err)
			require.EqualValues(t, 11111, gByUGID.GID)
		})
	}
}

// initDB returns a new database ready to be used alongside its database directory.
func initDB(t *testing.T, dbFile string) *db.Manager {
	t.Helper()

	dbDir, err := os.MkdirTemp("", "authd-db-test-*")
	require.NoError(t, err)

	if os.Getenv("SKIP_TEST_CLEANUP") == "" {
		t.Cleanup(func() {
			err := os.RemoveAll(dbDir)
			require.NoError(t, err, "Cleanup: could not remove temporary database directory")
		})
	}

	if dbFile != "" {
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", dbFile+".db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")
	}

	m, err := db.New(dbDir)
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })

	return m
}

func requireGetAssertions[E any](t *testing.T, got E, wantErr bool, wantErrType, err error) {
	t.Helper()

	if wantErrType != nil {
		require.ErrorIs(t, err, wantErrType, "Get should return expected error")
		return
	}
	if wantErr {
		require.Error(t, err, "Get should return an error")
		return
	}
	require.NoError(t, err)

	golden.CheckOrUpdateYAML(t, got)
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	m.Run()
}
