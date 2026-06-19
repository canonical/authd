package localentries_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/localentries"
	localentriestestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

func TestUpdatelocalentries(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string

		newGroups     []string
		oldGroups     []string
		groupFilePath string

		wantErr bool
	}{
		// First insertion cases
		"Insert_new_user_in_existing_files_with_no_users_in_our_group":             {groupFilePath: "no_users_in_our_groups.group"},
		"Insert_new_user_when_no_users_in_any_group":                               {groupFilePath: "no_users.group"},
		"Insert_new_user_in_existing_files_with_other_users_in_our_group":          {groupFilePath: "users_in_our_groups.group"},
		"Insert_new_user_in_existing_files_with_multiple_other_users_in_our_group": {groupFilePath: "multiple_users_in_our_groups.group"},

		// User already in groups
		"No-Op_for_user_is_already_present_in_both_local_groups":                  {groupFilePath: "user_in_both_groups.group"},
		"Insert_user_in_the_only_local_group_when_not_present":                    {groupFilePath: "user_in_one_group.group"},
		"Insert_user_in_the_only_local_group_when_not_present_even_with_multiple": {groupFilePath: "user_and_others_in_one_groups.group"},
		"Remove_user_from_an_additional_group_without_other_users":                {groupFilePath: "user_in_second_local_group.group"},
		"Remove_user_from_an_additional_group_with_other_users":                   {groupFilePath: "user_in_second_local_group_with_others.group"},
		"Add_and_remove_user_from_multiple_groups_with_one_remaining":             {groupFilePath: "user_in_many_groups.group"},

		// Flexible accepted cases
		"Missing_group_is_ignored":              {groupFilePath: "missing_group.group"},
		"Group_file_with_empty_line_is_ignored": {groupFilePath: "empty_line.group"},

		// No new groups
		"No-Op_for_user_with_no_groups_and_was_in_none": {newGroups: []string{}, groupFilePath: "no_users_in_our_groups.group"},
		"Remove_user_with_no_groups_from_existing_ones": {newGroups: []string{}, groupFilePath: "user_in_both_groups.group"},

		// User removed from groups
		"User_is_added_to_group_they_were_added_to_before":          {newGroups: []string{"localgroup1"}, oldGroups: []string{"localgroup1"}, groupFilePath: "no_users.group"},
		"User_is_removed_from_old_groups_but_not_from_other_groups": {newGroups: []string{}, oldGroups: []string{"localgroup3"}, groupFilePath: "user_in_both_groups.group"},
		"User_is_not_removed_from_groups_they_are_not_part_of":      {newGroups: []string{}, oldGroups: []string{"localgroup2"}, groupFilePath: "user_in_one_group.group"},

		"Warn_when_groups_file_has_no_group_name": {
			groupFilePath: "malformed_file_no_group_name.group",
			newGroups:     []string{"localgroup6"},
		},
		"Warn_when_groups_file_has_missing_fields": {
			groupFilePath: "malformed_file_missing_field.group",
			newGroups:     []string{"localgroup6"},
		},
		"Warn_when_groups_file_has_invalid_gid": {
			groupFilePath: "malformed_file_invalid_gid.group",
		},

		// Error cases
		"Error_on_missing_groups_file": {
			groupFilePath: "does_not_exists.group",
			wantErr:       true,
		},
		"Error_on_invalid_user_name": {
			groupFilePath: "no_users_in_our_groups.group",
			username:      "no,commas,please",
			wantErr:       true,
		},
		"Error_when_groups_file_has_a_duplicated_group": {
			groupFilePath: "malformed_file_duplicated.group",
			wantErr:       true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.newGroups == nil {
				tc.newGroups = []string{"localgroup1", "localgroup3"}
			}

			switch tc.username {
			case "":
				tc.username = "myuser"
			case "-":
				tc.username = ""
			}

			inputGroupFilePath := filepath.Join("testdata", tc.groupFilePath)
			outputGroupFilePath := filepath.Join(t.TempDir(), "group")

			if exists, _ := fileutils.FileExists(inputGroupFilePath); exists {
				tempGroupFile := filepath.Join(t.TempDir(), "group")
				err := fileutils.CopyFile(inputGroupFilePath, tempGroupFile)
				require.NoError(t, err, "failed to copy group file for testing")
				inputGroupFilePath = tempGroupFile
			}

			defer localentriestestutils.RequireGroupFile(t, outputGroupFilePath, golden.Path(t))

			entries, entriesUnlock, err := localentries.WithUserDBLock(
				localentries.WithGroupInputPath(inputGroupFilePath),
				localentries.WithGroupOutputPath(outputGroupFilePath),
				localentries.WithMockUserDBLocking(),
			)
			require.NoError(t, err, "Failed to lock the local entries")
			t.Cleanup(func() {
				err := entriesUnlock()
				require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
			})

			err = localentries.UpdateGroups(entries, tc.username, tc.newGroups, tc.oldGroups)
			if tc.wantErr {
				require.Error(t, err, "Updatelocalentries should have failed")
			} else {
				require.NoError(t, err, "Updatelocalentries should not have failed")
			}
		})
	}
}

