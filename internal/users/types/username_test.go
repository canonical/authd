package types_test

import (
	"testing"

	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestValidateUserName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name string

		wantErr bool
	}{
		"Plain_name":                    {name: "user1"},
		"Email_address":                 {name: "user1@example.com"},
		"Name_with_dashes":              {name: "user1-renamed@example.com"},
		"Name_with_dots_and_underscore": {name: "first.last_1@example.com"},
		"Name_ending_with_a_dollar":     {name: "machine$"},
		"Name_with_a_trailing_dash":     {name: "user1-"},

		"Error_when_empty": {name: "", wantErr: true},
		// ':' and ',' are the field and member separators of the group file.
		"Error_with_a_colon": {name: "user1:x", wantErr: true},
		"Error_with_a_comma": {name: "user1,user2", wantErr: true},
		// A '/' would escape the home directory path built from the name.
		"Error_with_a_slash": {name: "user1/../root", wantErr: true},
		// "." and ".." carry no '/' but still name directory entries, so a path built from them
		// would resolve to the directory itself or to its parent.
		"Error_when_only_a_dot":      {name: ".", wantErr: true},
		"Error_when_only_two_dots":   {name: "..", wantErr: true},
		"Name_starting_with_a_dot":   {name: ".user1"},
		"Name_of_three_dots_is_fine": {name: "..."},
		// A newline would add a line to the generated group file.
		"Error_with_a_newline":             {name: "user1\nroot:x:0:", wantErr: true},
		"Error_with_a_carriage_return":     {name: "user1\r", wantErr: true},
		"Error_with_a_tab":                 {name: "user1\tuser2", wantErr: true},
		"Error_with_a_null_byte":           {name: "user1\x00", wantErr: true},
		"Error_with_a_space":               {name: "user 1", wantErr: true},
		"Error_with_a_non_breaking_space":  {name: "user\u00a01", wantErr: true},
		"Error_when_starting_with_a_dash":  {name: "-user1", wantErr: true},
		"Error_when_starting_with_a_plus":  {name: "+user1", wantErr: true},
		"Error_when_only_a_dash":           {name: "-", wantErr: true},
		"Error_when_it_is_a_NIS_netgroup":  {name: "+@admins", wantErr: true},
		"Error_with_a_vertical_tab":        {name: "user1\vuser2", wantErr: true},
		"Error_with_a_unicode_line_break":  {name: "user1\u2028", wantErr: true},
		"Error_with_a_colon_at_the_start":  {name: ":user1", wantErr: true},
		"Error_with_a_slash_at_the_start":  {name: "/user1", wantErr: true},
		"Error_with_a_comma_at_the_start":  {name: ",user1", wantErr: true},
		"Error_with_a_delete_control_char": {name: "user1\x7f", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := types.ValidateUserName(tc.name)
			if tc.wantErr {
				require.Error(t, err, "ValidateUserName should reject %q", tc.name)
				return
			}
			require.NoError(t, err, "ValidateUserName should accept %q", tc.name)
		})
	}
}
