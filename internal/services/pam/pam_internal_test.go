package pam

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPermissionDenied(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data string
		want bool
	}{
		"Empty":             {data: "", want: false},
		"Invalid_json":      {data: "invalid", want: false},
		"Missing_code":      {data: `{"message":"denied"}`, want: false},
		"Unknown_code":      {data: `{"message":"denied","code":"other"}`, want: false},
		"Non_string_code":   {data: `{"message":"denied","code":true}`, want: false},
		"Non_object":        {data: `[]`, want: false},
		"Code_only":         {data: `{"code":"permission-denied"}`, want: true},
		"Permission_denied": {data: `{"message":"denied","code":"permission-denied"}`, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isPermissionDenied(tc.data))
		})
	}
}