func TestGetAndSaveLocalGroups(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		groupFilePath string
		addGroups     []types.GroupEntry
		removeGroups  []string
		addUsers      map[string][]string

		wantGetErr bool
		wantSetErr bool
		wantNoOp   bool
	}{
		"Empty_group_is_kept_empty_on_no_op": {
			groupFilePath: "empty.group",
		},
		"Empty_group_adding_one_group": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1},
			},
		},
		"Empty_group_adding_one_group_with_one_user": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1},
			},
			addUsers: map[string][]string{"group1": {"user1"}},
		},
		"Empty_group_adding_two_groups_with_two_users": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1},
				{Name: "group2", Passwd: "x", GID: 2},
			},
			addUsers: map[string][]string{
				"group1": {"user1.1", "user1.2"},
				"group2": {"user2.2", "user2.2"},
			},
		},
		"Empty_group_adding_one_group_and_removing_it_afterwards": {
			groupFilePath: "empty.group",
			addGroups:     []types.GroupEntry{{Name: "group1", GID: 1}},
			removeGroups:  []string{"group1"},
			wantNoOp:      true,
		},
		"Insert_new_user_in_existing_files_with_no_users_in_our_group": {
			groupFilePath: "no_users_in_our_groups.group",
			addUsers: map[string][]string{
				"localgroup1": {"user1.1", "user1.2"},
				"localgroup2": {"user2.2", "user2.2"},
			},
		},
		"Insert_new_user_when_no_users_in_any_group": {
			groupFilePath: "no_users.group",
			addUsers: map[string][]string{
				"localgroup1": {"user1.1", "user1.2"},
				"localgroup2": {"user2.2", "user2.2"},
			},
		},
		"Insert_new_user_in_existing_files_with_other_users_in_our_group": {
			groupFilePath: "users_in_our_groups.group",
			addUsers: map[string][]string{
				"localgroup1": {"user1.1", "user1.2"},
				"localgroup2": {"user2.2", "user2.2"},
			},
		},
		"Insert_new_user_in_existing_files_with_multiple_other_users_in_our_group": {
			groupFilePath: "multiple_users_in_our_groups.group",
			addUsers: map[string][]string{
				"localgroup1": {"user1.1", "user1.2"},
				"localgroup2": {"user2.2", "user2.2"},
			},
		},
		"Ignores_adding_duplicated_equal_groups": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 12345, Users: []string{"user1", "user2"}},
				{Name: "group1", Passwd: "x", GID: 12345, Users: []string{"user1", "user2"}},
			},
		},
		"Ignores_adding_duplicated_equal_group_to_existing_file": {
			groupFilePath: "no_users_in_our_groups.group",
			addGroups: []types.GroupEntry{
				{Name: "localgroup3", Passwd: "x", GID: 43},
			},
		},
		"Removes_group_correctly": {
			groupFilePath: "multiple_users_in_our_groups.group",
			removeGroups:  []string{"localgroup1", "localgroup4"},
		},
		"Warn_when_groups_file_has_a_duplicated_group": {
			groupFilePath: "malformed_file_duplicated.group",
			addGroups: []types.GroupEntry{
				{Name: "localgroup5", Passwd: "x", GID: 45},
			},
		},
		"Warn_when_groups_file_has_an_extra_field": {
			groupFilePath: "malformed_file_extra_field.group",
			addGroups: []types.GroupEntry{
				{Name: "localgroup5", Passwd: "x", GID: 45},
			},
		},
		"Warn_when_groups_file_has_entries_with_missing_fields": {
			groupFilePath: "malformed_file_invalid_entries_length.group",
			addGroups: []types.GroupEntry{
				{Name: "localgroup9", Passwd: "x", GID: 49},
			},
		},
		"Warn_when_groups_file_has_no_group_name": {
			groupFilePath: "malformed_file_no_group_name.group",
		},
		"Warn_when_groups_file_has_missing_fields": {
			groupFilePath: "malformed_file_missing_field.group",
		},
		"Warn_when_groups_file_has_invalid_gid": {
			groupFilePath: "malformed_file_invalid_gid.group",
		},

		// Error cases.
		"Error_when_missing_groups_file": {
			groupFilePath: "does_not_exists.group",
			wantGetErr:    true,
		},
		"Error_adding_duplicated_groups": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", GID: 12345},
				{Name: "group1", GID: 12345, Users: []string{"user1"}},
			},
			wantSetErr: true,
		},
		"Error_adding_duplicated_group_to_existing_file": {
			groupFilePath: "no_users_in_our_groups.group",
			addGroups:     []types.GroupEntry{{Name: "localgroup3", GID: 12345}},
			wantSetErr:    true,
		},
		"Error_adding_duplicated_groups_GIDs": {
			groupFilePath: "empty.group",
			addGroups: []types.GroupEntry{
				{Name: "group1", GID: 43},
				{Name: "group2", GID: 43, Users: []string{"user1"}},
			},
			wantSetErr: true,
		},
		"Error_adding_duplicated_group_GID_to_existing_file": {
			groupFilePath: "no_users_in_our_groups.group",
			addGroups:     []types.GroupEntry{{Name: "test-group3", GID: 43}},
			wantSetErr:    true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inputGroupFilePath := filepath.Join(testutils.CurrentDir(),
				"testdata", tc.groupFilePath)
			outputGroupFilePath := filepath.Join(t.TempDir(), "group")

			if !tc.wantNoOp && tc.addGroups == nil && tc.removeGroups == nil && tc.addUsers == nil {
				tc.wantNoOp = true
			}

			if exists, _ := fileutils.FileExists(inputGroupFilePath); exists {
				tempGroupFile := filepath.Join(t.TempDir(), "group")
				err := os.Symlink(inputGroupFilePath, tempGroupFile)
				require.NoError(t, err, "failed to symlink group file for testing")
				inputGroupFilePath = tempGroupFile
			}

			defer localentriestestutils.RequireGroupFile(t, outputGroupFilePath, golden.Path(t))

			entries, entriesUnlock, err := localentries.WithUserDBLock(
				localentries.WithGroupInputPath(inputGroupFilePath),
				localentries.WithGroupOutputPath(outputGroupFilePath),
				localentries.WithMockUserDBLocking(),
			)
			require.NoError(t, err, "Failed to lock the local entries")
			defer func() {
				err := entriesUnlock()
				require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
			}()

			groups, err := localentries.GetGroupEntries(entries)
			if tc.wantGetErr {
				require.Error(t, err, "GetEntries should return an error, but did not")
				return
			}
			require.NoError(t, err, "GetEntries should not return an error, but did")
			initialGroups := slices.Clone(groups)

			groups = append(groups, tc.addGroups...)
			groups = slices.DeleteFunc(groups, func(g types.GroupEntry) bool {
				return slices.Contains(tc.removeGroups, g.Name)
			})

			for groupName, userNames := range tc.addUsers {
				idx := slices.IndexFunc(groups, func(g types.GroupEntry) bool { return g.Name == groupName })
				require.GreaterOrEqual(t, idx, 0, "Setup: %q is not in groups %v", groupName, groups)
				groups[idx].Users = append(groups[idx].Users, userNames...)
			}

			err = localentries.SaveGroupEntries(entries, groups)
			if tc.wantSetErr {
				require.Error(t, err, "SaveEntries should have failed")
				updatedGroups, err := localentries.GetGroupEntries(entries)
				require.NoError(t, err, "GetEntries should not return an error, but did")
				require.Equal(t, initialGroups, updatedGroups, "Cached groups have been changed")
				return
			}

			if len(groups) == 0 {
				groups = nil
			}

			require.NoError(t, err, "SaveEntries should not have failed")
			// Ensure we also saved the cached version of the groups...
			updatedGroups, err := localentries.GetGroupEntries(entries)
			require.NoError(t, err, "GetEntries should not return an error, but did")
			require.Equal(t, groups, updatedGroups, "Cached groups are not saved")

			if tc.wantNoOp {
				// The output group file should not have been created
				require.NoFileExists(t, outputGroupFilePath, "Output group file should not have been created")
				return
			}

			require.FileExists(t, outputGroupFilePath, "Output file should have been created")
		})
	}
}

