package users_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/localentries"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/internal/users/tempentries"
	userstestutils "github.com/canonical/authd/internal/users/testutils"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewManager(t *testing.T) {
	tests := map[string]struct {
		dbFile          string
		corruptedDbFile bool
		uidMin          uint32
		uidMax          uint32
		gidMin          uint32
		gidMax          uint32

		wantErr bool
	}{
		"Successfully_create_manager_with_default_config":                           {},
		"Successfully_create_manager_with_custom_config":                            {uidMin: 10000, uidMax: 20000, gidMin: 10000, gidMax: 20000},
		"Successfully_create_manager_with_UID_range_next_to_systemd_dynamic_users":  {uidMin: users.SystemdDynamicUIDMax + 1, uidMax: users.SystemdDynamicUIDMax + 10000},
		"Successfully_create_manager_with_GID_range_next_to_systemd_dynamic_groups": {gidMin: users.SystemdDynamicUIDMin - 1000, gidMax: users.SystemdDynamicUIDMin - 1},

		"Warns_creating_manager_with_partially_invalid_UID_ranges": {uidMin: 1, uidMax: 20000},
		"Warns_creating_manager_with_partially_invalid_GID_ranges": {gidMin: 1, gidMax: 20000},

		// Corrupted databases
		"Error_when_database_is_corrupted": {corruptedDbFile: true, wantErr: true},
		"Error_if_dbDir_does_not_exist":    {dbFile: "-", wantErr: true},

		// Invalid UIDs/GIDs ranges
		"Error_if_UID_MIN_is_equal_to_UID_MAX":                    {uidMin: 1000, uidMax: 1000, wantErr: true},
		"Error_if_GID_MIN_is_equal_to_GID_MAX":                    {gidMin: 1000, gidMax: 1000, wantErr: true},
		"Error_if_UID_range_is_too_small":                         {uidMin: 1000, uidMax: 2000, wantErr: true},
		"Error_if_UID_range_overlaps_with_systemd_dynamic_users":  {uidMin: users.SystemdDynamicUIDMin, uidMax: users.SystemdDynamicUIDMax, wantErr: true},
		"Error_if_GID_range_overlaps_with_systemd_dynamic_groups": {gidMin: users.SystemdDynamicUIDMin, gidMax: users.SystemdDynamicUIDMax, wantErr: true},
		"Error_if_UID_range_is_larger_than_max_signed_int32":      {uidMin: 0, uidMax: math.MaxInt32 + 1, wantErr: true},
		"Error_if_GID_range_is_larger_than_max_signed_int32":      {gidMin: 0, gidMax: math.MaxInt32 + 1, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			destGroupFile := localgroupstestutils.SetupGroupMock(t,
				filepath.Join("testdata", "groups", "users_in_groups.group"))

			dbDir := t.TempDir()
			if tc.dbFile == "" {
				tc.dbFile = "multiple_users_and_groups"
			}
			if tc.dbFile == "-" {
				err := os.RemoveAll(dbDir)
				require.NoError(t, err, "Setup: could not remove temporary db directory")
			} else if tc.dbFile != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}
			if tc.corruptedDbFile {
				err := os.WriteFile(filepath.Join(dbDir, consts.DefaultDatabaseFileName), []byte("Corrupted db"), 0600)
				require.NoError(t, err, "Setup: Can't update the file with invalid db content")
			}

			config := users.DefaultConfig
			if tc.uidMin != 0 {
				config.UIDMin = tc.uidMin
			}
			if tc.uidMax != 0 {
				config.UIDMax = tc.uidMax
			}
			if tc.gidMin != 0 {
				config.GIDMin = tc.gidMin
			}
			if tc.gidMax != 0 {
				config.GIDMax = tc.gidMax
			}

			m, err := users.NewManager(config, dbDir)
			if tc.wantErr {
				t.Logf("Manager creation exited with %v", err)
				require.Error(t, err, "NewManager should return an error, but did not")
				return
			}
			require.NoError(t, err, "NewManager should not return an error, but did")

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)

			idGenerator := m.RealIDGenerator()

			require.Equal(t, int(config.UIDMin), int(idGenerator.UIDMin),
				"ID generator UIDMin has not the expected value")
			require.Equal(t, int(config.UIDMax), int(idGenerator.UIDMax),
				"ID generator UIDMax has not the expected value")
			require.Equal(t, int(config.GIDMin), int(idGenerator.GIDMin),
				"ID generator GIDMin has not the expected value")
			require.Equal(t, int(config.GIDMax), int(idGenerator.GIDMax),
				"ID generator GIDMax has not the expected value")

			localgroupstestutils.RequireGroupFile(t, destGroupFile, golden.Path(t))
		})
	}
}

