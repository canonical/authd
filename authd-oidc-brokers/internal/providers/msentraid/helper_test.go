//go:build withmsentraid

package msentraid_test

import (
	"encoding/json"
)

var (
	validIDToken = &testIDToken{
		claims: `{"preferred_username": "valid-user",
		"sub": "valid-sub",
		"home": "/home/valid-user",
		"shell": "/bin/bash",
		"name": "Valid User"}`,
	}

	invalidIDToken = &testIDToken{
		claims: "invalid claims",
	}
)

type testIDToken struct {
	claims string
}

func (t *testIDToken) Claims(v interface{}) error {
	return json.Unmarshal([]byte(t.claims), v)
}