//nolint:tparallel // This can't be parallel, but subtests can.
func TestRacingGroupsLockingActions(t *testing.T) {
	const nIterations = 50

	testFilePath := filepath.Join("testdata", "no_users_in_our_groups.group")
	testUserDBLocked := &localentries.UserDBLocked{}

	wg := sync.WaitGroup{}
	wg.Add(nIterations)

	// Lock and get the values in parallel.
	for idx := range nIterations {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			t.Cleanup(wg.Done)

			var opts []localentries.Option
			wantGroup := types.GroupEntry{Name: "root", GID: 0, Passwd: "x"}
			useTestGroupFile := idx%3 == 0

			if useTestGroupFile {
				// Mix the requests with test-only code paths...
				opts = append(opts,
					localentries.WithGroupPath(testFilePath),
					localentries.WithUserDBLockedInstance(testUserDBLocked),
					localentries.WithMockUserDBLocking(),
				)
				wantGroup = types.GroupEntry{Name: "localgroup1", GID: 41, Passwd: "x"}
			}

			entries, entriesUnlock, err := localentries.WithUserDBLock(opts...)
			require.NoError(t, err, "Failed to lock the local entries")
			t.Cleanup(func() {
				err := entriesUnlock()
				require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
			})

			groups, err := localentries.GetGroupEntries(entries)
			require.NoError(t, err, "GetEntries should not return an error, but did")
			require.NotEmpty(t, groups, "Got empty groups (test groups: %v)", useTestGroupFile)
			require.Contains(t, groups, wantGroup, "Expected group was not found  (test groups: %v)", useTestGroupFile)
		})
	}

	t.Run("final_check", func(t *testing.T) {
		t.Parallel()
		wg.Wait()

		// Get a last unlock function, to see if we're all good...
		entries, entriesUnlock, err := localentries.WithUserDBLock()
		require.NoError(t, err, "Failed to lock the local entries")

		require.NoError(t, err, "Unlock should not fail to lock the users group")

		err = entriesUnlock()
		require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")

		// Ensure that we had cleaned up all the locks correctly!
		require.Panics(t, func() { _, _ = localentries.GetGroupEntries(entries) })
	})
}

