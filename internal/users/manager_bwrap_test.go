package users_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

func TestSetUserID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		nonExistentUser                  bool
		emptyUsername                    bool
		uidAlreadySet                    bool
		uidAlreadyInUseByAuthdUser       bool
		uidAlreadyInUseAsGIDofAuthdUser  bool
		uidAlreadyInUseBySystemUser      bool
		uidAlreadyInUseAsGIDofSystemUser bool
		uidTooLarge                      bool
		homeDirDoesNotExist              bool
		homeDirOwnedByOtherUser          bool
		homeDirCannotBeAccessed          bool
		homeDirOwnerCannotBeChanged      bool

		wantErr      bool
		wantErrType  error
		wantWarnings int
	}{
		"Successfully_set_UID": {},
		"Successfully_set_UID_if_ID_is_already_in_use_as_GID_of_system_user": {uidAlreadyInUseAsGIDofSystemUser: true},
		"Successfully_set_UID_if_ID_is_already_in_use_as_GID_of_authd_user":  {uidAlreadyInUseAsGIDofAuthdUser: true},
		"Successfully_set_UID_when_home_directory_does_not_exist":            {homeDirDoesNotExist: true},

		"Warning_if_user_already_has_given_UID":            {uidAlreadySet: true, wantWarnings: 1},
		"Warning_if_home_directory_is_owned_by_other_user": {homeDirOwnedByOtherUser: true, wantWarnings: 1},
		"Warning_if_home_directory_cannot_be_accessed":     {homeDirCannotBeAccessed: true, wantWarnings: 1},

		"Error_if_username_is_empty":                      {emptyUsername: true, wantErr: true},
		"Error_if_user_does_not_exist":                    {nonExistentUser: true, wantErrType: db.NoDataFoundError{}},
		"Error_if_UID_is_already_in_use_by_authd":         {uidAlreadyInUseByAuthdUser: true, wantErr: true},
		"Error_if_UID_is_already_in_use_by_system":        {uidAlreadyInUseBySystemUser: true, wantErr: true},
		"Error_if_UID_is_too_large":                       {uidTooLarge: true, wantErr: true},
		"Error_if_home_directory_owner_cannot_be_changed": {homeDirOwnerCannotBeChanged: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !testutils.RunningInBubblewrap() {
				testutils.RunTestInBubbleWrap(t)
				return
			}

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "multiple_users_and_groups.db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			username := "user1"
			if tc.nonExistentUser {
				username = "nonexistent"
			} else if tc.emptyUsername {
				username = ""
			} else if !tc.homeDirDoesNotExist {
				uid := 1111
				gid := 11111
				if tc.homeDirOwnedByOtherUser {
					uid = 2222
				}
				home := createTemporaryHome(t, uid, gid, tc.homeDirCannotBeAccessed, tc.homeDirOwnerCannotBeChanged)
				setHome(t, m, username, home)
			}

			newUID := 54321
			if tc.uidTooLarge {
				newUID = math.MaxInt32 + 1
			}

			if tc.uidAlreadySet {
				setUID(t, m, username, newUID)
			}
			if tc.uidAlreadyInUseByAuthdUser {
				setUID(t, m, "user2", newUID)
			}
			if tc.uidAlreadyInUseAsGIDofAuthdUser {
				newUID = 22222
			}
			if tc.uidAlreadyInUseBySystemUser {
				addUserToSystem(t, newUID)
			}
			if tc.uidAlreadyInUseAsGIDofSystemUser {
				addGroupToSystem(t, newUID)
			}

			//nolint:gosec // G115 we set the UID above to values that are valid uint32
			warnings, err := m.SetUserID(username, uint32(newUID))
			log.Infof(context.Background(), "SetUserID error: %v", err)
			log.Infof(context.Background(), "SetUserID warnings: %v", warnings)

			if tc.wantErrType != nil {
				require.ErrorIs(t, err, tc.wantErrType, "SetUserID should return expected error")
				return
			}
			if tc.wantErr {
				require.Error(t, err, "SetUserID should return an error but didn't")
				return
			}
			require.NoError(t, err, "SetUserID should not return an error")
			require.Len(t, warnings, tc.wantWarnings, "Unexpected number of warnings")

			yamlData, err := db.Z_ForTests_DumpNormalizedYAML(m.DB())
			require.NoError(t, err)
			golden.CheckOrUpdate(t, yamlData, golden.WithPath("db"))

			if len(warnings) == 0 {
				return
			}

			// To make the tests deterministic, we replace the temporary home directory path with a placeholder
			for i, w := range warnings {
				if regexp.MustCompile(`Could not get owner of home directory '([^"]+)'`).MatchString(w) {
					warnings[i] = `Could not get owner of home directory '{{HOME}}'`
				}
				if regexp.MustCompile(`Not changing ownership of home directory '([^"]+)', because it is not owned by UID \d+ \(current owner: \d+\)`).MatchString(w) {
					warnings[i] = `Not changing ownership of home directory '{{HOME}}', because it is not owned by UID {{UID}} (current owner: {{CURR_UID}})`
				}
			}

			golden.CheckOrUpdateYAML(t, warnings, golden.WithPath("warnings"))
		})
	}
}

