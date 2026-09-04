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
		dbFile            string
		corruptedDbFile   bool
		uidMin            uint32
		uidMax            uint32
		gidMin            uint32
		gidMax            uint32
		useShortUsernames bool

		wantErr bool
	}{
		"Successfully_create_manager_with_default_config":                           {},
		"Successfully_create_manager_with_custom_config":                            {uidMin: 10000, uidMax: 20000, gidMin: 10000, gidMax: 20000, useShortUsernames: true},
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
			config.UseShortUsernames = tc.useShortUsernames

			m, err := users.NewManager(config, dbDir)
			if tc.wantErr {
				t.Logf("Manager creation exited with %v", err)
				require.Error(t, err, "NewManager should return an error, but did not")
				return
			}
			require.NoError(t, err, "NewManager should not return an error, but did")

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
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
	_, err := userstestutils.DBManager(m).AllUsers()

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
	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	userCases := map[string]userCase{
		"user1":                             {UserInfo: types.UserInfo{Name: "user1@example.com"}, UID: 1111},
		"nameless":                          {UID: 1111},
		"user2":                             {UserInfo: types.UserInfo{Name: "user2@example.com"}, UID: 2222},
		"same-name-different-uid":           {UserInfo: types.UserInfo{Name: "user1@example.com"}, UID: 3333},
		"different-name-same-uid":           {UserInfo: types.UserInfo{Name: "newuser1@example.com"}, UID: 1111},
		"different-capitalization-same-uid": {UserInfo: types.UserInfo{Name: "User1@example.com"}, UID: 1111},
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

		dbFile            string
		localGroupsFile   string
		useShortUsernames bool

		wantErr     bool
		noOutput    bool
		wantSameUID bool
	}{
		"Successfully_update_user":                                    {groupsCase: "authd-group"},
		"Successfully_update_user_updating_local_groups":              {groupsCase: "mixed-groups-authd-first", localGroupsFile: "users_in_groups.group"},
		"Successfully_update_user_updating_local_groups_with_changes": {groupsCase: "mixed-groups-authd-first", localGroupsFile: "user_mismatching_groups.group"},
		"Successfully_update_user_using_shortened_username":           {userCase: "user1", useShortUsernames: true, groupsCase: "authd-group"},
		// Users stored before short usernames were enabled carry no provider ID, so they can only
		// be identified by their fully qualified username. They must be renamed in place, keeping
		// their UID, rather than be taken for a brand new user.
		"Successfully_rename_existing_user_without_provider_ID_to_its_short_name": {userCase: "user1", useShortUsernames: true, dbFile: "one_user_and_group"},
		"UID_does_not_change_if_user_already_exists":                              {userCase: "same-name-different-uid", dbFile: "one_user_and_group", wantSameUID: true},
		"GID_does_not_change_if_group_with_same_UGID_exists":                      {groupsCase: "different-name-same-ugid", dbFile: "one_user_and_group"},
		"GID_does_not_change_if_group_with_same_name_and_empty_UGID_exists":       {groupsCase: "authd-group", dbFile: "group-with-empty-UGID"},
		"Removing_last_user_from_a_group_keeps_the_group_record":                  {groupsCase: "no-groups", dbFile: "one_user_and_group"},
		"Allow_login_with_existing_group_on_system":                               {groupsCase: "group-exists-on-system"},
		"User_private_group_GID_preserved_across_logins":                          {dbFile: "user_with_primary_group_gid_changed"},

		"Error_if_user_has_no_username":                           {userCase: "nameless", wantErr: true, noOutput: true},
		"Error_if_group_has_no_name":                              {groupsCase: "nameless-group", wantErr: true, noOutput: true},
		"Error_if_group_has_conflicting_gid":                      {groupsCase: "different-name-same-gid", dbFile: "one_user_and_group", wantErr: true, noOutput: true},
		"Error_if_group_with_same_name_but_different_UGID_exists": {groupsCase: "authd-group", dbFile: "one_user_and_group", wantErr: true, noOutput: true},
		"Error_if_user_exists_on_system":                          {userCase: "user-exists-on-system", wantErr: true, noOutput: true},
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

			managerCfg := users.DefaultConfig
			managerCfg.UseShortUsernames = tc.useShortUsernames
			m := newManagerForTestsWithConfig(t, managerCfg, dbDir, managerOpts...)

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

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, destGroupFile, golden.Path(t))
		})
	}
}

func TestUpdateUserProviderIDHandling(t *testing.T) {
	// This test and its subtests are intentionally not parallel: some subtests use SetupGroupMock,
	// which mutates the process-global localentries options. Running concurrently with other tests
	// that read those options (e.g. via UpdateUser) would race on them.

	newUser := func(name, providerID string, groups ...types.GroupInfo) types.UserInfo {
		return types.UserInfo{
			Name:       name,
			Gecos:      "gecos for " + name,
			Dir:        "/home/" + name,
			Shell:      "/bin/bash",
			BrokerID:   "broker-id",
			ProviderID: providerID,
			Groups:     groups,
		}
	}

	t.Run("Persist_providerid_on_first_post_migration_login", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		err = m.UpdateUser(newUser("user1@example.com", "providerid-user1"))
		require.NoError(t, err, "UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "UserByName should not return an error, but did")
		require.Equal(t, "providerid-user1", got.ProviderID, "provider ID should be persisted")
	})

	t.Run("Resolve_existing_user_by_providerid_and_rename", func(t *testing.T) {
		destGroupFile := localgroupstestutils.SetupGroupMock(t,
			filepath.Join("testdata", "groups", "single_localgroup_user1.group"))

		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group_with_providerid_and_local_group.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		oldUser, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "UserByName should not return an error, but did")

		err = m.UpdateUser(newUser("newuser1@example.com", "providerid-user1", types.GroupInfo{Name: "localgroup1", UGID: ""}))
		require.NoError(t, err, "UpdateUser should not return an error, but did")

		_, err = m.UserByName("user1@example.com")
		require.Error(t, err, "old username should no longer exist")

		renamed, err := userstestutils.DBManager(m).UserByName("newuser1@example.com")
		require.NoError(t, err, "new username should exist")
		require.Equal(t, oldUser.UID, renamed.UID, "UID should be preserved when renaming by provider ID")
		require.Equal(t, "providerid-user1", renamed.ProviderID, "provider ID should be preserved when renaming by provider ID")

		groupContent, err := os.ReadFile(destGroupFile)
		require.NoError(t, err, "could not read mocked group file")
		require.Equal(t, "localgroup1:x:41:newuser1@example.com\n", string(groupContent),
			"local group membership should be rewritten to the renamed user")
	})

	t.Run("Rename_onto_an_existing_different_username_fails_gracefully", func(t *testing.T) {
		// An IdP-side email change can land on a username that already belongs to a *different* user.
		// The provider-ID match authorises the rename and bypasses the "UID already in use" guard, so
		// without an explicit collision check the UPDATE hits the raw UNIQUE(name) constraint and
		// surfaces an opaque SQLite error. The rename must instead fail with a clear message and leave
		// both existing users intact.
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "two_users_with_providerid_and_local_group.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		// user1 (matched by providerid-user1) "renames" to newuser1@example.com, which is a different
		// existing user (uid 2222, providerid-newuser1).
		err = m.UpdateUser(newUser("newuser1@example.com", "providerid-user1"))
		require.Error(t, err, "renaming onto an existing different username must fail")
		require.Contains(t, err.Error(), "already in use by a different user",
			"the failure must be a clear message")
		require.NotContains(t, err.Error(), "UNIQUE constraint failed",
			"the raw SQLite constraint error must not leak to the caller")

		// Both original users must survive intact (no partial corruption from the failed rename).
		user1, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "the original user1 must still exist")
		require.Equal(t, "providerid-user1", user1.ProviderID, "user1 keeps its provider ID")
		require.Equal(t, uint32(1111), user1.UID, "user1 keeps its UID")

		newuser1, err := userstestutils.DBManager(m).UserByName("newuser1@example.com")
		require.NoError(t, err, "the colliding newuser1 must still exist")
		require.Equal(t, uint32(2222), newuser1.UID, "newuser1 keeps its UID")
	})

	t.Run("Preserve_locked_state_across_providerid_rename", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group_with_providerid.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		// An admin disables the account.
		err = m.LockUser("user1@example.com")
		require.NoError(t, err, "Setup: LockUser should not return an error")

		// The user's email changes at the IdP and they log in under the new name. The provider ID still
		// matches, so authd renames the existing (locked) row instead of creating a new user.
		err = m.UpdateUser(newUser("newuser1@example.com", "providerid-user1"))
		require.NoError(t, err, "UpdateUser should not return an error, but did")

		// The lock must survive the rename: a disabled account must not be silently re-enabled by an
		// IdP-side username change.
		renamed, err := userstestutils.DBManager(m).UserByName("newuser1@example.com")
		require.NoError(t, err, "new username should exist")
		require.True(t, renamed.Locked, "locked state must be preserved across a provider-ID rename")

		stillLocked, err := m.IsUserLocked("newuser1@example.com")
		require.NoError(t, err, "IsUserLocked should not return an error")
		require.True(t, stillLocked, "renamed user must remain locked")
	})

	t.Run("Keep_old_username_in_local_groups_when_rename_fails", func(t *testing.T) {
		destGroupFile := localgroupstestutils.SetupGroupMock(t,
			filepath.Join("testdata", "groups", "single_localgroup_user1.group"))

		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "two_users_with_providerid_and_local_group.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		// Renaming user1@example.com (matched by providerid-user1) to newuser1@example.com must fail,
		// because newuser1@example.com already exists as a different user. The DB update fails and
		// is rolled back, so the post-update cleanup must not strip the still-existing old user from
		// its local groups.
		err = m.UpdateUser(newUser("newuser1@example.com", "providerid-user1", types.GroupInfo{Name: "localgroup1", UGID: ""}))
		require.Error(t, err, "UpdateUser should return an error when the rename collides with an existing user")

		stillThere, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "old username should still exist after the failed rename")
		require.Equal(t, "providerid-user1", stillThere.ProviderID, "old user should keep its provider ID")

		// The group file must not have been mutated at all: no membership was rewritten or removed,
		// so the old user remains in localgroup1 exactly as before.
		require.NoFileExists(t, destGroupFile,
			"local groups must not be modified when the rename fails")
	})

	t.Run("Provider_ID_match_is_scoped_by_broker", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group_with_providerid.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir, users.WithIDGenerator(&users.IDGeneratorMock{
			UIDsToGenerate: []uint32{2222},
			GIDsToGenerate: []uint32{22222},
		}))

		userFromOtherBroker := newUser("newuser1@example.com", "providerid-user1")
		userFromOtherBroker.BrokerID = "other-broker-id"
		err = m.UpdateUser(userFromOtherBroker)
		require.NoError(t, err, "UpdateUser should not return an error, but did")

		oldUser, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "old username should still exist")
		require.Equal(t, "broker-id", oldUser.BrokerID, "old user should keep its broker ID")

		newUser, err := userstestutils.DBManager(m).UserByName("newuser1@example.com")
		require.NoError(t, err, "new username should exist")
		require.Equal(t, "other-broker-id", newUser.BrokerID, "new user should use its broker ID")
		require.Equal(t, "providerid-user1", newUser.ProviderID, "provider ID should be allowed under another broker")
	})

	t.Run("Preserve_providerid_when_broker_does_not_return_it", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group_with_providerid.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		err = m.UpdateUser(newUser("user1@example.com", ""))
		require.NoError(t, err, "UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "UserByName should not return an error, but did")
		require.Equal(t, "providerid-user1", got.ProviderID, "provider ID should be preserved when broker does not return it")
	})

	t.Run("Reject_login_with_a_different_broker", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group_with_providerid.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		userFromOtherBroker := newUser("user1@example.com", "providerid-user1")
		userFromOtherBroker.BrokerID = "other-broker-id"
		err = m.UpdateUser(userFromOtherBroker)
		require.Error(t, err, "UpdateUser should reject a login with a different broker")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "UserByName should not return an error, but did")
		require.Equal(t, "broker-id", got.BrokerID, "user should remain bound to its original broker")
	})

	t.Run("Rename_user_whose_private_group_has_ugid_equal_to_name", func(t *testing.T) {
		// When authd creates a user it prepends a private group {Name: username, UGID: username}.
		// On an IdP-side email rename the new private group arrives with {Name: newname, UGID: newname},
		// but the existing DB row has {UGID: oldname} under the same GID.  Without the fix in
		// handleGroupsUpdate this triggers a spurious "GID already in use" error because the UGID
		// change looks like a hijack.
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_with_private_group_ugid_equals_name.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir)

		err = m.UpdateUser(newUser("newuser1@example.com", "providerid-user1"))
		require.NoError(t, err, "UpdateUser should succeed when renaming a user whose private group has UGID == Name")

		_, err = m.UserByName("user1@example.com")
		require.Error(t, err, "old username should no longer exist after rename")

		_, err = m.UserByName("newuser1@example.com")
		require.NoError(t, err, "new username should exist after rename")
	})

	t.Run("Reject_new_user_without_provider_ID", func(t *testing.T) {
		dbDir := t.TempDir()
		m := newManagerForTests(t, dbDir)

		err := m.UpdateUser(newUser("brandnew@example.com", ""))
		require.Error(t, err, "UpdateUser should reject a new broker user without a provider ID")

		_, err = m.UserByName("brandnew@example.com")
		require.Error(t, err, "user without a provider ID should not have been created")
	})

	t.Run("Reject_provider_ID_without_broker_ID", func(t *testing.T) {
		dbDir := t.TempDir()
		m := newManagerForTests(t, dbDir)

		u := newUser("brandnew@example.com", "providerid-brandnew")
		u.BrokerID = ""
		err := m.UpdateUser(u)
		require.Error(t, err, "UpdateUser should reject a provider ID that is not scoped by a broker ID")
		require.Contains(t, err.Error(), "not scoped by a broker ID")

		_, err = m.UserByName("brandnew@example.com")
		require.Error(t, err, "user with unscoped provider ID should not have been created")
	})
}