func TestLockedInvalidActions(t *testing.T) {
	// This cannot be parallel

	require.Panics(t, func() { _ = localentries.UpdateGroups(nil, "", nil, nil) },
		"Update should panic but did not")
	require.Panics(t, func() { _, _ = localentries.GetGroupEntries(nil) },
		"GetEntries should panic but did not")
	require.Panics(t, func() { _ = localentries.SaveGroupEntries(nil, nil) },
		"SaveEntries should panic but did not")

	entries, entriesUnlock, err := localentries.WithUserDBLock()
	require.NoError(t, err, "Failed to lock the local entries")

	err = entriesUnlock()
	require.NoError(t, err, "Unlock should not fail to lock the users group")

	err = entriesUnlock()
	require.Error(t, err, "Unlocking twice should fail")

	require.Panics(t, func() { _ = localentries.UpdateGroups(entries, "", nil, nil) },
		"Update should panic but did not")
	require.Panics(t, func() { _, _ = localentries.GetGroupEntries(entries) },
		"GetEntries should panic but did not")
	require.Panics(t, func() { _ = localentries.SaveGroupEntries(entries, nil) },
		"SaveEntries should panic but did not")

	// This is to ensure that we're in a good state, despite the actions above
	for range 10 {
		_, entriesUnlock, err := localentries.WithUserDBLock()
		require.NoError(t, err, "Failed to lock the local entries")
		defer func() {
			err := entriesUnlock()
			require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
		}()
	}
}