func TestSetGroupID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		nonExistentGroup                 bool
		emptyGroupname                   bool
		gidAlreadySet                    bool
		gidAlreadyInUseByAuthdGroup      bool
		gidAlreadyInUseAsUIDofAuthdUser  bool
		gidAlreadyInUseBySystemGroup     bool
		gidAlreadyInUseAsUIDofSystemUser bool
		gidIsPrimaryGroupOfMultipleUsers bool
		gidIsNotPrimaryGroupOfAnyUser    bool
		gidTooLarge                      bool
		homeDirDoesNotExist              bool
		homeDirOwnedByOtherGroup         bool
		homeDirCannotBeAccessed          bool
		homeDirOwnerCannotBeChanged      bool

		wantErr      bool
		wantErrType  error
		wantWarnings int
	}{
		"Successfully_set_GID": {},
		"Successfully_set_GID_if_ID_is_already_in_use_as_UID_of_system_user": {gidAlreadyInUseAsUIDofSystemUser: true},
		"Successfully_set_GID_if_ID_is_already_in_use_as_UID_of_authd_user":  {gidAlreadyInUseAsUIDofAuthdUser: true},
		"Successfully_set_GID_when_home_directory_does_not_exist":            {homeDirDoesNotExist: true},
		"Successfully_set_GID_when_group_is_not_primary_group_of_any_user":   {gidIsNotPrimaryGroupOfAnyUser: true},
		"Primary_groups_of_multiple_users_are_updated":                       {gidIsPrimaryGroupOfMultipleUsers: true},

		"Warning_if_group_already_has_given_GID":            {gidAlreadySet: true, wantWarnings: 1},
		"Warning_if_home_directory_is_owned_by_other_group": {homeDirOwnedByOtherGroup: true, wantWarnings: 1},
		"Warning_if_home_directory_cannot_be_accessed":      {homeDirCannotBeAccessed: true, wantWarnings: 1},

		"Error_if_groupname_is_empty":                     {emptyGroupname: true, wantErr: true},
		"Error_if_group_does_not_exist":                   {nonExistentGroup: true, wantErrType: db.NoDataFoundError{}},
		"Error_if_GID_is_already_in_use_by_authd":         {gidAlreadyInUseByAuthdGroup: true, wantErr: true},
		"Error_if_GID_is_already_in_use_by_system":        {gidAlreadyInUseBySystemGroup: true, wantErr: true},
		"Error_if_GID_is_too_large":                       {gidTooLarge: true, wantErr: true},
		"Error_if_home_directory_owner_cannot_be_changed": {homeDirOwnerCannotBeChanged: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !testutils.RunningInBubblewrap() {
				testutils.RunTestInBubbleWrap(t)
				return
			}

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "multiple_users_and_groups.db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			groupname := "group1"
			if tc.nonExistentGroup {
				groupname = "nonexistent"
			} else if tc.emptyGroupname {
				groupname = ""
			} else if !tc.homeDirDoesNotExist {
				uid := 1111
				gid := 11111
				if tc.homeDirOwnedByOtherGroup {
					gid = 2222
				}
				home := createTemporaryHome(t, uid, gid, tc.homeDirCannotBeAccessed, tc.homeDirOwnerCannotBeChanged)
				setHome(t, m, "user1", home)
			}

			newGID := 54321
			if tc.gidTooLarge {
				newGID = math.MaxInt32 + 1
			}

			if tc.gidAlreadySet {
				setGID(t, m, groupname, newGID)
			}
			if tc.gidAlreadyInUseByAuthdGroup {
				setGID(t, m, "group3", newGID)
			}
			if tc.gidAlreadyInUseAsUIDofAuthdUser {
				setUID(t, m, "user2", newGID)
			}
			if tc.gidAlreadyInUseBySystemGroup {
				addGroupToSystem(t, newGID)
			}
			if tc.gidAlreadyInUseAsUIDofSystemUser {
				addUserToSystem(t, newGID)
			}
			if tc.gidIsNotPrimaryGroupOfAnyUser {
				// Change the primary group of "user1" to another group
				setPrimaryGroup(t, m, "user1", 22222)
			}
			if tc.gidIsPrimaryGroupOfMultipleUsers {
				// Change the primary group of "user2" to the group we want to change
				setPrimaryGroup(t, m, "user2", 11111)
			}

			//nolint:gosec // G115 we set the GID above to values that are valid uint32
			warnings, err := m.SetGroupID(groupname, uint32(newGID))
			log.Infof(context.Background(), "SetGroupID error: %v", err)
			log.Infof(context.Background(), "SetGroupID warnings: %v", warnings)

			if tc.wantErrType != nil {
				require.ErrorIs(t, err, tc.wantErrType, "SetGroupID should return expected error")
				return
			}
			if tc.wantErr {
				require.Error(t, err, "SetGroupID should return an error but didn't")
				return
			}
			require.NoError(t, err, "SetGroupID should not return an error")
			require.Len(t, warnings, tc.wantWarnings, "Unexpected number of warnings")

			yamlData, err := db.Z_ForTests_DumpNormalizedYAML(m.DB())
			require.NoError(t, err)
			golden.CheckOrUpdate(t, yamlData, golden.WithPath("db"))

			if len(warnings) == 0 {
				return
			}

			// To make the tests deterministic, we replace the temporary home directory path with a placeholder
			for i, w := range warnings {
				if regexp.MustCompile(`Could not get owner of home directory '([^"]+)'`).MatchString(w) {
					warnings[i] = `Could not get owner of home directory '{{HOME}}'`
				}
				if regexp.MustCompile(`Not changing ownership of home directory '([^"]+)', because it is not owned by GID \d+ \(current owner: \d+\)`).MatchString(w) {
					warnings[i] = `Not changing ownership of home directory '{{HOME}}', because it is not owned by GID {{GID}} (current owner: {{CURR_GID}})`
				}
			}

			golden.CheckOrUpdateYAML(t, warnings, golden.WithPath("warnings"))
		})
	}
}