func TestUserInfoStoredAs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name       string
		dir        string
		groups     []types.GroupInfo
		storedName string

		wantDir    string
		wantGroups []types.GroupInfo
	}{
		"Rewrites_the_name_derived_fields": {
			name: "user1@example.com", dir: "/home/user1@example.com", storedName: "user1",
			groups:     []types.GroupInfo{{Name: "user1@example.com", UGID: "ugid1"}, {Name: "group1", UGID: "ugid2"}},
			wantDir:    "/home/user1",
			wantGroups: []types.GroupInfo{{Name: "user1", UGID: "ugid1"}, {Name: "group1", UGID: "ugid2"}},
		},
		"Replaces_only_the_last_home_directory_element": {
			// The username names the home directory, but it may also appear in the path of the
			// directory holding it, which is not ours to rewrite.
			name: "user1@example.com", dir: "/srv/user1@example.com/homes/user1@example.com", storedName: "user1",
			wantDir: "/srv/user1@example.com/homes/user1",
		},
		"Leaves_a_home_directory_not_named_after_the_user_alone": {
			name: "user1@example.com", dir: "/home/shared", storedName: "user1",
			wantDir: "/home/shared",
		},
		"Leaves_an_empty_home_directory_alone": {
			name: "user1@example.com", storedName: "user1",
			wantDir: "",
		},
		"Is_a_no_op_when_the_name_does_not_change": {
			name: "user1@example.com", dir: "/home/user1@example.com", storedName: "user1@example.com",
			groups:     []types.GroupInfo{{Name: "user1@example.com", UGID: "ugid1"}},
			wantDir:    "/home/user1@example.com",
			wantGroups: []types.GroupInfo{{Name: "user1@example.com", UGID: "ugid1"}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reported := types.UserInfo{Name: tc.name, Dir: tc.dir, Groups: slices.Clone(tc.groups)}
			got := users.UserInfoStoredAs(reported, tc.storedName)

			require.Equal(t, tc.storedName, got.Name, "Unexpected stored name")
			require.Equal(t, tc.wantDir, got.Dir, "Unexpected home directory")
			require.Equal(t, tc.wantGroups, got.Groups, "Unexpected groups")

			// The caller owns the information it handed over: the group slice shares its backing
			// array with it, so rewriting a name in place would reach back into the caller.
			require.Equal(t, tc.name, reported.Name, "The reported name must not have been rewritten")
			require.Equal(t, tc.dir, reported.Dir, "The reported home directory must not have been rewritten")
			require.Equal(t, tc.groups, reported.Groups, "The reported groups must not have been rewritten")
		})
	}
}

func TestUpdateUserShortUsernameErrors(t *testing.T) {
	shortUsernamesConfig := users.DefaultConfig
	shortUsernamesConfig.UseShortUsernames = true

	newUser := func(name string, groups ...types.GroupInfo) types.UserInfo {
		return types.UserInfo{
			Name:       name,
			Dir:        "/home/" + name,
			Shell:      "/bin/bash",
			BrokerID:   "broker-id",
			ProviderID: "provider-id",
			Groups:     groups,
		}
	}

	t.Run("Error_when_resolving_the_existing_user_fails", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir())
		require.NoError(t, m.Stop(), "Setup: could not close database")

		err := m.UpdateUser(newUser("user@example.com"))
		require.ErrorContains(t, err, "failed to look up user by provider ID")
	})

	t.Run("Error_when_checking_the_short_name_fails", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		holder := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "other-provider-id", "other@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(holder, nil, nil),
			"Setup: could not add the short-name holder")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			`UPDATE users SET gid = 'invalid' WHERE name = 'user'`),
			"Setup: could not corrupt the short-name holder")

		err := m.UpdateUser(newUser("user@example.com"))
		require.ErrorContains(t, err, "query error")
	})

	t.Run("Error_when_loading_the_existing_user_fails", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("user@example.com", 1111, 1111, "", "/home/user@example.com",
			"/bin/bash", "broker-id", "provider-id", "user@example.com")
		privateGroup := db.NewGroupRow("user@example.com", 1111, "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow,
			[]db.GroupRow{privateGroup}, nil), "Setup: could not add existing user")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m), "DROP TABLE users_to_groups"),
			"Setup: could not remove the user-group relation table")

		err := m.UpdateUser(newUser("user@example.com"))
		require.ErrorContains(t, err, "users_to_groups")
	})

	t.Run("Error_when_an_identity_provider_group_conflicts", func(t *testing.T) {
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(
			filepath.Join("testdata", "db", "one_user_and_group.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTests(t, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{2222}}))
		t.Cleanup(func() { _ = m.Stop() })

		err = m.UpdateUser(newUser("user2@example.com",
			types.GroupInfo{Name: "group1@example.com", UGID: "different-group"}))
		require.ErrorContains(t, err, "different group with the same name")
	})
}

func TestUserEntryStoredAsErrors(t *testing.T) {
	shortUsernamesConfig := users.DefaultConfig
	shortUsernamesConfig.UseShortUsernames = true

	t.Run("Error_when_the_user_database_is_locked", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		require.NoError(t, userslocking.WriteLock(), "Setup: could not lock the user database")
		t.Cleanup(func() { _ = userslocking.WriteUnlock() })

		_, err := m.UserEntryStoredAs(types.UserEntry{Name: "user@example.com"})
		require.ErrorIs(t, err, userslocking.ErrLock)
	})

	t.Run("Error_when_checking_the_short_name_fails", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		holder := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "provider-id", "holder@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(holder, nil, nil),
			"Setup: could not add the short-name holder")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			`UPDATE users SET gid = 'invalid' WHERE name = 'user'`),
			"Setup: could not corrupt the short-name holder")

		_, err := m.UserEntryStoredAs(types.UserEntry{Name: "user@example.com"})
		require.ErrorContains(t, err, "query error")
	})
}

