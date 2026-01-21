package types_test

import (
	"testing"

	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func ptrValue[T any](value T) *T {
	return &value
}

func TestUserInfoEquals(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		u1, u2 types.UserInfo
		want   bool
	}{
		"Equal_when_all_fields_and_groups_are_equal": {
			u1: types.UserInfo{
				Name:  "user1",
				UID:   1000,
				Gecos: "User1",
				Dir:   "/home/user1",
				Shell: "/bin/bash",
				Groups: []types.GroupInfo{
					{Name: "sudo", GID: ptrValue[uint32](27)},
					{Name: "users", GID: ptrValue[uint32](100)},
				},
			},
			u2: types.UserInfo{
				Name:  "user1",
				UID:   1000,
				Gecos: "User1",
				Dir:   "/home/user1",
				Shell: "/bin/bash",
				Groups: []types.GroupInfo{
					{Name: "sudo", GID: ptrValue[uint32](27)},
					{Name: "users", GID: ptrValue[uint32](100)},
				},
			},
			want: true,
		},
		"Equal_when_both_have_no_groups": {
			u1:   types.UserInfo{Name: "user1"},
			u2:   types.UserInfo{Name: "user1"},
			want: true,
		},
		"Equal_when_groups_are_equal_but_in_different_pointer_instances": {
			u1: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{{Name: "sudo", GID: ptrValue[uint32](27)}},
			},
			u2: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{{Name: "sudo", GID: ptrValue[uint32](27)}},
			},
			want: true,
		},
		"Equal_when_groups_are_equal_but_in_different_order": {
			u1: types.UserInfo{
				Name: "user1",
				Groups: []types.GroupInfo{
					{Name: "group1", GID: ptrValue[uint32](1)},
					{Name: "group2", GID: ptrValue[uint32](2)},
				},
			},
			u2: types.UserInfo{
				Name: "user1",
				Groups: []types.GroupInfo{
					{Name: "group2", GID: ptrValue[uint32](2)},
					{Name: "group1", GID: ptrValue[uint32](1)},
				},
			},
			want: true,
		},

		// Failing cases.
		"Fails_if_names_differ": {
			u1:   types.UserInfo{Name: "user1"},
			u2:   types.UserInfo{Name: "user2"},
			want: false,
		},
		"Fails_if_UIDs_differ": {
			u1:   types.UserInfo{Name: "user1", UID: 1000},
			u2:   types.UserInfo{Name: "user1", UID: 1001},
			want: false,
		},
		"Fails_if_Gecos_differ": {
			u1:   types.UserInfo{Name: "user1", Gecos: "User1"},
			u2:   types.UserInfo{Name: "user1", Gecos: "User3"},
			want: false,
		},
		"Fails_if_Dir_differ": {
			u1:   types.UserInfo{Name: "user1", Dir: "/home/user1"},
			u2:   types.UserInfo{Name: "user1", Dir: "/home/user2"},
			want: false,
		},
		"Fails_if_Shell_differ": {
			u1:   types.UserInfo{Name: "user1", Shell: "/bin/bash"},
			u2:   types.UserInfo{Name: "user1", Shell: "/bin/zsh"},
			want: false,
		},
		"Fails_if_Groups_differ_in_length": {
			u1: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{{Name: "sudo", GID: ptrValue[uint32](27)}},
			},
			u2: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{},
			},
			want: false,
		},
		"Fails_if_Groups_differ_in_content": {
			u1: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{{Name: "sudo", GID: ptrValue[uint32](27)}},
			},
			u2: types.UserInfo{
				Name:   "user1",
				Groups: []types.GroupInfo{{Name: "users", GID: ptrValue[uint32](100)}},
			},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.u1.Equals(tc.u2)
			require.Equal(t, tc.want, got, "Equals() returned unexpected result")
		})
	}
}