// createTemporaryHome creates a temporary home directory for the given user.
func createTemporaryHome(t *testing.T, uid, gid int, inaccessible, cannotBeChanged bool) string {
	t.Helper()

	// We use a deterministic path (/tmp/home) here because the home directory
	// is stored in the database which we dump to a golden file, so we would
	// have to replace the path in the golden file to make it deterministic.
	// It's simpler to just use a deterministic path here.
	parentDir := filepath.Join(os.TempDir(), "home")
	home := filepath.Join(parentDir, fmt.Sprintf("user-%d", uid))

	if inaccessible {
		// Create the parent directory as a file, so that the home directory cannot be accessed.
		err := fileutils.Touch(parentDir)
		require.NoError(t, err, "Setup: could not create temporary file")
		return home
	}

	err := os.MkdirAll(parentDir, 0700)
	require.NoError(t, err, "Setup: could not create parent directory for home")

	if cannotBeChanged {
		//nolint:gosec // G204 we want to use exec.Command with variables here
		cmd := exec.Command("mount", "-t", "tmpfs", "tmpfs", parentDir)
		cmd.Stdout = t.Output()
		cmd.Stderr = t.Output()
		err := cmd.Run()
		require.NoError(t, err, "Setup: could not mount tmpfs")
	}

	// Create the home directory and chown it to the user
	err = os.MkdirAll(home, 0700)
	require.NoError(t, err, "Setup: could not create home directory")

	err = os.Chown(home, uid, gid)
	require.NoError(t, err, "Setup: could not chown home directory")

	if cannotBeChanged {
		//nolint:gosec // G204 we want to use exec.Command with variables here
		cmd := exec.Command("mount", "-o", "remount,ro", parentDir, parentDir)
		cmd.Stdout = t.Output()
		cmd.Stderr = t.Output()
		err = cmd.Run()
		require.NoError(t, err, "Setup: could not remount tmpfs read-only")
	}

	return home
}