func TestValidateChangedGroups(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		currentGroups []types.GroupEntry
		changedGroups []types.GroupEntry
		newGroups     []types.GroupEntry
		wantErr       bool
	}{
		"Empty_groups_no_error": {},
		"Add_valid_group": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
		},
		"No_changes_in_old_groups": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
		},
		"Replace_old_groups_with_same_name_valid": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user3"}},
			},
		},
		"Add_multiple_valid_groups": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
				{Name: "group3", Passwd: "x", GID: 3, Users: []string{"user3"}},
			},
		},
		"Removed_group": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				// group2 removed
			},
		},
		"Removed_multiple_groups": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
				{Name: "group3", Passwd: "x", GID: 3, Users: []string{"user3"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group3", Passwd: "x", GID: 3, Users: []string{"user3"}},
				// group1 and group2 removed
			},
		},
		"All_groups_removed": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				// All groups removed
			},
		},
		"Add_valid_group_to_current_groups_with_invalid": {
			currentGroups: []types.GroupEntry{
				{Name: "invalid1", Passwd: "x", GID: 1, Users: []string{"user,1"}},
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "invalid1", Passwd: "x", GID: 1, Users: []string{"user,1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
		},
		"Add_user_to_group": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1", "user2"}},
			},
		},

		// Error cases.
		"Error_changed_groups_invalid": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "invalid,group", Passwd: "x", GID: 2, Users: []string{"user2"}},
			},
			wantErr: true,
		},
		"Error_combined_groups_invalid": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 1, Users: []string{"user2"}},
			},
			wantErr: true,
		},
		"Error_adding_multiple_groups_one_invalid": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
				{Name: "invalid,group", Passwd: "x", GID: 4, Users: []string{"user4"}},
			},
			wantErr: true,
		},
		"Error_adding_multiple_groups_with_duplicate_GID": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
				{Name: "group3", Passwd: "x", GID: 2, Users: []string{"user3"}},
			},
			wantErr: true,
		},
		"Error_adding_multiple_groups_with_duplicate_name": {
			currentGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
			},
			newGroups: []types.GroupEntry{
				{Name: "group1", Passwd: "x", GID: 1, Users: []string{"user1"}},
				{Name: "group2", Passwd: "x", GID: 2, Users: []string{"user2"}},
				{Name: "group2", Passwd: "x", GID: 22, Users: []string{"user2.2"}},
			},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := localentries.ValidateChangedGroups(tc.currentGroups, tc.newGroups)
			if tc.wantErr {
				require.Error(t, err, "expected error but got nil")
				t.Logf("Validation failed with error: %v", err)
				return
			}
			require.NoError(t, err, "expected no error but got: %v", err)
		})
	}
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	userslocking.Z_ForTests_OverrideLocking()
	defer userslocking.Z_ForTests_RestoreLocking()

	m.Run()
}