func TestStop(t *testing.T) {
	dbDir := t.TempDir()
	m := newManagerForTests(t, dbDir)
	require.NoError(t, m.Stop(), "Stop should not return an error, but did")

	// Should fail, because the db is closed
	_, err := userstestutils.GetManagerDB(m).AllUsers()

	require.Error(t, err, "AllUsers should return an error, but did not")
}

type userCase struct {
	types.UserInfo
	UID uint32 // The UID to generate for this user
}

type groupCase struct {
	types.GroupInfo
	GID uint32 // The GID to generate for this group
}

func TestUpdateUser(t *testing.T) {
	userCases := map[string]userCase{
		"user1":                             {UserInfo: types.UserInfo{Name: "user1"}, UID: 1111},
		"nameless":                          {UID: 1111},
		"user2":                             {UserInfo: types.UserInfo{Name: "user2"}, UID: 2222},
		"same-name-different-uid":           {UserInfo: types.UserInfo{Name: "user1"}, UID: 3333},
		"different-name-same-uid":           {UserInfo: types.UserInfo{Name: "newuser1"}, UID: 1111},
		"different-capitalization-same-uid": {UserInfo: types.UserInfo{Name: "User1"}, UID: 1111},
		"user-exists-on-system":             {UserInfo: types.UserInfo{Name: "root"}, UID: 1111},
	}

	groupsCases := map[string][]groupCase{
		"authd-group":                {{GroupInfo: types.GroupInfo{Name: "group1", UGID: "1"}, GID: 11111}},
		"local-group":                {{GroupInfo: types.GroupInfo{Name: "localgroup1", UGID: ""}}},
		"authd-group-with-uppercase": {{GroupInfo: types.GroupInfo{Name: "Group1", UGID: "1"}, GID: 11111}},
		"mixed-groups-authd-first": {
			{GroupInfo: types.GroupInfo{Name: "group1", UGID: "1"}, GID: 11111},
			{GroupInfo: types.GroupInfo{Name: "localgroup1", UGID: ""}},
		},
		"mixed-groups-local-first": {
			{GroupInfo: types.GroupInfo{Name: "localgroup1", UGID: ""}},
			{GroupInfo: types.GroupInfo{Name: "group1", UGID: "1"}, GID: 11111},
		},
		"nameless-group":          {{GroupInfo: types.GroupInfo{Name: "", UGID: "1"}, GID: 11111}},
		"different-name-same-gid": {{GroupInfo: types.GroupInfo{Name: "newgroup1", UGID: "1"}, GID: 11111}},
		"group-exists-on-system":  {{GroupInfo: types.GroupInfo{Name: "root", UGID: "1"}, GID: 11111}},
		"no-groups":               {},
		// This group case has no GID to generate, because it's expected that the GID of the old group is re-used
		"different-name-same-ugid": {{GroupInfo: types.GroupInfo{Name: "renamed-group", UGID: "12345678"}}},
	}

	tests := map[string]struct {
		userCase   string
		groupsCase string

		dbFile          string
		localGroupsFile string

		wantErr     bool
		noOutput    bool
		wantSameUID bool
	}{
		"Successfully_update_user":                                          {groupsCase: "authd-group"},
		"Successfully_update_user_updating_local_groups":                    {groupsCase: "mixed-groups-authd-first", localGroupsFile: "users_in_groups.group"},
		"Successfully_update_user_updating_local_groups_with_changes":       {groupsCase: "mixed-groups-authd-first", localGroupsFile: "user_mismatching_groups.group"},
		"UID_does_not_change_if_user_already_exists":                        {userCase: "same-name-different-uid", dbFile: "one_user_and_group", wantSameUID: true},
		"GID_does_not_change_if_group_with_same_UGID_exists":                {groupsCase: "different-name-same-ugid", dbFile: "one_user_and_group"},
		"GID_does_not_change_if_group_with_same_name_and_empty_UGID_exists": {groupsCase: "authd-group", dbFile: "group-with-empty-UGID"},
		"Removing_last_user_from_a_group_keeps_the_group_record":            {groupsCase: "no-groups", dbFile: "one_user_and_group"},

		"Error_if_user_has_no_username":                           {userCase: "nameless", wantErr: true, noOutput: true},
		"Error_if_group_has_no_name":                              {groupsCase: "nameless-group", wantErr: true, noOutput: true},
		"Error_if_group_has_conflicting_gid":                      {groupsCase: "different-name-same-gid", dbFile: "one_user_and_group", wantErr: true, noOutput: true},
		"Error_if_group_with_same_name_but_different_UGID_exists": {groupsCase: "authd-group", dbFile: "one_user_and_group", wantErr: true, noOutput: true},
		"Error_if_user_exists_on_system":                          {userCase: "user-exists-on-system", wantErr: true, noOutput: true},
		"Error_if_group_exists_on_system":                         {groupsCase: "group-exists-on-system", wantErr: true, noOutput: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.localGroupsFile == "" {
				t.Parallel()
			}

			var destGroupFile string
			if tc.localGroupsFile != "" {
				destGroupFile = localgroupstestutils.SetupGroupMock(t,
					filepath.Join("testdata", "groups", tc.localGroupsFile))
			}

			if tc.userCase == "" {
				tc.userCase = "user1"
			}

			user := userCases[tc.userCase]
			user.Dir = "/home/" + user.Name
			user.Shell = "/bin/bash"
			user.Gecos = "gecos for " + user.Name
			for _, g := range groupsCases[tc.groupsCase] {
				user.Groups = append(user.Groups, g.GroupInfo)
			}

			dbDir := t.TempDir()
			if tc.dbFile != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}

			var gids []uint32
			for _, group := range groupsCases[tc.groupsCase] {
				if group.GID != 0 {
					gids = append(gids, group.GID)
				}
			}

			managerOpts := []users.Option{
				users.WithIDGenerator(&users.IDGeneratorMock{
					UIDsToGenerate: []uint32{user.UID},
					GIDsToGenerate: gids,
				}),
			}
			m := newManagerForTests(t, dbDir, managerOpts...)

			var oldUID uint32
			if tc.wantSameUID {
				oldUser, err := m.UserByName(user.Name)
				require.NoError(t, err, "UserByName should not return an error, but did")
				oldUID = oldUser.UID
			}

			err := m.UpdateUser(user.UserInfo)
			log.Debugf(context.Background(), "UpdateUser error: %v", err)

			requireErrorAssertions(t, err, nil, tc.wantErr)
			if tc.wantErr && tc.noOutput {
				return
			}

			if tc.wantSameUID {
				newUser, err := m.UserByName(user.Name)
				require.NoError(t, err, "UserByName should not return an error, but did")
				require.Equal(t, oldUID, newUser.UID, "UID should not have changed")
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, destGroupFile, golden.Path(t))
		})
	}
}

