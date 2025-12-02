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
				if regexp.MustCompile(`Could not get owner of home directory "([^"]+)"`).MatchString(w) {
					warnings[i] = `Could not get owner of home directory "{{HOME}}"`
				}
				if regexp.MustCompile(`Not changing ownership of home directory "([^"]+)", because it is not owned by UID \d+ \(current owner: \d+\)`).MatchString(w) {
					warnings[i] = `Not changing ownership of home directory "{{HOME}}", because it is not owned by UID {{UID}} (current owner: {{CURR_UID}})`
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

func addUserToSystem(t *testing.T, uid int) {
	t.Helper()

	//nolint:gosec // G204 we want to use exec.Command with variables here
	cmd := exec.Command(
		"useradd",
		"--uid", strconv.Itoa(uid),
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
