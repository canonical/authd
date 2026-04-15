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

func TestValidateUsername(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string
		wantErr  bool
	}{
		// Valid usernames
		"Valid_lowercase_name":                         {username: "user"},
		"Valid_name_starting_with_underscore":          {username: "_user"},
		"Valid_name_with_hyphen":                       {username: "user-name"},
		"Valid_name_with_underscore":                   {username: "user_name"},
		"Valid_name_with_digits":                       {username: "user1"},
		"Valid_name_with_trailing_dollar_sign":         {username: "user$"},
		"Valid_single_character_name":                  {username: "a"},
		"Valid_name_with_mixed_allowed_chars":          {username: "a-b_c0"},
		"Valid_email_style_name":                       {username: "user@example.com"},
		"Valid_email_style_name_with_subdomain":        {username: "user@sub.example.com"},
		"Valid_name_with_dot":                          {username: "first.last"},

		// Invalid usernames
		"Error_on_empty_username":                      {username: "", wantErr: true},
		"Error_on_uppercase_character":                 {username: "User", wantErr: true},
		"Error_on_uppercase_email":                     {username: "User@example.com", wantErr: true},
		"Error_on_name_starting_with_digit":            {username: "1user", wantErr: true},
		"Error_on_name_starting_with_hyphen":           {username: "-user", wantErr: true},
		"Error_on_name_with_dollar_not_at_end":         {username: "user$name", wantErr: true},
		"Error_on_name_with_space":                     {username: "user name", wantErr: true},

		// Injection / path traversal characters must be rejected
		"Error_on_name_with_slash":                     {username: "user/name", wantErr: true},
		"Error_on_name_with_backslash":                 {username: `user\name`, wantErr: true},
		"Error_on_name_with_single_quote":              {username: "user'name", wantErr: true},
		"Error_on_name_with_double_quote":              {username: `user"name`, wantErr: true},
		"Error_on_name_with_backtick":                  {username: "user`name", wantErr: true},
		"Error_on_name_with_semicolon":                 {username: "user;name", wantErr: true},
		"Error_on_name_with_ampersand":                 {username: "user&name", wantErr: true},
		"Error_on_name_with_pipe":                      {username: "user|name", wantErr: true},
		"Error_on_name_with_null_byte":                 {username: "user\x00name", wantErr: true},
		"Error_on_name_with_newline":                   {username: "user\nname", wantErr: true},
		"Error_on_name_with_tab":                       {username: "user\tname", wantErr: true},
		"Error_on_name_with_colon":                     {username: "user:name", wantErr: true},
		"Error_on_name_with_exclamation":               {username: "user!name", wantErr: true},
		"Error_on_name_with_open_paren":                {username: "user(name", wantErr: true},
		"Error_on_name_with_close_paren":               {username: "user)name", wantErr: true},
		"Error_on_name_with_less_than":                 {username: "user<name", wantErr: true},
		"Error_on_name_with_greater_than":              {username: "user>name", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := types.ValidateUsername(tc.username)
			if tc.wantErr {
				require.Error(t, err, "ValidateUsername should return an error for %q, but did not", tc.username)
				return
			}
			require.NoError(t, err, "ValidateUsername should not return an error for %q, but did", tc.username)
		})
	}
}