func TestRegisterUserPreauth(t *testing.T) {
	t.Parallel()

	userCases := map[string]userCase{
		"user1":                   {UserInfo: types.UserInfo{Name: "user1"}, UID: 1111},
		"nameless":                {UID: 1111},
		"same-name-different-uid": {UserInfo: types.UserInfo{Name: "user1"}, UID: 3333},
		"user-exists-on-system":   {UserInfo: types.UserInfo{Name: "root"}, UID: 1111},
	}

	tests := map[string]struct {
		userCase string

		dbFile string

		wantUserInDB bool
		wantErr      bool
	}{
		"Successfully_update_user": {},
		"Successfully_if_user_already_exists_on_db": {
			userCase: "same-name-different-uid", dbFile: "one_user_and_group", wantUserInDB: true,
		},

		"Error_if_user_has_no_username":  {userCase: "nameless", wantErr: true},
		"Error_if_user_exists_on_system": {userCase: "user-exists-on-system", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.userCase == "" {
				tc.userCase = "user1"
			}

			user := userCases[tc.userCase]

			dbDir := t.TempDir()
			if tc.dbFile != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}

			managerOpts := []users.Option{
				users.WithIDGenerator(&users.IDGeneratorMock{
					UIDsToGenerate: []uint32{user.UID},
				}),
			}
			m := newManagerForTests(t, dbDir, managerOpts...)

			uid, err := m.RegisterUserPreAuth(user.Name)

			requireErrorAssertions(t, err, nil, tc.wantErr)
			if tc.wantErr {
				return
			}

			_, err = m.UserByName(user.Name)
			if tc.wantUserInDB {
				require.NoError(t, err, "UserByName should not return an error, but did")
			} else {
				require.Error(t, err, "UserByName should return an error, but did not")
			}

			newUser, err := m.UserByID(uid)
			require.NoError(t, err, "UserByID should not return an error, but did")

			require.Equal(t, uid, newUser.UID, "UID should not have changed")

			if tc.wantUserInDB {
				require.Equal(t, user.Name, newUser.Name, "User name does not match")
			} else {
				require.True(t, strings.HasPrefix(newUser.Name, tempentries.UserPrefix),
					"Pre-auth users should have %q as prefix: %q", tempentries.UserPrefix,
					newUser.Name)
				newUser.Name = tempentries.UserPrefix + "-{{random-suffix}}"
			}

			golden.CheckOrUpdateYAML(t, newUser)
		})
	}
}