func TestShortUsernameLookupCompatibility(t *testing.T) {
	t.Run("NamesForLogin_falls_back_to_a_legacy_stored_name", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("legacy@example.com", 1111, 1111, "", "/home/legacy@example.com",
			"/bin/bash", "broker-id", "", "")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil),
			"Setup: could not add legacy user")

		storedName, fullUsername, err := m.NamesForLogin("legacy@example.com")
		require.NoError(t, err)
		require.Equal(t, "legacy@example.com", storedName)
		require.Equal(t, storedName, fullUsername)
	})

	t.Run("UserEntryStoredAs_keeps_a_name_held_by_a_legacy_user", func(t *testing.T) {
		config := users.DefaultConfig
		config.UseShortUsernames = true
		m := newManagerForTestsWithConfig(t, config, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		holder := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "", "")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(holder, nil, nil),
			"Setup: could not add legacy short-name holder")

		got, err := m.UserEntryStoredAs(types.UserEntry{
			Name: "user@example.com",
			Dir:  "/home/user@example.com",
		})
		require.NoError(t, err)
		require.Equal(t, "user@example.com", got.Name,
			"a short name held by another user must not be reused")
		require.Equal(t, "/home/user@example.com", got.Dir)
	})
}

func TestIsAuthenticatedUserLockedLookupErrors(t *testing.T) {
	t.Run("Error_when_the_full_username_lookup_fails", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		require.NoError(t, m.Stop(), "Setup: could not close database")

		_, err := m.IsAuthenticatedUserLocked("user@example.com", "", "")
		require.ErrorContains(t, err, "failed to look up user by full username")
	})

	t.Run("Error_when_the_legacy_stored_name_lookup_fails", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("legacy@example.com", 1111, 1111, "", "/home/legacy@example.com",
			"/bin/bash", "broker-id", "", "alias@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil),
			"Setup: could not add legacy user")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			`UPDATE users SET gid = 'invalid' WHERE name = 'legacy@example.com'`),
			"Setup: could not corrupt legacy user")

		_, err := m.IsAuthenticatedUserLocked("legacy@example.com", "", "")
		require.ErrorContains(t, err, "failed to look up user by stored name")
	})
}

func TestStoredGroupNameFallbacks(t *testing.T) {
	t.Run("Keep_the_requested_name_when_the_private_group_is_missing", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "provider-id", "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil),
			"Setup: could not add user without a private group")

		got, err := m.StoredGroupName("user@example.com")
		require.NoError(t, err)
		require.Equal(t, "user@example.com", got,
			"a missing private group should leave the requested name unchanged")
	})

	t.Run("Error_when_the_private_group_owner_lookup_fails", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "provider-id", "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil),
			"Setup: could not add user without a private group")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			`UPDATE users SET gid = 'invalid' WHERE name = 'user'`),
			"Setup: could not corrupt private group owner")

		_, err := m.StoredGroupName("user@example.com")
		require.ErrorContains(t, err, "query error")
	})

	t.Run("Error_when_the_private_group_lookup_fails", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash",
			"broker-id", "provider-id", "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil),
			"Setup: could not add user without a private group")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m), "PRAGMA foreign_keys = OFF"),
			"Setup: could not disable foreign keys")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m), "DROP TABLE groups"),
			"Setup: could not replace groups table")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			"CREATE TABLE groups (name TEXT, gid INT PRIMARY KEY, ugid TEXT)"),
			"Setup: could not create malformed groups table")
		require.NoError(t, db.Z_ForTests_Exec(userstestutils.DBManager(m),
			"INSERT INTO groups (name, gid, ugid) VALUES (NULL, 1111, 'user@example.com')"),
			"Setup: could not add malformed private group")

		_, err := m.StoredGroupName("user@example.com")
		require.ErrorContains(t, err, "query error")
	})
}

func TestShortUsernameAllowed(t *testing.T) {
	t.Parallel()

	defaultMgr := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = defaultMgr.Stop() })
	require.False(t, defaultMgr.ShortUsernameAllowed())

	cfg := users.DefaultConfig
	cfg.UseShortUsernames = true
	shortMgr := newManagerForTestsWithConfig(t, cfg, t.TempDir())
	t.Cleanup(func() { _ = shortMgr.Stop() })
	require.True(t, shortMgr.ShortUsernameAllowed())
}

func TestBrokerAndProviderIDForUser(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("user1@example.com", 1111, 1111, "", "/home/user1", "/bin/bash", "broker-id", "provider-123", "user1@example.com")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

	brokerID, providerID, err := m.BrokerAndProviderIDForUser("user1@example.com")
	require.NoError(t, err)
	require.Equal(t, "broker-id", brokerID)
	require.Equal(t, "provider-123", providerID)

	_, _, err = m.BrokerAndProviderIDForUser("nonexistent@example.com")
	require.ErrorIs(t, err, db.NoDataFoundError{})
}

func TestPrepareUserAliases(t *testing.T) {
	t.Parallel()

	t.Run("Validation_errors", func(t *testing.T) {
		t.Parallel()
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		_, err := m.PrepareUserAliases("lease1", types.UserInfo{Name: ""})
		require.ErrorContains(t, err, "empty username")

		_, err = m.PrepareUserAliases("lease1", types.UserInfo{
			Name:       "user@example.com",
			ProviderID: "prov-id",
			BrokerID:   "",
		})
		require.ErrorContains(t, err, "not scoped by a broker ID")
	})

	t.Run("Unknown_user_returns_no_aliases", func(t *testing.T) {
		t.Parallel()
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		aliases, err := m.PrepareUserAliases("lease1", types.UserInfo{Name: "unknown@example.com"})
		require.NoError(t, err)
		require.Nil(t, aliases)
	})

	t.Run("User_alias_prepared_when_short_usernames_enabled", func(t *testing.T) {
		t.Parallel()
		cfg := users.DefaultConfig
		cfg.UseShortUsernames = true
		m := newManagerForTestsWithConfig(t, cfg, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		userRow := db.NewUserRow("user@example.com", 1111, 1111, "", "/home/user@example.com", "/bin/bash", "broker-id", "prov-id", "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

		aliases, err := m.PrepareUserAliases("lease1", types.UserInfo{
			Name:       "user@example.com",
			BrokerID:   "broker-id",
			ProviderID: "prov-id",
		})
		require.NoError(t, err)
		require.Equal(t, []string{"user@example.com"}, aliases)
		require.True(t, m.HasUserAlias("lease1"))

		retained, err := m.RetainUserAlias("lease2", "user@example.com")
		require.NoError(t, err)
		require.True(t, retained)

		require.True(t, m.ReleaseUserAlias("lease1"))
		m.CancelUserAlias("lease1")
	})

	t.Run("Error_when_manager_is_stopped", func(t *testing.T) {
		t.Parallel()
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })
		userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash", "broker-id", "prov-id", "user@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))
		m.Z_ForTests_StopTemporaryAliases()

		_, err := m.PrepareUserAliases("lease1", types.UserInfo{
			Name:       "user@example.com",
			BrokerID:   "broker-id",
			ProviderID: "prov-id",
		})
		require.ErrorContains(t, err, "temporary user aliases have stopped")
	})
}

func TestUserEntryStoredAsSuccess(t *testing.T) {
	t.Parallel()

	t.Run("Unchanged_when_short_usernames_disabled", func(t *testing.T) {
		t.Parallel()
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		entry := types.UserEntry{
			Name: "user@example.com",
			Dir:  "/home/user@example.com",
		}
		got, err := m.UserEntryStoredAs(entry)
		require.NoError(t, err)
		require.Equal(t, entry, got)
	})

	t.Run("Renamed_when_short_usernames_enabled", func(t *testing.T) {
		t.Parallel()
		cfg := users.DefaultConfig
		cfg.UseShortUsernames = true
		m := newManagerForTestsWithConfig(t, cfg, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		entry := types.UserEntry{
			Name: "user@example.com",
			Dir:  "/home/user@example.com",
		}
		got, err := m.UserEntryStoredAs(entry)
		require.NoError(t, err)
		require.Equal(t, "user", got.Name)
		require.Equal(t, "/home/user", got.Dir)
	})
}

func TestNamesForLoginAndRetainAlias(t *testing.T) {
	t.Parallel()

	cfg := users.DefaultConfig
	cfg.UseShortUsernames = true
	m := newManagerForTestsWithConfig(t, cfg, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash", "broker-id", "prov-id", "user@example.com")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

	// By short name
	stored, full, retained, err := m.NamesForLoginAndRetainAlias("user", "lease1")
	require.NoError(t, err)
	require.Equal(t, "user", stored)
	require.Equal(t, "user@example.com", full)
	require.False(t, retained)

	// By full username
	stored, full, retained, err = m.NamesForLoginAndRetainAlias("user@example.com", "lease2")
	require.NoError(t, err)
	require.Equal(t, "user", stored)
	require.Equal(t, "user@example.com", full)
	require.False(t, retained)

	// By temporary alias
	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease3", "tempalias", 1111, "broker-id", "prov-id", "user@example.com"))

	stored, full, retained, err = m.NamesForLoginAndRetainAlias("tempalias", "lease4")
	require.NoError(t, err)
	require.Equal(t, "user", stored)
	require.Equal(t, "user@example.com", full)
	require.True(t, retained)

	// By temporary alias with empty leaseID (via NamesForLogin)
	stored, full, err = m.NamesForLogin("tempalias")
	require.NoError(t, err)
	require.Equal(t, "user", stored)
	require.Equal(t, "user@example.com", full)

	// Non-existent
	_, _, _, err = m.NamesForLoginAndRetainAlias("nonexistent@example.com", "lease5")
	require.ErrorIs(t, err, db.NoDataFoundError{})

	// Temporary alias pointing to a deleted user
	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease6", "orphanalias", 9999, "broker-id", "prov-id", "orphan@example.com"))
	_, _, retained, err = m.NamesForLoginAndRetainAlias("orphanalias", "lease7")
	require.ErrorIs(t, err, db.NoDataFoundError{})
	require.False(t, retained)

	// User with empty FullUsername in DB
	userNoFull := db.NewUserRow("nofull", 2222, 2222, "", "/home/nofull", "/bin/bash", "broker-id", "prov-2", "")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userNoFull, nil, nil))
	stored, full, err = m.NamesForLogin("nofull")
	require.NoError(t, err)
	require.Equal(t, "nofull", stored)
	require.Equal(t, "nofull", full)
}

