package info_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
)

func TestNewUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name   string
		home   string
		uuid   string
		shell  string
		gecos  string
		groups []info.Group
	}{
		"Create_a_new_user": {
			name:   "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},

		// Default values
		"Create_a_new_user_with_default_home": {
			name:   "test-user",
			home:   "",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},
		"Create_a_new_user_with_default_shell": {
			name:   "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},
		"Create_a_new_user_with_default_gecos": {name: "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantHome := tc.home
			if tc.home == "" {
				wantHome = tc.name
			}

			wantShell := tc.shell
			if tc.shell == "" {
				wantShell = "/usr/bin/bash"
			}

			wantGecos := tc.gecos
			if tc.gecos == "" {
				wantGecos = tc.name
			}

			got := info.NewUser(tc.name, tc.home, tc.uuid, tc.shell, tc.gecos, tc.groups)
			require.Equal(t, tc.name, got.Name, "Name does not match the expected value")
			require.Equal(t, wantHome, got.Home, "Home does not match the expected value")
			require.Equal(t, tc.uuid, got.UUID, "UUID does not match the expected value")
			require.Equal(t, wantShell, got.Shell, "Shell does not match the expected value")
			require.Equal(t, wantGecos, got.Gecos, "Gecos does not match the expected value")
			require.Equal(t, tc.groups, got.Groups, "Groups do not match the expected value")
		})
	}
}