func TestConcurrentUserUpdate(t *testing.T) {
	t.Parallel()

	const nIterations = 100
	const preAuthIterations = 3
	const perUserGroups = 3
	const userUpdateRetries = 3

	dbDir := t.TempDir()
	const dbFile = "one_user_and_group_with_matching_gid"
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	const registeredUserPrefix = "authd-test-maybe-pre-check-user"

	lockedEntries, entriesUnlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Failed to lock the local entries")
	systemPasswd, err := lockedEntries.GetUserEntries()
	require.NoError(t, err, "GetPasswdEntries should not fail but it did")
	systemGroups, err := lockedEntries.GetGroupEntries()
	require.NoError(t, err, "GetGroupEntries should not fail but it did")

	err = entriesUnlock()
	require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")

	idGenerator := &users.IDGenerator{
		UIDMin: 0,
		//nolint: gosec // we're in tests, overflow is very unlikely to happen.
		UIDMax: uint32(len(systemPasswd)) + nIterations*preAuthIterations,
		GIDMin: 0,
		//nolint: gosec // we're in tests, overflow is very unlikely to happen.
		GIDMax: uint32(len(systemGroups)) + nIterations*perUserGroups,
	}
	m := newManagerForTests(t, dbDir, users.WithIDGenerator(idGenerator))

	originalDBUsers, err := m.AllUsers()
	require.NoError(t, err, "AllUsers should not fail but it did")
	originalDBGroups, err := m.AllGroups()
	require.NoError(t, err, "AllGroups should not fail but it did")

	wg := sync.WaitGroup{}
	wg.Add(nIterations)

	// These tests are meant to stress-test in parallel our users manager,
	// this is happening by updating new users or pre-auth some of them
	// using a very limited UID and GID set, to retry more their generation.
	// concurrently so that users gets registered first and then updated.
	// Finally ensure that the generated UIDs and GIDs are not clashing.
	for idx := range nIterations {
		t.Run(fmt.Sprintf("Iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			t.Logf("Running iteration %d", idx)

			idx := idx
			doPreAuth := idx%3 == 0
			userName := fmt.Sprintf("authd-test-user%d", idx)
			t.Cleanup(wg.Done)

			var preauthUID atomic.Uint32
			// var err error
			if doPreAuth {
				// In the pre-auth case we do even more parallelization, so that
				// the pre-auth happens without a defined order of the actual
				// registration.
				userName = fmt.Sprintf("%s%d", registeredUserPrefix, idx)

				//nolint:thelper // This is actually a test function!
				preAuth := func(t *testing.T) {
					t.Parallel()

					t.Logf("Registering pre-auth user %q", userName)
					uid, err := m.RegisterUserPreAuth(userName)
					require.NoError(t, err, "RegisterPreAuthUser should not fail but it did")
					preauthUID.Store(uid)
					t.Logf("Registered pre-auth user %q with UID %d", userName, uid)
				}

				for i := range preAuthIterations {
					t.Run(fmt.Sprintf("Pre_auth%d", i), preAuth)
				}
			}

			//nolint:thelper // This is actually a test function!
			userUpdate := func(t *testing.T) {
				t.Parallel()

				uid := preauthUID.Load()
				t.Logf("Updating user %q (using UID %d)", userName, uid)
				u := types.UserInfo{
					Name:   userName,
					UID:    uid,
					Dir:    "/home-prefixes/" + userName,
					Shell:  "/usr/sbin/nologin",
					Groups: []types.GroupInfo{{Name: fmt.Sprintf("authd-test-local-group%d", idx)}},
				}

				// One user group matching the user is automatically added by authd.
				for gdx := range perUserGroups - 1 {
					u.Groups = append(u.Groups, types.GroupInfo{
						Name: fmt.Sprintf("authd-test-group%d.%d", idx, gdx),
						UGID: fmt.Sprintf("authd-test-ugid%d.%d", idx, gdx),
					})
				}

				err := m.UpdateUser(u)
				require.NoError(t, err, "UpdateUser should not fail but it did")
				t.Logf("Updated user %q using UID %d", userName, uid)
			}

			testName := "Update_user"
			if doPreAuth {
				testName = "Maybe_finish_registration"
			}

			for i := range userUpdateRetries {
				t.Run(fmt.Sprintf("%s%d", testName, i), userUpdate)
			}
		})
	}

	for _, u := range systemPasswd {
		t.Run(fmt.Sprintf("Error_updating_user_%s", u.Name), func(t *testing.T) {
			t.Parallel()

			err := m.UpdateUser(types.UserInfo{
				Name:  u.Name,
				Dir:   "/home-prefixes/" + u.Name,
				Shell: "/usr/sbin/nologin",
			})
			require.Error(t, err, "Updating user %q must fail but it does not", u.Name)
		})
	}

	for idx, g := range systemGroups {
		t.Run(fmt.Sprintf("Error_updating_user_with_non_local_group_%s", g.Name), func(t *testing.T) {
			t.Parallel()

			userName := fmt.Sprintf("%s-with-invalid-groups%d", registeredUserPrefix, idx)
			err := m.UpdateUser(types.UserInfo{
				Name:  userName,
				Dir:   "/home-prefixes/" + g.Name,
				Shell: "/usr/sbin/nologin",
				Groups: []types.GroupInfo{{
					Name: g.Name,
					UGID: fmt.Sprintf("authd-test-ugid-for-%s", g.Name),
				}},
			})
			require.Error(t, err, "Updating user %q must fail but it does not", g.Name)
		})
	}

	t.Run("Database_checks", func(t *testing.T) {
		t.Parallel()

		// Wait for the other tests to be completed, not using t.Cleanup here
		// since this is actually a test.
		wg.Wait()

		// This includes the extra user that was already in the DB.
		users, err := m.AllUsers()
		require.NoError(t, err, "AllUsers should not fail but it did")
		require.Len(t, users, nIterations+1, "Number of registered users mismatch")

		// This includes the extra group that was already in the DB.
		groups, err := m.AllGroups()
		require.NoError(t, err, "AllGroups should not fail but it did")
		require.Len(t, groups, nIterations*3+1, "Number of registered groups mismatch")

		lockedEntries, entriesUnlock, err := localentries.WithUserDBLock()
		require.NoError(t, err, "Failed to lock the local entries")
		defer func() {
			err := entriesUnlock()
			require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
		}()

		localPasswd, err := lockedEntries.GetUserEntries()
		require.NoError(t, err, "GetPasswdEntries should not fail but it did")
		localGroups, err := lockedEntries.GetGroupEntries()
		require.NoError(t, err, "GetGroupEntries should not fail but it did")

		uniqueUIDs := make(map[uint32]types.UserEntry)
		uniqueGIDs := make(map[uint32]string)

		for _, u := range users {
			require.NotZero(t, u.UID, "No user should have the UID equal to zero, but %q has", u.Name)
			require.Equal(t, u.UID, u.GID, "GID does not match UID for user %q", u.Name)

			old, ok := uniqueUIDs[u.UID]
			require.False(t, ok,
				"UID %d must be unique across entries, but it's used both %q and %q",
				u.UID, u.Name, old)
			uniqueUIDs[u.UID] = u
			require.Equal(t, int(u.UID), int(u.GID), "User %q UID should match its GID", u.Name)

			if slices.ContainsFunc(originalDBUsers, func(dbU types.UserEntry) bool {
				return dbU.UID == u.UID && dbU.Name == u.Name
			}) {
				// Ignore the local user checks for users already in the DB.
				continue
			}

			require.GreaterOrEqual(t, u.UID, idGenerator.UIDMin,
				"Generated UID should be an ID greater or equal to the minimum")
			require.LessOrEqual(t, u.UID, idGenerator.UIDMax,
				"Generate UID should be an ID less or equal to the maximum")

			localgroups, err := m.DB().UserLocalGroups(u.UID)
			require.NoError(t, err, "UserLocalGroups for %q should not fail but it did", u.Name)
			require.Len(t, localgroups, 1,
				"Number of registered local groups for %q mismatch", u.Name)

			isLocal := slices.ContainsFunc(localPasswd, func(lu types.UserEntry) bool {
				return lu.UID == u.UID
			})
			require.False(t, isLocal, "UID %d for user %q should not be a local user ID but it is",
				u.UID, u.Name)
		}

		for _, g := range groups {
			require.NotZero(t, g.GID, "No group should have the GID equal to zero, but %q has", g.Name)

			old, ok := uniqueGIDs[g.GID]
			require.False(t, ok, "GID %d must be unique across entries, but it's used both %q and %q",
				g.GID, g.Name, old)
			uniqueGIDs[g.GID] = g.Name

			u, ok := uniqueUIDs[g.GID]
			if ok {
				require.Equal(t, int(g.GID), int(u.GID),
					"Group %q can only match its user, not to %q", g.Name, u.Name)
			}

			isLocal := slices.ContainsFunc(localGroups, func(lg types.GroupEntry) bool {
				return lg.GID == g.GID
			})
			require.False(t, isLocal, "GID %d for group %q should not be a local user GID but it is",
				g.GID, g.Name)

			if slices.ContainsFunc(originalDBGroups, func(dbU types.GroupEntry) bool {
				return dbU.GID == g.GID && dbU.Name == g.Name
			}) {
				// Ignore the local user checks for users already in the DB.
				continue
			}

			require.GreaterOrEqual(t, g.GID, idGenerator.GIDMin,
				"Generated GID should be an ID greater or equal to the minimum")
			require.LessOrEqual(t, g.GID, idGenerator.GIDMax,
				"Generate GID should be an ID less or equal to the maximum")
		}
	})
}