func TestUserByTemporaryAliasEdgeCases(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("currentname", 1111, 1111, "", "/home/currentname", "/bin/bash", "broker-id", "prov-id-1", "user@example.com")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

	// Alias matches current name but provider ID differs
	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease1", "currentname", 1111, "broker-id", "mismatched-prov-id", "user@example.com"))
	stored, full, err := m.NamesForLogin("currentname")
	require.NoError(t, err)
	require.Equal(t, "currentname", stored)
	require.Equal(t, "user@example.com", full)

	// Alias matches UID but user in DB has different identity
	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease2", "differentname", 1111, "broker-id", "mismatched-prov-id", "other@example.com"))
	_, _, _, err = m.NamesForLoginAndRetainAlias("differentname", "lease3")
	require.ErrorIs(t, err, db.NoDataFoundError{})
}

func TestStoredGroupNameWithTemporaryAlias(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("user", 1111, 2222, "", "/home/user", "/bin/bash", "broker-id", "prov-id", "user@example.com")
	groupRow := db.NewGroupRow("user-pg", 2222, "user@example.com")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, []db.GroupRow{groupRow}, nil))

	// Direct group match
	got, err := m.StoredGroupName("user-pg")
	require.NoError(t, err)
	require.Equal(t, "user-pg", got)

	// By full username
	got, err = m.StoredGroupName("user@example.com")
	require.NoError(t, err)
	require.Equal(t, "user-pg", got)

	// By temporary alias
	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease1", "aliasuser", 1111, "broker-id", "prov-id", "user@example.com"))
	got, err = m.StoredGroupName("aliasuser")
	require.NoError(t, err)
	require.Equal(t, "user-pg", got)

	// Non-existent
	got, err = m.StoredGroupName("nonexistentgroup")
	require.NoError(t, err)
	require.Equal(t, "nonexistentgroup", got)
}

func TestTemporaryAliasInGroupMembership(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("user1", 1111, 2222, "", "/home/user1", "/bin/bash", "broker-id", "prov-id", "user1@example.com")
	groupRow := db.NewGroupRow("sharedgroup", 2222, "sharedgroup-ugid")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, []db.GroupRow{groupRow}, []string{"user1"}))

	require.NoError(t, m.Z_ForTests_AddTemporaryAlias("lease1", "user1-alias", 1111, "broker-id", "prov-id", "user1@example.com"))

	grpByName, err := m.GroupByName("sharedgroup")
	require.NoError(t, err)
	require.Contains(t, grpByName.Users, "user1")
	require.Contains(t, grpByName.Users, "user1-alias")

	grpByID, err := m.GroupByID(2222)
	require.NoError(t, err)
	require.Contains(t, grpByID.Users, "user1")
	require.Contains(t, grpByID.Users, "user1-alias")

	allGroups, err := m.AllGroups()
	require.NoError(t, err)
	var found *types.GroupEntry
	for _, g := range allGroups {
		if g.Name == "sharedgroup" {
			found = &g
			break
		}
	}
	require.NotNil(t, found)
	require.Contains(t, found.Users, "user1")
	require.Contains(t, found.Users, "user1-alias")

	usedGIDs, err := m.UsedGIDs()
	require.NoError(t, err)
	require.Contains(t, usedGIDs, uint32(2222))
}

func TestCleanupTemporaryAlias(t *testing.T) {
	t.Run("Reconcile_cleanups_and_remove_from_local_groups", func(t *testing.T) {
		m := newManagerForTests(t, t.TempDir())
		t.Cleanup(func() { _ = m.Stop() })

		// Non-existent alias cleanup is a no-op
		require.NoError(t, m.Z_ForTests_CleanupTemporaryAlias("nonexistent"))

		// User that stayed at old name (cleanup removed without group changes)
		userRow := db.NewUserRow("olduser", 1111, 1111, "", "/home/olduser", "/bin/bash", "broker", "prov", "olduser@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

		cleanupRecord := users.TemporaryAliasCleanupForTests{
			Name:            "olduser",
			UID:             1111,
			NewName:         "newuser",
			OldBrokerID:     "broker",
			OldProviderID:   "prov",
			OldFullUsername: "olduser@example.com",
			NewBrokerID:     "broker",
			NewProviderID:   "prov",
			NewFullUsername: "olduser@example.com",
		}
		require.NoError(t, m.Z_ForTests_AddJournalCleanup(cleanupRecord))
		require.NoError(t, m.Z_ForTests_CleanupTemporaryAlias("olduser"))

		// User renamed to newname: reconcile cleans up local groups
		userRow2 := db.NewUserRow("newuser", 1111, 1111, "", "/home/newuser", "/bin/bash", "broker", "prov", "olduser@example.com")
		require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow2, nil, nil))

		cleanupRecord2 := users.TemporaryAliasCleanupForTests{
			Name:            "olduser",
			UID:             1111,
			NewName:         "newuser",
			OldBrokerID:     "broker",
			OldProviderID:   "prov",
			OldFullUsername: "olduser@example.com",
			NewBrokerID:     "broker",
			NewProviderID:   "prov",
			NewFullUsername: "olduser@example.com",
		}
		require.NoError(t, m.Z_ForTests_AddJournalCleanup(cleanupRecord2))
		require.NoError(t, m.Z_ForTests_ReconcileTemporaryAliasCleanups())

		// Remove temporary alias from local groups
		require.True(t, m.Z_ForTests_RemoveTemporaryAliasFromLocalGroups("olduser"))
	})
}

func TestUserByFullUsername(t *testing.T) {
	t.Parallel()

	m := newManagerForTests(t, t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })

	userRow := db.NewUserRow("user", 1111, 1111, "", "/home/user", "/bin/bash", "broker", "prov", "user@example.com")
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow, nil, nil))

	entry, err := m.UserByFullUsername("user@example.com")
	require.NoError(t, err)
	require.Equal(t, "user", entry.Name)
	require.Equal(t, uint32(1111), entry.UID)

	_, err = m.UserByFullUsername("nonexistent@example.com")
	require.ErrorIs(t, err, db.NoDataFoundError{})
}

func TestRenameHomeDir(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", users.Z_ForTests_RenameHomeDir("", "old", "new"))
	require.Equal(t, "/home/other", users.Z_ForTests_RenameHomeDir("/home/other", "old", "new"))
	require.Equal(t, "/home/new", users.Z_ForTests_RenameHomeDir("/home/old", "old", "new"))
	require.Equal(t, "/custom/path/new", users.Z_ForTests_RenameHomeDir("/custom/path/old", "old", "new"))
}

