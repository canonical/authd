package envutils_test

import (
	"testing"

	"github.com/canonical/authd/internal/envutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetenv(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  []string
		key  string
		want string
	}{
		"Get_existing_environment_variable": {
			env:  []string{"FOO=bar", "BAZ=qux"},
			key:  "FOO",
			want: "bar",
		},
		"Get_environment_variable_with_empty_value": {
			env:  []string{"FOO=bar", "EMPTY=", "BAZ=qux"},
			key:  "EMPTY",
			want: "",
		},
		"Get_environment_variable_with_special_characters": {
			env:  []string{"PATH=/usr/bin:/usr/local/bin"},
			key:  "PATH",
			want: "/usr/bin:/usr/local/bin",
		},
		"Get_environment_variable_with_spaces": {
			env:  []string{"MESSAGE=hello world"},
			key:  "MESSAGE",
			want: "hello world",
		},
		"Get_environment_variable_with_equals_sign_in_value": {
			env:  []string{"EQUATION=x=y+z"},
			key:  "EQUATION",
			want: "x=y+z",
		},
		"Get_first_variable_in_list": {
			env:  []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:  "FIRST",
			want: "1",
		},
		"Get_middle_variable_in_list": {
			env:  []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:  "SECOND",
			want: "2",
		},
		"Get_last_variable_in_list": {
			env:  []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:  "THIRD",
			want: "3",
		},
		"Return_empty_string_when_key_not_found": {
			env:  []string{"FOO=bar", "BAZ=qux"},
			key:  "MISSING",
			want: "",
		},
		"Return_empty_string_when_key_not_found_in_empty_environment": {
			env:  []string{},
			key:  "VAR",
			want: "",
		},
		"Return_empty_string_when_looking_for_partial_key_match": {
			env:  []string{"FOOBAR=baz"},
			key:  "FOO",
			want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := envutils.Getenv(tc.env, tc.key)
			assert.Equal(t, tc.want, got, "Value should match expected")
		})
	}
}

func TestSetenv(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env         []string
		key         string
		value       string
		want        []string
		wantErr     bool
		errContains string
	}{
		"Set_new_environment_variable": {
			env:   []string{"FOO=bar", "BAZ=qux"},
			key:   "NEW_VAR",
			value: "new_value",
			want:  []string{"FOO=bar", "BAZ=qux", "NEW_VAR=new_value"},
		},
		"Update_existing_environment_variable": {
			env:   []string{"FOO=bar", "BAZ=qux"},
			key:   "FOO",
			value: "updated",
			want:  []string{"FOO=updated", "BAZ=qux"},
		},
		"Set_variable_in_empty_environment": {
			env:   []string{},
			key:   "VAR",
			value: "value",
			want:  []string{"VAR=value"},
		},
		"Set_variable_with_empty_value": {
			env:   []string{"FOO=bar"},
			key:   "EMPTY",
			value: "",
			want:  []string{"FOO=bar", "EMPTY="},
		},
		"Update_variable_to_empty_value": {
			env:   []string{"FOO=bar", "BAZ=qux"},
			key:   "FOO",
			value: "",
			want:  []string{"FOO=", "BAZ=qux"},
		},
		"Set_variable_with_special_characters_in_value": {
			env:   []string{},
			key:   "PATH",
			value: "/usr/bin:/usr/local/bin",
			want:  []string{"PATH=/usr/bin:/usr/local/bin"},
		},
		"Set_variable_with_spaces_in_value": {
			env:   []string{},
			key:   "MESSAGE",
			value: "hello world",
			want:  []string{"MESSAGE=hello world"},
		},
		"Update_first_variable_in_list": {
			env:   []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:   "FIRST",
			value: "updated",
			want:  []string{"FIRST=updated", "SECOND=2", "THIRD=3"},
		},
		"Update_middle_variable_in_list": {
			env:   []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:   "SECOND",
			value: "updated",
			want:  []string{"FIRST=1", "SECOND=updated", "THIRD=3"},
		},
		"Update_last_variable_in_list": {
			env:   []string{"FIRST=1", "SECOND=2", "THIRD=3"},
			key:   "THIRD",
			value: "updated",
			want:  []string{"FIRST=1", "SECOND=2", "THIRD=updated"},
		},

		// Error cases
		"Error_on_empty_key": {
			env:         []string{"FOO=bar"},
			key:         "",
			value:       "value",
			wantErr:     true,
			errContains: "empty key",
		},
		"Error_on_key_with_equals_sign": {
			env:         []string{"FOO=bar"},
			key:         "KEY=VALUE",
			value:       "value",
			wantErr:     true,
			errContains: "invalid key",
		},
		"Error_on_key_with_null_byte": {
			env:         []string{"FOO=bar"},
			key:         "KEY\x00",
			value:       "value",
			wantErr:     true,
			errContains: "invalid key",
		},
		"Error_on_value_with_null_byte": {
			env:         []string{"FOO=bar"},
			key:         "KEY",
			value:       "value\x00",
			wantErr:     true,
			errContains: "invalid value",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := envutils.Setenv(tc.env, tc.key, tc.value)

			if tc.wantErr {
				require.Error(t, err, "Setenv should return an error")
				assert.Contains(t, err.Error(), tc.errContains, "Error message should contain expected text")
				return
			}

			require.NoError(t, err, "Setenv should not return an error")
			assert.Equal(t, tc.want, got, "Environment slice should match expected")
		})
	}
}

func TestSetenvDoesNotModifyOriginal(t *testing.T) {
	t.Parallel()

	original := []string{"FOO=bar", "BAZ=qux"}
	originalCopy := make([]string, len(original))
	copy(originalCopy, original)

	result, err := envutils.Setenv(original, "NEW", "value")
	require.NoError(t, err)

	// Verify original slice content is unchanged (but may have increased capacity)
	assert.Equal(t, originalCopy, original[:len(originalCopy)], "Original slice content should not be modified")
	// Verify result contains the new variable
	assert.Contains(t, result, "NEW=value", "Result should contain new variable")
}

func TestSetenvPreservesOrder(t *testing.T) {
	t.Parallel()

	// Update a middle variable
	env1 := []string{"A=1", "B=2", "C=3", "D=4", "E=5"}
	result, err := envutils.Setenv(env1, "C", "updated")
	require.NoError(t, err)

	expected := []string{"A=1", "B=2", "C=updated", "D=4", "E=5"}
	assert.Equal(t, expected, result, "Order should be preserved when updating")

	// Add a new variable
	env2 := []string{"A=1", "B=2", "C=3", "D=4", "E=5"}
	result2, err := envutils.Setenv(env2, "F", "6")
	require.NoError(t, err)

	expected2 := []string{"A=1", "B=2", "C=3", "D=4", "E=5", "F=6"}
	assert.Equal(t, expected2, result2, "New variable should be appended at the end")
}