func TestUpdateWhenNoMoreIDsAreAvailable(t *testing.T) {
	t.Parallel()

	const maxIDs = uint32(10)

	tests := map[string]struct {
		idGenerator users.IDGeneratorIface
	}{
		"Errors_after_registering_the_max_amount_of_users_for_lower_IDs": {
			idGenerator: &users.IDGenerator{
				UIDMin: 0,
				UIDMax: 0 + maxIDs - 1,
				GIDMin: 0,
				GIDMax: 0 + maxIDs - 1,
			},
		},
		"Errors_after_registering_the_max_amount_of_users_for_highest_IDs": {
			idGenerator: &users.IDGenerator{
				UIDMin: math.MaxUint32 - maxIDs + 1,
				UIDMax: math.MaxUint32,
				GIDMin: math.MaxUint32 - maxIDs + 1,
				GIDMax: math.MaxUint32,
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			const dbFile = "one_user_and_group_with_matching_gid"
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir, users.WithIDGenerator(tc.idGenerator))

			// Let'ts fill the manager first...
			for idx := range maxIDs {
				userName := fmt.Sprintf("authd-test-lucky-user-%d", idx)
				t.Logf("Updating user %q", userName)

				err := m.UpdateUser(types.UserInfo{
					Name:  userName,
					Dir:   "/home-prefixes/" + userName,
					Shell: "/usr/sbin/nologin",
				})

				// We do not care about the return value now...
				t.Logf("UpdateUser for %q exited with %v", userName, err)
			}

			// Now try to add more users, we must fail for all of them.
			for idx := range maxIDs {
				t.Run(fmt.Sprintf("Adding_more_users%d", idx), func(t *testing.T) {
					t.Parallel()

					userName := fmt.Sprintf("authd-test-unlucky-user-%d", idx)
					t.Logf("Updating user %q", userName)

					err := m.UpdateUser(types.UserInfo{
						Name:  userName,
						Dir:   "/home-prefixes/" + userName,
						Shell: "/usr/sbin/nologin",
					})

					require.Error(t, err, "UpdateUser should have failed for %q", userName)
				})
			}
		})
	}
}

func TestBrokerForUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string
		dbFile   string

		wantBrokerID string
		wantErr      bool
		wantErrType  error
	}{
		"Successfully_get_broker_for_user":                     {username: "user1", dbFile: "multiple_users_and_groups", wantBrokerID: "broker-id"},
		"Return_no_broker_but_in_db_if_user_has_no_broker_yet": {username: "userwithoutbroker", dbFile: "multiple_users_and_groups", wantBrokerID: ""},

		"Error_if_user_does_not_exist": {username: "doesnotexist", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			brokerID, err := m.BrokerForUser(tc.username)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			require.Equal(t, tc.wantBrokerID, brokerID, "BrokerForUser should return the expected brokerID, but did not")
		})
	}
}

func TestUpdateBrokerForUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string

		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_update_broker_for_user": {},

		"Error_if_user_does_not_exist": {username: "doesnotexist", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.username == "" {
				tc.username = "user1"
			}
			if tc.dbFile == "" {
				tc.dbFile = "multiple_users_and_groups"
			}

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			err = m.UpdateBrokerForUser(tc.username, "ExampleBrokerID")

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

//nolint:dupl // This is not a duplicate test
func TestLockUser(t *testing.T) {
	tests := map[string]struct {
		username string

		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_lock_user": {},

		"Error_if_user_does_not_exist": {username: "doesnotexist", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// We don't care about the output of gpasswd in this test, but we still need to mock it.
			_ = localgroupstestutils.SetupGroupMock(t, filepath.Join("testdata", "groups", "empty.group"))

			if tc.username == "" {
				tc.username = "user1"
			}
			if tc.dbFile == "" {
				tc.dbFile = "multiple_users_and_groups"
			}

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			err = m.LockUser(tc.username)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

//nolint:dupl // This is not a duplicate test
func TestUnlockUser(t *testing.T) {
	tests := map[string]struct {
		username string

		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_enable_user": {},

		"Error_if_user_does_not_exist": {username: "doesnotexist", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// We don't care about the output of gpasswd in this test, but we still need to mock it.
			_ = localgroupstestutils.SetupGroupMock(t, filepath.Join("testdata", "groups", "empty.group"))

			if tc.username == "" {
				tc.username = "user1"
			}
			if tc.dbFile == "" {
				tc.dbFile = "locked_user"
			}

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			err = m.UnlockUser(tc.username)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestUserByIDAndName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		uid        uint32
		username   string
		dbFile     string
		isTempUser bool

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_user_by_ID":           {uid: 1111, dbFile: "multiple_users_and_groups"},
		"Successfully_get_user_by_name":         {username: "user1", dbFile: "multiple_users_and_groups"},
		"Successfully_get_temporary_user_by_ID": {dbFile: "multiple_users_and_groups", isTempUser: true},

		"Error_if_user_does_not_exist_-_by_ID":   {uid: 0, dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
		"Error_if_user_does_not_exist_-_by_name": {username: "doesnotexist", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			if tc.isTempUser {
				tc.uid, err = m.RegisterUserPreAuth("tempuser1")
				require.NoError(t, err, "RegisterUser should not return an error, but did")
			}

			var user types.UserEntry
			if tc.username != "" {
				user, err = m.UserByName(tc.username)
			} else {
				user, err = m.UserByID(tc.uid)
			}

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			// Registering a temporary user creates it with a random UID, GID, and gecos, so we have to make it
			// deterministic before comparing it with the golden file
			if tc.isTempUser {
				require.True(t, strings.HasPrefix(user.Name, tempentries.UserPrefix))
				user.Name = tempentries.UserPrefix + "{{random-suffix}}"
				require.Equal(t, tc.uid, user.UID)
				user.UID = 0
				require.Equal(t, tc.uid, user.GID)
				user.GID = 0
				require.NotEmpty(t, user.Gecos)
				user.Gecos = ""
			}

			golden.CheckOrUpdateYAML(t, user)
		})
	}
}

func TestAllUsers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_all_users": {dbFile: "multiple_users_and_groups"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			got, err := m.AllUsers()

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestGroupByIDAndName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		gid         uint32
		groupname   string
		dbFile      string
		preAuthUser string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_group_by_ID":                  {gid: 11111, dbFile: "multiple_users_and_groups"},
		"Successfully_get_group_by_ID_for_preauth_user": {preAuthUser: "hello-authd", dbFile: "multiple_users_and_groups"},
		"Successfully_get_group_by_name":                {groupname: "group1", dbFile: "multiple_users_and_groups"},

		"Error_if_group_does_not_exist_-_by_ID":   {gid: 0, dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
		"Error_if_group_does_not_exist_-_by_name": {groupname: "doesnotexist", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir, users.WithIDGenerator(&users.IDGeneratorMock{
				UIDsToGenerate: []uint32{12345},
				GIDsToGenerate: []uint32{12345},
			}))

			if tc.preAuthUser != "" {
				tc.gid, err = m.RegisterUserPreAuth(tc.preAuthUser)
				require.NoError(t, err, "RegisterUserPreAuth should not fail for %q, but it did",
					tc.preAuthUser)
			}

			var group types.GroupEntry
			if tc.groupname != "" {
				group, err = m.GroupByName(tc.groupname)
			} else {
				group, err = m.GroupByID(tc.gid)
			}

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			if tc.preAuthUser != "" {
				require.True(t, strings.HasPrefix(group.Name, tempentries.UserPrefix),
					"Pre-auth user group should have %q as prefix: %q", tempentries.UserPrefix,
					group.Name)
				group.Name = tempentries.UserPrefix + "-{{RANDOM_ID}}"

				require.Len(t, group.Users, 1, "Users length mismatch")
				require.True(t, strings.HasPrefix(group.Users[0], tempentries.UserPrefix),
					"Pre-auth user should have %q as prefix: %q", tempentries.UserPrefix,
					group.Users[0])
				group.Users[0] = tempentries.UserPrefix + "-{{RANDOM_ID}}"
			}

			golden.CheckOrUpdateYAML(t, group)
		})
	}
}

func TestAllGroups(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_all_groups": {dbFile: "multiple_users_and_groups"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			got, err := m.AllGroups()

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestShadowByName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string
		dbFile   string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_shadow_by_name": {username: "user1", dbFile: "multiple_users_and_groups"},

		"Error_if_shadow_does_not_exist": {username: "doesnotexist", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			got, err := m.ShadowByName(tc.username)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestAllShadows(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantErr bool
	}{
		"Successfully_get_all_users": {dbFile: "multiple_users_and_groups"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			got, err := m.AllShadows()

			requireErrorAssertions(t, err, nil, tc.wantErr)
			if tc.wantErr {
				return
			}

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestCompareNewUserInfoWithDB(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dbFile string

		wantUserExactMatch map[string]bool
		wantUserNoMatch    map[string]bool
	}{
		"Compare_all_valid_users": {
			dbFile:             "multiple_users_and_groups",
			wantUserExactMatch: map[string]bool{"user1": true},
		},
		"Compare_all_not_matching_users": {
			dbFile: "multiple_users_and_groups",
			wantUserNoMatch: map[string]bool{
				"user1": true, "user2": true, "user3": true, "userwithoutbroker": true,
			},
		},
	}
	for name, tc := range tests {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			userEntries, err := m.AllUsers()
			require.NoError(t, err, "AllUsers should not fail but it did")

			for _, u := range userEntries {
				t.Run(u.Name, func(t *testing.T) {
					t.Parallel()

					u, err := m.GetOldUserInfoFromDB(u.Name)
					require.NoError(t, err, "GetOldUserInfoFromDB should not fail but it did")
					require.NotNil(t, u, "GetOldUserInfoFromDB user should not be nil but it is")

					dbUserInfo := *u
					golden.CheckOrUpdateYAML(t, dbUserInfo,
						golden.WithSuffix("-from-getOldUserInfoFromDB"))

					userInfoFile := filepath.Join("testdata", t.Name())
					content, err := os.ReadFile(userInfoFile)
					require.NoError(t, err, "ReadFile should not fail opening %q", userInfoFile)

					var wantUserInfo types.UserInfo
					err = yaml.Unmarshal(content, &wantUserInfo)
					require.NoError(t, err, "Cannot deserialize user info")

					if tc.wantUserExactMatch[u.Name] {
						require.Equal(t, wantUserInfo, dbUserInfo,
							"User infos be strictly equal, but they are not")
						require.True(t, wantUserInfo.Equals(dbUserInfo),
							"User infos be strictly equal, but they are not")
					} else {
						require.NotEqual(t, wantUserInfo, dbUserInfo,
							"User infos should not be strictly equal, but they are")
						require.False(t, wantUserInfo.Equals(dbUserInfo),
							"User infos should not be strictly equal, but they are")
					}

					got := users.CompareNewUserInfoWithUserInfoFromDB(wantUserInfo, dbUserInfo)
					require.Equal(t, !tc.wantUserNoMatch[u.Name], got,
						"User infos does not respect wanted equality check:"+
							"\nNew: %#v\n Old: %#v", wantUserInfo, dbUserInfo)
				})
			}
		})

		t.Run("not_existing_user", func(t *testing.T) {
			t.Parallel()

			user, err := m.GetOldUserInfoFromDB("ImustNot-exist")
			require.NoError(t, err, "GetOldUserInfoFromDB should not fail but it did")
			require.Nil(t, user, "returned user should be nil, but it was not")
		})
	}
}

func TestRegisterUserPreAuthWhenLocked(t *testing.T) {
	// This cannot be parallel

	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, context.Background())
	userslocking.Z_ForTests_SetMaxWaitTime(t, testutils.MultipliedSleepDuration(750*time.Millisecond))

	dbFile := "one_user_and_group"
	dbDir := t.TempDir()
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	m := newManagerForTests(t, dbDir)

	uid, err := m.RegisterUserPreAuth("locked-user")
	require.ErrorIs(t, err, userslocking.ErrLock)
	require.Zero(t, uid, "Uid should be unset")
}

func TestRegisterUserPreAuthAfterUnlock(t *testing.T) {
	// This cannot be parallel

	waitTime := testutils.MultipliedSleepDuration(750 * time.Millisecond)
	lockCtx, lockCancel := context.WithTimeout(context.Background(), waitTime/2)
	t.Cleanup(lockCancel)

	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, lockCtx)
	userslocking.Z_ForTests_SetMaxWaitTime(t, waitTime)

	t.Cleanup(func() { _ = userslocking.WriteUnlock() })

	dbFile := "one_user_and_group"
	dbDir := t.TempDir()
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	m := newManagerForTests(t, dbDir)

	uid, err := m.RegisterUserPreAuth("locked-user")
	require.NoError(t, err, "Registration should not fail")
	require.NotZero(t, uid, "UID should be set")
}

func TestUpdateUserWhenLocked(t *testing.T) {
	// This cannot be parallel

	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, context.Background())
	userslocking.Z_ForTests_SetMaxWaitTime(t, testutils.MultipliedSleepDuration(750*time.Millisecond))

	dbFile := "one_user_and_group"
	dbDir := t.TempDir()
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	m := newManagerForTests(t, dbDir)

	err = m.UpdateUser(types.UserInfo{UID: 1234, Name: "test-user"})
	require.ErrorIs(t, err, userslocking.ErrLock)
}