func TestUpdateUserShortUsernames(t *testing.T) {
	// This test and its subtests are intentionally not parallel: they mutate the process-global
	// localentries options through the user manager, as TestUpdateUserProviderIDHandling does.

	newUser := func(name, providerID string, groups ...types.GroupInfo) types.UserInfo {
		return types.UserInfo{
			Name:       name,
			Gecos:      "gecos for " + name,
			Dir:        "/home/" + name,
			Shell:      "/bin/bash",
			BrokerID:   "broker-id",
			ProviderID: providerID,
			Groups:     groups,
		}
	}

	shortUsernamesConfig := users.DefaultConfig
	shortUsernamesConfig.UseShortUsernames = true

	systemCollisionCases := map[string]struct {
		existingUser bool
		systemUsers  []types.UserEntry
		systemGroups []types.GroupEntry
	}{
		"Keep_the_fully_qualified_name_when_a_system_user_has_the_short_name": {
			systemUsers: []types.UserEntry{{Name: "authd-name-collision"}},
		},
		"Keep_an_existing_fully_qualified_name_when_a_system_group_has_the_short_name": {
			existingUser: true,
			systemGroups: []types.GroupEntry{{Name: "authd-name-collision"}},
		},
	}
	for name, tc := range systemCollisionCases {
		t.Run(name, func(t *testing.T) {
			const (
				shortName    = "authd-name-collision"
				fullUsername = shortName + "@example.com"
				uid          = 1111
			)

			dbDir := t.TempDir()
			managerOpts := []users.Option{
				users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{uid}}),
			}
			m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir, managerOpts...)

			if tc.existingUser {
				userRow := db.NewUserRow(fullUsername, uid, uid, "gecos for "+fullUsername,
					"/home/"+fullUsername, "/bin/bash", "broker-id", "providerid-user", fullUsername)
				privateGroup := db.NewGroupRow(fullUsername, uid, fullUsername)
				require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow,
					[]db.GroupRow{privateGroup}, nil), "Setup: could not create existing user")
			}

			t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)
			localentries.Z_ForTests_SetUserDBEntries(tc.systemUsers, tc.systemGroups)

			preAuthEntry, err := m.UserEntryStoredAs(types.UserEntry{
				Name: fullUsername,
				Dir:  "/home/" + fullUsername,
			})
			require.NoError(t, err, "the pre-authentication entry should resolve")
			require.Equal(t, fullUsername, preAuthEntry.Name,
				"the pre-authentication entry should keep the fully qualified name")
			require.Equal(t, "/home/"+fullUsername, preAuthEntry.Dir,
				"the pre-authentication home should keep the fully qualified name")

			// Unlocking clears the cached system entries, as it does in production. Restore the
			// collision for the separate lock held during the database update.
			localentries.Z_ForTests_SetUserDBEntries(tc.systemUsers, tc.systemGroups)

			if tc.existingUser {
				aliases, err := m.PrepareUserAliases("lease", newUser(fullUsername, "providerid-user"))
				require.NoError(t, err)
				require.Equal(t, []string{fullUsername}, aliases)
			}
			require.NoError(t, m.UpdateUser(newUser(fullUsername, "providerid-user")),
				"the system name collision must not prevent authentication")
			if tc.existingUser {
				require.False(t, m.HasUserAlias("lease"),
					"a name that stays canonical must not remain registered as an alias")
			}

			got, err := userstestutils.DBManager(m).UserByName(fullUsername)
			require.NoError(t, err, "the user should keep their fully qualified name")
			require.Equal(t, uint32(uid), got.UID, "the user should keep their UID")

			_, err = userstestutils.DBManager(m).UserByName(shortName)
			require.Error(t, err, "the colliding short name must not be stored")

			groups, err := m.AllGroups()
			require.NoError(t, err, "AllGroups should not return an error")
			require.Len(t, groups, 1, "only the user private group should exist")
			require.Equal(t, fullUsername, groups[0].Name,
				"the private group should keep the fully qualified name")
		})
	}

	t.Run("Store_new_user_under_its_short_name", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir(),
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1")
		require.NoError(t, err, "the user should be stored under its short name")
		require.Equal(t, "user1@example.com", got.FullUsername, "the full username must be persisted")
		require.Equal(t, "/home/user1", got.Dir, "the home directory must follow the short name")

		_, err = m.UserByName("user1@example.com")
		require.Error(t, err, "the user must not be reachable through its full username")
	})

	t.Run("Store_username_verbatim_when_the_feature_is_disabled", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, users.DefaultConfig, t.TempDir(),
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "the user should be stored under the name reported by the broker")
		require.Equal(t, "user1@example.com", got.FullUsername,
			"the full username must match the stored name when the feature is disabled")
		require.Equal(t, "/home/user1@example.com", got.Dir, "the home directory must not be rewritten")
	})

	// Enabling the feature on an existing installation must migrate the user in place: the row is
	// renamed instead of duplicated, and both the UID and the private group GID are preserved so
	// that the home directory stays accessible. The identity is confirmed by the provider ID when
	// there is one, and by the fully qualified username otherwise, which is the only proof
	// available for users created before the brokers reported a provider ID.
	renameCases := map[string]struct {
		dbFile     string
		providerID string
	}{
		"Rename_existing_user_when_the_feature_is_enabled": {
			dbFile: "one_user_with_private_group_and_providerid.db.yaml", providerID: "providerid-user1",
		},
		"Rename_existing_user_without_a_provider_ID": {
			dbFile: "one_user_with_private_group_no_providerid.db.yaml",
		},
	}
	for name, tc := range renameCases {
		t.Run(name, func(t *testing.T) {
			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir)

			require.NoError(t, m.UpdateUser(newUser("user1@example.com", tc.providerID)),
				"UpdateUser should not return an error, but did")

			got, err := userstestutils.DBManager(m).UserByName("user1")
			require.NoError(t, err, "the user should have been renamed to its short name")
			require.Equal(t, uint32(1111), got.UID, "the UID must be preserved across the rename")
			require.Equal(t, uint32(1111), got.GID, "the private group GID must be preserved across the rename")
			require.Equal(t, "user1@example.com", got.FullUsername, "the full username must be persisted")
			require.Equal(t, "/home/user1@example.com", got.Dir,
				"the existing home directory must be kept, so that the user does not lose their data")

			_, err = userstestutils.DBManager(m).UserByName("user1@example.com")
			require.Error(t, err, "the old row must not be left behind")

			// The private group is identified by the fully qualified username, so it is renamed in
			// place rather than replaced by a new group with a freshly generated GID.
			groups, err := m.AllGroups()
			require.NoError(t, err, "AllGroups should not return an error, but did")
			require.Len(t, groups, 1, "no extra group should have been created")
			require.Equal(t, "user1", groups[0].Name, "the private group must follow the user name")
			require.Equal(t, uint32(1111), groups[0].GID, "the private group must keep its GID")
		})
	}

	t.Run("Keep_the_fully_qualified_name_when_the_short_one_is_taken", func(t *testing.T) {
		// Two fully qualified usernames can shorten to the same name. Both users must stay able to
		// log in: the first to claim the short name keeps it, and the other is stored under their
		// fully qualified name. Refusing the second one instead would deny them an account for as
		// long as the first one exists, and which of the two lost would come down to who happened
		// to log in first.
		dbDir := t.TempDir()
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111, 2222}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"Setup: UpdateUser should not return an error, but did")

		require.NoError(t, m.UpdateUser(newUser("user1@other.com", "providerid-other-user1")),
			"the second user must be created rather than refused")

		got, err := userstestutils.DBManager(m).UserByName("user1")
		require.NoError(t, err, "the first user must keep the short name")
		require.Equal(t, "user1@example.com", got.FullUsername, "the first user must keep the short name")
		require.Equal(t, uint32(1111), got.UID, "the first user must keep their UID")

		other, err := userstestutils.DBManager(m).UserByName("user1@other.com")
		require.NoError(t, err, "the second user must be stored under their fully qualified name")
		require.Equal(t, "user1@other.com", other.FullUsername, "the second user must keep their own identity")
		require.Equal(t, uint32(2222), other.UID, "the two users must be separate accounts")
	})

	t.Run("Keep_the_fully_qualified_name_of_a_pre_existing_user", func(t *testing.T) {
		// The same collision seen from the other side: a user authd already knows must never be
		// locked out because somebody else claimed their short name in the meantime.
		dbDir := t.TempDir()
		err := db.Z_ForTests_CreateDBFromYAML(
			filepath.Join("testdata", "db", "one_user_with_private_group_no_providerid.db.yaml"), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")

		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{2222}}))

		require.NoError(t, m.UpdateUser(newUser("user1@other.com", "providerid-other-user1")),
			"Setup: UpdateUser should not return an error, but did")

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"the pre-existing user must still be able to authenticate")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "the pre-existing user must keep their fully qualified name")
		require.Equal(t, uint32(1111), got.UID, "the pre-existing user must keep their UID")
	})

	t.Run("Accept_a_domain_change_of_the_same_user", func(t *testing.T) {
		// A user whose domain changes at the IdP keeps their short name, so the name they are
		// stored under looks taken by somebody else. The provider ID says otherwise, and the row
		// must be updated in place rather than rejected as a collision.
		dbDir := t.TempDir()
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"Setup: UpdateUser should not return an error, but did")

		require.NoError(t, m.UpdateUser(newUser("user1@other.com", "providerid-user1")),
			"UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1")
		require.NoError(t, err, "the user should still be stored under its short name")
		require.Equal(t, uint32(1111), got.UID, "the UID must be preserved across the domain change")
		require.Equal(t, uint32(1111), got.GID, "the private group GID must be preserved across the domain change")
		require.Equal(t, "user1@other.com", got.FullUsername, "the new full username must be persisted")

		_, err = m.UserByFullUsername("user1@example.com")
		require.Error(t, err, "the previous full username must no longer resolve")

		byNewFullUsername, err := m.UserByFullUsername("user1@other.com")
		require.NoError(t, err, "the user must be reachable through its new full username")
		require.Equal(t, "user1", byNewFullUsername.Name, "the entry must stay named after the short name")

		groups, err := m.AllGroups()
		require.NoError(t, err, "AllGroups should not return an error, but did")
		require.Len(t, groups, 1, "no extra group should have been created")
		require.Equal(t, "user1", groups[0].Name, "the private group must keep the user name")
		require.Equal(t, uint32(1111), groups[0].GID, "the private group must keep its GID")
	})

	t.Run("Keep_the_previous_full_username_during_a_domain_change", func(t *testing.T) {
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, t.TempDir(),
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))
		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")))

		renamedUser := newUser("user1@other.com", "providerid-user1")
		aliases, err := m.PrepareUserAliases("lease", renamedUser)
		require.NoError(t, err)
		require.Equal(t, []string{"user1@example.com"}, aliases)
		require.NoError(t, m.UpdateUser(renamedUser))

		previous, err := m.UserByName("user1@example.com")
		require.NoError(t, err, "the previous full username should remain resolvable during PAM")
		require.Equal(t, "user1@example.com", previous.Name)
		require.Equal(t, uint32(1111), previous.UID)
	})

	t.Run("Keep_the_fully_qualified_name_when_a_rename_target_is_taken", func(t *testing.T) {
		// The provider ID identifies the user being renamed, but the short form of their new
		// username belongs to somebody else. The rename still has to go through, under the fully
		// qualified name, leaving the short one with the user who already holds it.
		dbDir := t.TempDir()
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111, 2222}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"Setup: UpdateUser should not return an error, but did")
		require.NoError(t, m.UpdateUser(newUser("user2@other.com", "providerid-user2")),
			"Setup: UpdateUser should not return an error, but did")

		require.NoError(t, m.UpdateUser(newUser("user2@example.com", "providerid-user1")),
			"the renamed user must still be able to authenticate")

		renamed, err := userstestutils.DBManager(m).UserByName("user2@example.com")
		require.NoError(t, err, "the renamed user must be stored under their fully qualified name")
		require.Equal(t, uint32(1111), renamed.UID, "the renamed user must keep their UID")

		got, err := userstestutils.DBManager(m).UserByName("user2")
		require.NoError(t, err, "the other user must still exist")
		require.Equal(t, "user2@other.com", got.FullUsername, "the other user must keep the short name")
		require.Equal(t, uint32(2222), got.UID, "the other user must keep their UID")
	})

	t.Run("Rename_back_when_the_feature_is_disabled_again", func(t *testing.T) {
		// Turning the feature off must be as reversible as turning it on: the provider ID still
		// resolves the shortened row, which is renamed back to the fully qualified username without
		// losing its UID or its private group GID.
		dbDir := t.TempDir()
		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"Setup: UpdateUser should not return an error, but did")
		require.NoError(t, m.Stop(), "Setup: the manager should stop cleanly")

		m = newManagerForTests(t, dbDir)

		require.NoError(t, m.UpdateUser(newUser("user1@example.com", "providerid-user1")),
			"UpdateUser should not return an error, but did")

		got, err := userstestutils.DBManager(m).UserByName("user1@example.com")
		require.NoError(t, err, "the user should have been renamed back to its full name")
		require.Equal(t, uint32(1111), got.UID, "the UID must be preserved across the rename")
		require.Equal(t, uint32(1111), got.GID, "the private group GID must be preserved across the rename")

		_, err = userstestutils.DBManager(m).UserByName("user1")
		require.Error(t, err, "the shortened row must not be left behind")

		groups, err := m.AllGroups()
		require.NoError(t, err, "AllGroups should not return an error, but did")
		require.Len(t, groups, 1, "no extra group should have been created")
		require.Equal(t, "user1@example.com", groups[0].Name, "the private group must follow the user name")
		require.Equal(t, uint32(1111), groups[0].GID, "the private group must keep its GID")
	})

	t.Run("Temporary_alias_tracks_current_local_groups", func(t *testing.T) {
		destGroupFile := localgroupstestutils.SetupGroupMock(t,
			filepath.Join("testdata", "groups", "empty_local_group.group"))
		dbDir := t.TempDir()
		user := newUser("user1@example.com", "providerid-user1",
			types.GroupInfo{Name: "localgroup1"})

		m := newManagerForTestsWithConfig(t, shortUsernamesConfig, dbDir,
			users.WithIDGenerator(&users.IDGeneratorMock{UIDsToGenerate: []uint32{1111}}))
		require.NoError(t, m.UpdateUser(user))
		require.NoError(t, m.Stop())

		m = newManagerForTests(t, dbDir)
		aliases, err := m.PrepareUserAliases("lease", user)
		require.NoError(t, err)
		require.Equal(t, []string{"user1"}, aliases)
		require.NoError(t, m.UpdateUser(user))

		groupData, err := os.ReadFile(destGroupFile)
		require.NoError(t, err)
		require.Contains(t, string(groupData), "localgroup1:x:41:user1@example.com,user1",
			"the old alias should have the user's current local groups")
		journalPath := filepath.Join(dbDir, "temporary-user-aliases.json")
		require.FileExists(t, journalPath, "temporary local-group memberships should be journaled")
		err = m.UpdateUser(newUser("user1", "providerid-other"))
		require.ErrorContains(t, err, `username "user1" is temporarily reserved by another user`)

		require.NoError(t, os.WriteFile(destGroupFile, []byte("localgroup1:x:41:user1\n"), 0600),
			"Setup: simulate a crash before the canonical local-group membership was written")
		localentries.Z_ForTests_SetGroupPath(destGroupFile, destGroupFile)
		require.NoError(t, m.Z_ForTests_Crash(), "simulated crash should close the database")
		m = newManagerForTests(t, dbDir)
		require.NotNil(t, m)
		groupData, err = os.ReadFile(destGroupFile)
		require.NoError(t, err)
		require.Equal(t, "localgroup1:x:41:user1@example.com", strings.TrimSpace(string(groupData)),
			"startup recovery should clean up the temporary alias")
		require.NoFileExists(t, journalPath, "startup recovery should remove the alias journal")
	})
}