// setHome updates the home directory of the given user.
func setHome(t *testing.T, m *users.Manager, username string, home string) {
	t.Helper()

	u, err := m.DB().UserByName(username)
	require.NoError(t, err, "Setup: could not get user by ID")

	// UpdateUserEntry doesn't update the home directory if the user already exists
	// and has a non-empty home directory set. We need to delete the user first.
	err = m.DB().DeleteUser(u.UID)
	require.NoError(t, err, "Setup: could not delete user")

	// Set the new home directory
	u.Dir = home

	// Re-add the user with the new home directory
	err = m.DB().UpdateUserEntry(u, nil, nil)
	require.NoError(t, err, "Setup: could not update user")
}

func setUID(t *testing.T, m *users.Manager, username string, uid int) {
	t.Helper()

	if uid < 0 || uid > math.MaxUint32 {
		require.Fail(t, "Setup: invalid UID %d", uid)
	}

	err := m.DB().SetUserID(username, uint32(uid))
	require.NoError(t, err, "Setup: could not set user ID")
}

func setGID(t *testing.T, m *users.Manager, groupname string, gid int) {
	t.Helper()

	if gid < 0 || gid > math.MaxUint32 {
		require.Fail(t, "Setup: invalid GID %d", gid)
	}

	_, err := m.DB().SetGroupID(groupname, uint32(gid))
	require.NoError(t, err, "Setup: could not set group ID")
}

// setPrimaryGroup updates the primary group of the given user.
func setPrimaryGroup(t *testing.T, m *users.Manager, username string, gid uint32) {
	t.Helper()

	u, err := m.DB().UserByName(username)
	require.NoError(t, err, "Setup: could not get user by ID")

	u.GID = gid

	err = m.DB().UpdateUserEntry(u, nil, nil)
	require.NoError(t, err, "Setup: could not update user")
}

func addUserToSystem(t *testing.T, uid int) {
	t.Helper()

	//nolint:gosec // G204 we want to use exec.Command with variables here
	cmd := exec.Command(
		"useradd",
		"--uid", strconv.Itoa(uid),
		"--gid", "0",
		"--no-create-home",
		fmt.Sprintf("test-%d", uid),
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: useradd failed: %s", output)
}

func addGroupToSystem(t *testing.T, gid int) {
	t.Helper()

	//nolint:gosec // G204 we want to use exec.Command with variables here
	cmd := exec.Command(
		"groupadd",
		"--gid", strconv.Itoa(gid),
		fmt.Sprintf("test-%d", gid),
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: groupadd failed: %s", output)
}