func TestUpdateUserAfterUnlock(t *testing.T) {
	// This cannot be parallel

	waitTime := testutils.MultipliedSleepDuration(750 * time.Millisecond)
	lockCtx, lockCancel := context.WithTimeout(context.Background(), waitTime/2)
	t.Cleanup(lockCancel)

	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, lockCtx)
	userslocking.Z_ForTests_SetMaxWaitTime(t, waitTime)

	t.Cleanup(func() { _ = userslocking.WriteUnlock() })

	dbFile := "one_user_and_group"
	dbDir := t.TempDir()
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	m := newManagerForTests(t, dbDir)

	err = m.UpdateUser(types.UserInfo{UID: 1234, Name: "some-user-test"})
	require.NoError(t, err, "UpdateUser should not fail")
}

func requireErrorAssertions(t *testing.T, gotErr, wantErrType error, wantErr bool) {
	t.Helper()

	if wantErrType != nil {
		require.ErrorIs(t, gotErr, wantErrType, "Should return expected error")
		return
	}
	if wantErr {
		require.Error(t, gotErr, "Error should be returned")
		return
	}
	require.NoError(t, gotErr, "Error should not be returned")
}

func newManagerForTests(t *testing.T, dbDir string, opts ...users.Option) *users.Manager {
	t.Helper()

	m, err := users.NewManager(users.DefaultConfig, dbDir, opts...)
	require.NoError(t, err, "NewManager should not return an error, but did")

	return m
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	if testutils.RunningInBubblewrap() {
		m.Run()
		return
	}

	userslocking.Z_ForTests_OverrideLocking()
	defer userslocking.Z_ForTests_RestoreLocking()

	m.Run()
}