func TestIsAuthenticatedUserLockedWithEmptyFullUsername(t *testing.T) {
	t.Parallel()

	const username = "legacy@example.com"

	m := newManagerForTests(t, t.TempDir())
	userRow := db.NewUserRow(username, 1111, 1111, "Legacy user", "/home/"+username,
		"/bin/bash", "broker-id", "", "")
	privateGroup := db.NewGroupRow(username, 1111, username)
	require.NoError(t, userstestutils.DBManager(m).UpdateUserEntry(userRow,
		[]db.GroupRow{privateGroup}, nil), "Setup: could not create the downgraded user")
	require.NoError(t, m.LockUser(username), "Setup: could not lock the downgraded user")

	locked, err := m.IsAuthenticatedUserLocked(username, "broker-id", "")
	require.NoError(t, err, "the downgraded user should resolve by their stored name")
	require.True(t, locked, "the account lock must survive an empty full_username value")
}

func TestRegisterUserPreauth(t *testing.T) {
	t.Parallel()

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	userCases := map[string]userCase{
		"user1":                   {UserInfo: types.UserInfo{Name: "user1@example.com"}, UID: 1111},
		"nameless":                {UID: 1111},
		"same-name-different-uid": {UserInfo: types.UserInfo{Name: "user1@example.com"}, UID: 3333},
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

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

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
		UIDMax: uint32(len(systemPasswd)) + nIterations*preAuthIterations + uint32(len(systemGroups)),
		GIDMin: 0,
		//nolint: gosec // we're in tests, overflow is very unlikely to happen.
		GIDMax: uint32(len(systemGroups)) + nIterations*perUserGroups + uint32(len(systemGroups)),
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
			userName := fmt.Sprintf("authd-test-user%d@example.com", idx)
			t.Cleanup(wg.Done)

			var preauthUID atomic.Uint32
			// var err error
			if doPreAuth {
				// In the pre-auth case we do even more parallelization, so that
				// the pre-auth happens without a defined order of the actual
				// registration.
				userName = fmt.Sprintf("%s%d@example.com", registeredUserPrefix, idx)

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

	// Test that adding users with the same name as system users fails
	for _, u := range systemPasswd {
		t.Run(fmt.Sprintf("Error_updating_user_with_name_conflict_%s", u.Name), func(t *testing.T) {
			t.Parallel()

			err := m.UpdateUser(types.UserInfo{
				Name:  u.Name,
				Dir:   "/home-prefixes/" + u.Name,
				Shell: "/usr/sbin/nologin",
			})
			require.Error(t, err, "Updating user %q must fail but it does not", u.Name)
		})
	}

	// Test that adding users with groups with the same name as local groups does not fail (we print a warning instead).
	for idx, g := range systemGroups {
		t.Run(fmt.Sprintf("Allow_updating_user_with_group_name_conflict_%s", g.Name), func(t *testing.T) {
			t.Parallel()

			userName := fmt.Sprintf("%s-with-group-name-conflict%d@example.com", registeredUserPrefix, idx)
			err := m.UpdateUser(types.UserInfo{
				Name:  userName,
				Dir:   "/home-prefixes/" + g.Name,
				Shell: "/usr/sbin/nologin",
				Groups: []types.GroupInfo{{
					Name: g.Name,
					UGID: fmt.Sprintf("authd-test-ugid-for-%s", g.Name),
				}, {
					Name: fmt.Sprintf("authd-test-local-group%d", idx),
				}},
			})
			// UpdateUser call should pass although the user would not be added to the system group
			require.NoError(t, err, "Updating user %q with group name conflict %q should not fail but it did", userName, g.Name)
		})
	}

	t.Run("Database_checks", func(t *testing.T) {
		t.Parallel()

		// Wait for the other tests to be completed, not using t.Cleanup here
		// since this is actually a test.
		wg.Wait()

		// This includes the extra user that was already in the DB and the
		// users registered via non-local groups loop.
		users, err := m.AllUsers()
		require.NoError(t, err, "AllUsers should not fail but it did")
		require.Len(t, users, nIterations+1+len(systemGroups), "Number of registered users mismatch")

		// This includes the extra group that was already in the DB and the
		// private groups for users registered via the non-local groups loop.
		groups, err := m.AllGroups()
		require.NoError(t, err, "AllGroups should not fail but it did")
		require.Len(t, groups, nIterations*3+1+len(systemGroups), "Number of registered groups mismatch")

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
			// The GID of user private groups is set to the same value as the UID, even if GIDMax is smaller,
			// so we need to check that the generated GID is less or equal to the maximum between GIDMax and UIDMax.
			gidMax := max(idGenerator.GIDMax, idGenerator.UIDMax)
			require.LessOrEqual(t, g.GID, gidMax,
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
		"Successfully_get_broker_for_user":                     {username: "user1@example.com", dbFile: "multiple_users_and_groups", wantBrokerID: "broker-id"},
		"Return_no_broker_but_in_db_if_user_has_no_broker_yet": {username: "userwithoutbroker@example.com", dbFile: "multiple_users_and_groups", wantBrokerID: ""},

		"Error_if_user_does_not_exist": {username: "doesnotexist@example.com", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
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
				tc.username = "user1@example.com"
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

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestDeleteUser(t *testing.T) {
	tests := map[string]struct {
		username string
		dbFile   string

		localGroupsFile string
		removeHome      bool

		wantErr     bool
		wantErrType error
	}{
		"Successfully_delete_user":                                   {},
		"Successfully_delete_user_removes_them_from_local_groups":    {localGroupsFile: "users_in_groups.group"},
		"Successfully_delete_user_keeps_other_users_in_shared_group": {username: "user2@example.com"},
		"Successfully_delete_user_keeps_primary_group_if_other_users_still_use_it": {
			username: "user1@example.com",
			dbFile:   "multiple_users_shared_primary_group_with_tmp_home",
		},
		"Successfully_delete_user_and_remove_home": {removeHome: true},

		"Error_if_user_does_not_exist": {username: "doesnotexist@example.com", wantErrType: db.NoDataFoundError{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			groupFile := tc.localGroupsFile
			if tc.localGroupsFile == "" {
				groupFile = "empty.group"
			}
			destGroupFile := localgroupstestutils.SetupGroupMock(t, filepath.Join("testdata", "groups", groupFile))

			if tc.username == "" {
				tc.username = "user1@example.com"
			}

			dbDir := t.TempDir()
			dbFile := tc.dbFile
			if dbFile == "" {
				dbFile = "multiple_users_and_groups_with_tmp_home"
			}
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			var userHome string
			if tc.username != "doesnotexist@example.com" {
				user, err := m.UserByName(tc.username)
				require.NoError(t, err, "Setup: could not look up user")
				userHome = user.Dir
				if userHome != "" {
					err = os.MkdirAll(userHome, 0o700)
					require.NoError(t, err, "Setup: could not create home directory for %s", tc.username)
				}
			}
			// We expect db file to have user home directories under
			// /tmp/authd-delete-user-test to keep the cleanup logic simple
			t.Cleanup(func() { _ = os.RemoveAll("/tmp/authd-delete-user-test/") })

			err = m.DeleteUser(tc.username, tc.removeHome)
			log.Debugf(context.Background(), "DeleteUser error: %v", err)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			if tc.removeHome {
				require.NoDirExists(t, userHome, "Home directory should have been removed")
			} else {
				require.DirExists(t, userHome, "Home directory should still exist")
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, destGroupFile, golden.Path(t))
		})
	}
}

func TestDeleteGroup(t *testing.T) {
	tests := map[string]struct {
		groupname string
		dbFile    string

		wantErr     bool
		wantErrType error
	}{
		"Successfully_delete_group_keeps_its_members_in_the_db":       {groupname: "nonprimarygroup", dbFile: "multiple_users_and_groups_with_non_primary_group"},
		"Successfully_delete_shared_group_leaves_other_groups_intact": {groupname: "commongroup", dbFile: "multiple_users_and_groups"},

		"Error_if_group_does_not_exist":                       {groupname: "doesnotexist", dbFile: "multiple_users_and_groups", wantErrType: db.NoDataFoundError{}},
		"Error_if_group_is_primary_group_of_an_existing_user": {groupname: "group1", dbFile: "multiple_users_and_groups", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// We don't care about the output of gpasswd in this test, but we still need to mock it.
			_ = localgroupstestutils.SetupGroupMock(t, filepath.Join("testdata", "groups", "empty.group"))

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", tc.dbFile+".db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			err = m.DeleteGroup(tc.groupname)
			log.Debugf(context.Background(), "DeleteGroup error: %v", err)

			requireErrorAssertions(t, err, tc.wantErrType, tc.wantErr)
			if tc.wantErrType != nil || tc.wantErr {
				return
			}

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestUsersWithPrimaryGroup(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		gid uint32

		wantErr bool
	}{
		"Returns_users_for_which_the_group_is_primary":             {gid: 11111},
		"Returns_empty_slice_when_no_user_has_it_as_primary":       {gid: 88888},
		"Returns_empty_slice_for_shared_group_that_is_not_primary": {gid: 99999},
		"Returns_multiple_users_when_group_is_primary_for_several": {gid: 55555},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "group_primary_check.db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")
			m := newManagerForTests(t, dbDir)

			got, err := m.UsersWithPrimaryGroup(tc.gid)

			requireErrorAssertions(t, err, nil, tc.wantErr)
			if tc.wantErr {
				return
			}

			golden.CheckOrUpdateYAML(t, got)
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
				tc.username = "user1@example.com"
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

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
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
				tc.username = "user1@example.com"
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

			got, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.DBManager(m))
			require.NoError(t, err, "Created database should be valid yaml content")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestUserByIDAndName(t *testing.T) {
	t.Parallel()

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	tests := map[string]struct {
		uid        uint32
		username   string
		dbFile     string
		isTempUser bool

		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_user_by_ID":           {uid: 1111, dbFile: "multiple_users_and_groups"},
		"Successfully_get_user_by_name":         {username: "user1@example.com", dbFile: "multiple_users_and_groups"},
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
				tc.uid, err = m.RegisterUserPreAuth("tempuser1@example.com")
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

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

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
		"Successfully_get_shadow_by_name": {username: "user1@example.com", dbFile: "multiple_users_and_groups"},

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
			wantUserExactMatch: map[string]bool{"user1@example.com": true},
		},
		"Compare_all_not_matching_users": {
			dbFile: "multiple_users_and_groups",
			wantUserNoMatch: map[string]bool{
				"user1@example.com": true, "user2@example.com": true, "user3@example.com": true, "userwithoutbroker@example.com": true,
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
				testName, _, _ := strings.Cut(u.Name, "@")
				t.Run(testName, func(t *testing.T) {
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

func TestDiffNewUserInfoWithDBNormalizesMatchingGroupsWhenGroupCountsDiffer(t *testing.T) {
	t.Parallel()

	userPrivateGroupGID := uint32(11111)
	existingGroupGID := uint32(22222)

	dbUserInfo := types.UserInfo{
		Name:  "user1@example.com",
		UID:   1111,
		Gecos: "User1 gecos",
		Dir:   "/home/user1@example.com",
		Shell: "/bin/bash",
		Groups: []types.GroupInfo{
			{Name: "user1@example.com", UGID: "user1@example.com", GID: &userPrivateGroupGID},
			{Name: "group1@example.com", UGID: "12345678", GID: &existingGroupGID},
		},
	}

	newUserInfo := types.UserInfo{
		Name:  "user1@example.com",
		Gecos: "User1 gecos",
		Dir:   "/home/user1@example.com",
		Shell: "/bin/bash",
		Groups: []types.GroupInfo{
			{Name: "user1@example.com", UGID: "user1@example.com"},
			{Name: "group1@example.com", UGID: "12345678"},
			{Name: "group2@example.com", UGID: "87654321"},
		},
	}

	got := users.DiffNewUserInfoWithUserInfoFromDB(newUserInfo, dbUserInfo)

	require.Equal(t, []string{`group "group2@example.com" added`}, got)
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

	uid, err := m.RegisterUserPreAuth("locked-user@example.com")
	require.ErrorIs(t, err, userslocking.ErrLock)
	require.Zero(t, uid, "Uid should be unset")
}

func TestRegisterUserPreAuthAfterUnlock(t *testing.T) {
	// This cannot be parallel

	// This test is flaky
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

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

	uid, err := m.RegisterUserPreAuth("locked-user@example.com")
	require.NoError(t, err, "Registration should not fail")
	require.NotZero(t, uid, "UID should be set")
}

func TestUpdateUserWhenLocked(t *testing.T) {
	// This cannot be parallel

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	userslocking.Z_ForTests_OverrideLockingAsLockedExternally(t, context.Background())
	userslocking.Z_ForTests_SetMaxWaitTime(t, testutils.MultipliedSleepDuration(750*time.Millisecond))

	dbFile := "one_user_and_group"
	dbDir := t.TempDir()
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", dbFile+".db.yaml"), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	m := newManagerForTests(t, dbDir)

	err = m.UpdateUser(types.UserInfo{UID: 1234, Name: "test-user@example.com"})
	require.ErrorIs(t, err, userslocking.ErrLock)
}

func TestUpdateUserAfterUnlock(t *testing.T) {
	// This cannot be parallel

	// This test is flaky, see https://github.com/canonical/authd/issues/1120
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

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

	err = m.UpdateUser(types.UserInfo{UID: 1234, Name: "some-user-test@example.com"})
	require.NoError(t, err, "UpdateUser should not fail")
}

func TestSetShell(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		nonExistentUser bool
		emptyUsername   bool
		emptyShell      bool
		shell           string

		wantWarnings int
		wantErr      bool
	}{
		"Successfully_set_shell": {},

		"Warning_if_shell_is_not_in_etc_shells": {
			shell:        "/bin/ls",
			wantWarnings: 1,
		},
		"Warning_if_shell_does_not_exist": {
			shell:        "/doesnotexist",
			wantWarnings: 1,
		},
		"Warning_if_shell_is_directory": {
			shell:        "/etc",
			wantWarnings: 1,
		},
		"Warning_if_shell_is_not_executable": {
			shell:        "/etc/passwd",
			wantWarnings: 1,
		},

		// checkValidPasswdField error cases
		"Error_if_shell_is_empty": {
			emptyShell: true,
			wantErr:    true,
		},
		"Error_if_shell_contains_invalid_utf8": {
			shell:   "/bin/\xff\xfeinvalid",
			wantErr: true,
		},
		"Error_if_shell_contains_colon": {
			shell:   "/bin/sh:bash",
			wantErr: true,
		},
		"Error_if_shell_contains_control_characters": {
			shell:   "/bin/sh\x00",
			wantErr: true,
		},
		"Error_if_shell_contains_control_character_tab": {
			shell:   "/bin/sh\t",
			wantErr: true,
		},
		"Error_if_shell_contains_control_character_newline": {
			shell:   "/bin/sh\n",
			wantErr: true,
		},
		"Error_if_shell_contains_control_character_del": {
			shell:   "/bin/sh\x7f",
			wantErr: true,
		},

		// checkValidShellPath error cases
		"Error_if_shell_is_not_absolute_path": {
			shell:   "bin/sh",
			wantErr: true,
		},
		"Error_if_shell_path_is_not_normalized": {
			shell:   "/bin/../bin/sh",
			wantErr: true,
		},
		"Error_if_shell_path_is_not_normalized_with_dot": {
			shell:   "/bin/./sh",
			wantErr: true,
		},
		"Error_if_shell_path_is_too_long": {
			shell:   "/" + strings.Repeat("a", 4096),
			wantErr: true,
		},

		// other error cases
		"Error_if_user_does_not_exist": {
			nonExistentUser: true,
			wantErr:         true,
		},
		"Error_if_username_is_empty": {
			emptyUsername: true,
			wantErr:       true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group.db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			username := "user1@example.com"
			if tc.nonExistentUser {
				username = "nonexistent"
			} else if tc.emptyUsername {
				username = ""
			}

			shell := "/bin/sh"
			if tc.emptyShell {
				shell = ""
			} else if tc.shell != "" {
				shell = tc.shell
			}

			warnings, err := m.SetShell(username, shell)
			requireErrorAssertions(t, err, nil, tc.wantErr)

			require.Len(t, warnings, tc.wantWarnings, "Number of warnings mismatch")

			if tc.wantErr {
				return
			}

			yamlData, err := db.Z_ForTests_DumpNormalizedYAML(m.DB())
			require.NoError(t, err)
			golden.CheckOrUpdate(t, yamlData, golden.WithPath("db"))

			golden.CheckOrUpdateYAML(t, warnings, golden.WithPath("warnings"))
		})
	}
}

func TestSetHomeDir(t *testing.T) {
	// These tests acquire the user-management write lock and move directories on
	// disk, so they must not run in parallel: the test lock override returns an
	// error immediately when the lock is already held.
	tests := map[string]struct {
		emptyUsername     bool
		nonExistentUser   bool
		relativeNewHome   bool
		createOldHome     bool
		precreateNewHome  bool
		sameAsCurrentHome bool
		busyUser          bool
		newHomeNotAccess  bool
		oldHomeNotAccess  bool
		renameFails       bool
		dbReadOnly        bool

		wantErr      bool
		wantChanged  bool
		wantMoved    bool
		wantWarnings int
	}{
		"Successfully_move_existing_home_dir":  {createOldHome: true, wantChanged: true, wantMoved: true},
		"Update_db_only_when_old_home_missing": {wantChanged: true, wantWarnings: 1},
		"No-op_when_user_already_has_home_dir": {sameAsCurrentHome: true, wantWarnings: 1},

		"Error_when_destination_already_exists":   {createOldHome: true, precreateNewHome: true, wantErr: true},
		"Error_when_path_is_not_absolute":         {relativeNewHome: true, wantErr: true},
		"Error_when_username_is_empty":            {emptyUsername: true, wantErr: true},
		"Error_when_user_does_not_exist":          {nonExistentUser: true, wantErr: true},
		"Error_when_user_is_busy":                 {busyUser: true, createOldHome: true, wantErr: true},
		"Error_when_new_home_path_not_accessible": {newHomeNotAccess: true, createOldHome: true, wantErr: true},
		"Error_when_current_home_not_accessible":  {oldHomeNotAccess: true, wantErr: true},
		"Error_when_rename_fails":                 {renameFails: true, createOldHome: true, wantErr: true},
		"Error_and_rollback_when_db_update_fails": {dbReadOnly: true, createOldHome: true, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", "db", "one_user_and_group.db.yaml"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m := newManagerForTests(t, dbDir)

			username := "user1@example.com"
			if tc.nonExistentUser {
				username = "nonexistent@example.com"
			} else if tc.emptyUsername {
				username = ""
			}

			baseDir := t.TempDir()
			oldHome := filepath.Join(baseDir, "old")
			newHome := filepath.Join(baseDir, "new")
			if tc.relativeNewHome {
				newHome = "relative/new"
			}
			if tc.sameAsCurrentHome {
				newHome = oldHome
			}

			// For the "new home not accessible" test, create a parent directory
			// with no permissions so os.Lstat on the new home fails with EACCES.
			if tc.newHomeNotAccess {
				restrictedDir := filepath.Join(baseDir, "restricted")
				require.NoError(t, os.MkdirAll(restrictedDir, 0o000), "Setup: could not create restricted directory")
				newHome = filepath.Join(restrictedDir, "new")
				t.Cleanup(func() { _ = os.Chmod(restrictedDir, 0o700) }) //nolint:gosec // test-only cleanup
			}

			// For the "old home not accessible" test, point the user at a path
			// inside a restricted directory so os.Lstat fails with EACCES.
			if tc.oldHomeNotAccess {
				restrictedDir := filepath.Join(baseDir, "restricted")
				require.NoError(t, os.MkdirAll(restrictedDir, 0o000), "Setup: could not create restricted directory")
				oldHome = filepath.Join(restrictedDir, "old")
				t.Cleanup(func() { _ = os.Chmod(restrictedDir, 0o700) }) //nolint:gosec // test-only cleanup
			}

			// For the "rename fails" test, make the parent of the new home
			// read-only so os.Rename cannot create the new entry.
			if tc.renameFails {
				restrictedDir := filepath.Join(baseDir, "restricted")
				require.NoError(t, os.MkdirAll(restrictedDir, 0o500), "Setup: could not create restricted directory")
				newHome = filepath.Join(restrictedDir, "new")
				t.Cleanup(func() { _ = os.Chmod(restrictedDir, 0o700) }) //nolint:gosec // test-only cleanup
			}

			// Point the user at our controlled old home directory.
			if !tc.emptyUsername && !tc.nonExistentUser {
				err = m.DB().SetHomeDir(username, oldHome)
				require.NoError(t, err, "Setup: could not set initial home directory")
			}

			// For the busy-user test, set the user's UID to the current process
			// UID so proc.CheckUserBusy finds an active process.
			if tc.busyUser {
				err = m.DB().SetUserID(username, uint32(os.Getuid())) //nolint:gosec // G115 - UID is always a valid uint32 in tests
				require.NoError(t, err, "Setup: could not set user UID to current process UID")
			}

			if tc.createOldHome {
				require.NoError(t, os.MkdirAll(oldHome, 0o700), "Setup: could not create old home directory")
				require.NoError(t, os.WriteFile(filepath.Join(oldHome, "marker"), []byte("data"), 0o600), "Setup: could not create marker file")
			}
			if tc.precreateNewHome {
				require.NoError(t, os.MkdirAll(newHome, 0o700), "Setup: could not pre-create new home directory")
			}

			// For the DB-read-only test, make the database directory read-only
			// after setup so that SQLite cannot create the rollback journal
			// file and the UPDATE in db.SetHomeDir fails.  The SELECT
			// (UserByName) still succeeds because it is served from the
			// already-open connection's page cache and needs no journal.
			if tc.dbReadOnly {
				require.NoError(t, os.Chmod(dbDir, 0o500), "Setup: could not make database directory read-only") //nolint:gosec // test-only permission change
				t.Cleanup(func() { _ = os.Chmod(dbDir, 0o700) })                                                 //nolint:gosec // test-only cleanup
			}

			resp, err := m.SetHomeDir(username, newHome)
			if tc.wantErr {
				require.Error(t, err, "SetHomeDir should return an error")
				// On error, the database record must remain unchanged.
				if !tc.emptyUsername && !tc.nonExistentUser && !tc.dbReadOnly {
					u, lookupErr := m.UserByName(username)
					require.NoError(t, lookupErr, "User should still exist")
					require.Equal(t, oldHome, u.Dir, "Home directory in the database should be unchanged on error")
				}
				// For the dbReadOnly case, the directory was moved (rename succeeded)
				// but the DB update failed, so the rollback should have moved it back.
				if tc.dbReadOnly {
					require.DirExists(t, oldHome, "Old home directory should have been rolled back")
				}
				return
			}
			require.NoError(t, err, "SetHomeDir should not return an error")
			require.Equal(t, tc.wantChanged, resp.HomeDirChanged, "Unexpected HomeDirChanged value")
			require.Equal(t, tc.wantMoved, resp.HomeDirMoved, "Unexpected HomeDirMoved value")
			require.Len(t, resp.Warnings, tc.wantWarnings, "Unexpected number of warnings")

			// On success, the database record is always updated to the new path.
			u, err := m.UserByName(username)
			require.NoError(t, err, "User should exist")
			require.Equal(t, newHome, u.Dir, "Home directory in the database should be updated")

			if tc.wantMoved {
				require.NoDirExists(t, oldHome, "Old home directory should have been moved")
				require.FileExists(t, filepath.Join(newHome, "marker"), "Marker file should exist at the new location")
			} else {
				// The old home was missing, so the new directory must not be created.
				require.NoDirExists(t, newHome, "New home directory should not be created when the old one is missing")
			}
		})
	}
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

	return newManagerForTestsWithConfig(t, users.DefaultConfig, dbDir, opts...)
}

func newManagerForTestsWithConfig(t *testing.T, config users.Config, dbDir string, opts ...users.Option) *users.Manager {
	t.Helper()

	m, err := users.NewManager(config, dbDir, opts...)
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
