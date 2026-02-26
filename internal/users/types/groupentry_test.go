package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateGroupEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		group   GroupEntry
		wantErr bool
	}{
		{
			name:  "Valid_group",
			group: GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
		},
		{
			name:  "Valid_root_group",
			group: GroupEntry{Name: "root", Passwd: "x"},
		},
		{
			name:  "Valid_group_with_empty_users_list",
			group: GroupEntry{Name: "group1", GID: 1006, Passwd: "x", Users: []string{}},
		},

		// Error cases.
		{
			name:    "Error_on_empty_group",
			wantErr: true,
		},
		{
			name:    "Error_on_non_root_group_with_zero_GID",
			group:   GroupEntry{Name: "i-wish-i-was-root"},
			wantErr: true,
		},
		{
			name:    "Error_on_empty_name",
			group:   GroupEntry{Name: "", GID: 1001, Passwd: "x", Users: []string{"user1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_name_contains_comma",
			group:   GroupEntry{Name: "ad,mins", GID: 1002, Passwd: "x", Users: []string{"user1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_passwd_contains_comma",
			group:   GroupEntry{Name: "group1", GID: 1003, Passwd: "x,", Users: []string{"user1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_user_contains_comma",
			group:   GroupEntry{Name: "group1", GID: 1004, Passwd: "x", Users: []string{"al,ice"}},
			wantErr: true,
		},
		{
			name:    "Error_on_multiple_users_one_with_comma",
			group:   GroupEntry{Name: "group1", GID: 1005, Passwd: "x", Users: []string{"user1", "b,ob"}},
			wantErr: true,
		},
		{
			name:    "Error_on_name_contains_colon",
			group:   GroupEntry{Name: "ad:mins", GID: 1002, Passwd: "x", Users: []string{"user1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_passwd_contains_colon",
			group:   GroupEntry{Name: "group1", GID: 1003, Passwd: "x:", Users: []string{"user1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_user_contains_colon",
			group:   GroupEntry{Name: "group1", GID: 1004, Passwd: "x", Users: []string{"user:1"}},
			wantErr: true,
		},
		{
			name:    "Error_on_multiple_users_one_with_colon",
			group:   GroupEntry{Name: "group1", GID: 1005, Passwd: "x", Users: []string{"user1", "user:2"}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.group.Validate()
			if tc.wantErr {
				require.Error(t, err, "Validate should return error but it did not")
				return
			}

			require.NoError(t, err, "Validate should not return error but it did")
		})
	}
}

func TestGroupEntryEquals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    GroupEntry
		b    GroupEntry
		want bool
	}{
		{
			name: "Equal_groups_all_fields",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
			want: true,
		},
		{
			name: "Different_Users_order",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user2", "user1"}},
			want: true,
		},
		{
			name: "Both_empty_users",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{}},
			want: true,
		},
		{
			name: "Both_empty",
			a:    GroupEntry{},
			b:    GroupEntry{},
			want: true,
		},
		{
			name: "Different_name",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			b:    GroupEntry{Name: "group2", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			want: false,
		},
		{
			name: "Different_GID",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			b:    GroupEntry{Name: "group1", GID: 1001, Passwd: "x", Users: []string{"user1"}},
			want: false,
		},
		{
			name: "Different_passwd",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "y", Users: []string{"user1"}},
			want: false,
		},
		{
			name: "Different_users",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user2"}},
			want: false,
		},
		{
			name: "Different_multiple_users",
			a:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user2", "user3"}},
			want: false,
		},
		{
			name: "One_empty_one_filled",
			a:    GroupEntry{},
			b:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.a.Equals(tc.b)
			require.Equal(t, tc.want, got, "Equals not matching expected; want %v", tc.want)
		})
	}
}

func TestGroupEntryString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		group    GroupEntry
		expected string
	}{
		{
			name:     "All_fields_set_with_users",
			group:    GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
			expected: "group1:x:1000:user1,user2",
		},
		{
			name:     "No_users",
			group:    GroupEntry{Name: "group2", GID: 1001, Passwd: "y", Users: nil},
			expected: "group2:y:1001:",
		},
		{
			name:     "Empty_users_slice",
			group:    GroupEntry{Name: "group3", GID: 1002, Passwd: "z", Users: []string{}},
			expected: "group3:z:1002:",
		},
		{
			name:     "Empty_passwd",
			group:    GroupEntry{Name: "group4", GID: 1003, Passwd: "", Users: []string{"user3"}},
			expected: "group4::1003:user3",
		},
		{
			name:     "Empty_group",
			group:    GroupEntry{},
			expected: "::0:",
		},
		{
			name:     "Multiple_users",
			group:    GroupEntry{Name: "admins", GID: 42, Passwd: "pw", Users: []string{"alice", "bob", "carol"}},
			expected: "admins:pw:42:alice,bob,carol",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.group.String()
			require.Equal(t, tc.expected, got, "String output mismatch")
		})
	}
}

func TestValidateGroupEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		groups  []GroupEntry
		wantErr bool
	}{
		{
			name: "Valid_multiple_groups",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
				{Name: "group2", GID: 1001, Passwd: "x", Users: []string{"user3"}},
			},
		},
		{
			name: "Valid_duplicate_group_structs_with_default_passwd",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			},
		},
		{
			name: "Valid_duplicate_group_structs_with_unset_passwd",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Users: []string{"user1"}},
				{Name: "group1", GID: 1000, Users: []string{"user1"}},
			},
		},
		{
			name:   "Valid_empty_list",
			groups: []GroupEntry{},
		},
		{
			name: "Valid_root_group_GID_0",
			groups: []GroupEntry{
				{Name: "root", GID: 0, Passwd: "x"},
			},
		},
		{
			name: "Valid_group_with_empty_passwd",
			groups: []GroupEntry{
				{Name: "group1", GID: 1002, Passwd: "", Users: []string{"user1"}},
			},
		},

		// Error cases
		{
			name: "Error_duplicate_group_name_different_content",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1001, Passwd: "x", Users: []string{"user2"}},
			},
			wantErr: true,
		},
		{
			name: "Error_duplicate_GID",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1000, Passwd: "x", Users: []string{"user2"}},
			},
			wantErr: true,
		},
		{
			name: "Error_duplicate_group_structs_with_partial_default_passwd",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1000, Users: []string{"user1"}},
			},
			wantErr: true,
		},
		{
			name: "Error_invalid_group_entry",
			groups: []GroupEntry{
				{Name: "", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			},
			wantErr: true,
		},
		{
			name: "Error_non_root_group_GID_0",
			groups: []GroupEntry{
				{Name: "group1", GID: 0, Passwd: "x"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateGroupEntries(tc.groups)
			if tc.wantErr {
				require.Error(t, err, "ValidateGroupEntries should return error but did not")
				return
			}

			require.NoError(t, err, "ValidateGroupEntries should not return error but did")
		})
	}
}

func TestGroupEntryDeepCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original GroupEntry
	}{
		{
			name:     "Empty_group",
			original: GroupEntry{},
		},
		{
			name:     "Group_with_users",
			original: GroupEntry{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1", "user2"}},
		},
		{
			name:     "Group_with_empty_users",
			original: GroupEntry{Name: "group2", GID: 1001, Passwd: "x", Users: []string{}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			copied := tc.original.DeepCopy()
			require.Equal(t, tc.original, copied, "DeepCopy should produce an equal struct")

			// Mutate the copy's Users slice and ensure original is not affected
			if copied.Users != nil {
				copied.Users = append(copied.Users, "newuser")
				require.NotEqual(t, tc.original.Users, copied.Users, "Users slice should be independent after DeepCopy")
			}
		})
	}
}

func TestDeepCopyGroupEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original []GroupEntry
	}{
		{
			name:     "Empty_slice",
			original: []GroupEntry{},
		},
		{
			name: "Multiple_groups",
			original: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1001, Passwd: "y", Users: []string{"user2", "user3"}},
			},
		},
		{
			name: "Groups_with_empty_users",
			original: []GroupEntry{
				{Name: "group3", GID: 1002, Passwd: "z", Users: []string{}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			copied := DeepCopyGroupEntries(tc.original)
			require.Equal(t, tc.original, copied, "DeepCopyGroupEntries should produce an equal slice")

			// Mutate the copy and ensure original is not affected
			if len(copied) > 0 && len(copied[0].Users) > 0 {
				copied[0].Users[0] = "mutated"
				require.NotEqual(t, tc.original[0].Users[0], copied[0].Users[0], "Users slice should be independent after DeepCopyGroupEntries")
			}
			if len(copied) > 0 {
				copied[0].Name = "mutated"
				require.NotEqual(t, tc.original[0].Name, copied[0].Name, "Struct fields should be independent after DeepCopyGroupEntries")
			}
		})
	}
}

func TestGetValidGroupEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		groups []GroupEntry
		want   []GroupEntry
	}{
		{
			name: "Nil_input",
		},
		{
			name:   "Empty_input",
			groups: []GroupEntry{},
			want:   []GroupEntry{},
		},
		{
			name: "All_valid_groups",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1001, Passwd: "y", Users: []string{"user2"}},
			},
			want: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1001, Passwd: "y", Users: []string{"user2"}},
			},
		},
		{
			name: "Invalid_group_skipped",
			groups: []GroupEntry{
				{Name: "invalid,group", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1001, Passwd: "y", Users: []string{"user2"}},
			},
			want: []GroupEntry{
				{Name: "group2", GID: 1001, Passwd: "y", Users: []string{"user2"}},
			},
		},
		{
			name: "Duplicate_name_with_equal_content",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			},
			want: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
			},
		},
		{
			name: "Duplicate_name_with_different_content",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1001, Passwd: "y", Users: []string{"user2"}},
				{Name: "group2", GID: 1002, Passwd: "z", Users: []string{"user3"}},
			},
			want: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1002, Passwd: "z", Users: []string{"user3"}},
			},
		},
		{
			name: "Duplicate_GID",
			groups: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group2", GID: 1000, Passwd: "y", Users: []string{"user2"}},
				{Name: "group3", GID: 1001, Passwd: "z", Users: []string{"user3"}},
			},
			want: []GroupEntry{
				{Name: "group1", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group3", GID: 1001, Passwd: "z", Users: []string{"user3"}},
			},
		},
		{
			name: "Multiple_invalid_and_duplicates",
			groups: []GroupEntry{
				{Name: "invalid,group", GID: 1000, Passwd: "x", Users: []string{"user1"}},
				{Name: "group1", GID: 1001, Passwd: "y", Users: []string{"user2"}},
				{Name: "group1", GID: 1001, Passwd: "y", Users: []string{"user2"}},
				{Name: "group2", GID: 1001, Passwd: "z", Users: []string{"user3"}},
				{Name: "group3", GID: 1002, Passwd: "w", Users: []string{"user4"}},
			},
			want: []GroupEntry{
				{Name: "group1", GID: 1001, Passwd: "y", Users: []string{"user2"}},
				{Name: "group1", GID: 1001, Passwd: "y", Users: []string{"user2"}},
				{Name: "group3", GID: 1002, Passwd: "w", Users: []string{"user4"}},
			},
		},
		{
			name: "Root_group_GID_0_valid",
			groups: []GroupEntry{
				{Name: "root", GID: 0, Passwd: "x"},
				{Name: "group1", GID: 1000, Passwd: "y"},
			},
			want: []GroupEntry{
				{Name: "root", GID: 0, Passwd: "x"},
				{Name: "group1", GID: 1000, Passwd: "y"},
			},
		},
		{
			name: "Non_root_group_GID_0_invalid",
			groups: []GroupEntry{
				{Name: "group1", GID: 0, Passwd: "x"},
				{Name: "group2", GID: 1001, Passwd: "y"},
			},
			want: []GroupEntry{
				{Name: "group2", GID: 1001, Passwd: "y"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := GetValidGroupEntries(tc.groups)
			require.Equal(t, tc.want, got, "GetValidGroupEntries output mismatch")
		})
	}
}
