package token_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

var testToken = &token.AuthCachedInfo{
	Token: &oauth2.Token{
		AccessToken:  "accesstoken",
		RefreshToken: "refreshtoken",
	},
	RawIDToken: "rawidtoken",
	UserInfo: info.User{
		Name:       "foo",
		ProviderID: "saved-user-id",
		Home:       "/home/foo",
		Gecos:      "foo",
		Shell:      "/usr/bin/bash",
		Groups: []info.Group{
			{Name: "token-test-group", UGID: "12345"},
		},
	},
}

func TestCacheAuthInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		existingParentDir bool
		existingFile      bool
		fileIsDir         bool
		parentIsFile      bool

		wantError bool
	}{
		"Successfully_store_token_with_non_existing_parent_directory": {},
		"Successfully_store_token_with_existing_parent_directory":     {existingParentDir: true},
		"Successfully_store_token_with_existing_file":                 {existingParentDir: true, existingFile: true},

		"Error_when_file_exists_and_is_a_directory": {existingParentDir: true, existingFile: true, fileIsDir: true, wantError: true},
		"Error_when_parent_directory_is_a_file":     {existingParentDir: true, parentIsFile: true, wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")

			if tc.existingParentDir && !tc.parentIsFile {
				err := os.MkdirAll(filepath.Dir(tokenPath), 0700)
				require.NoError(t, err, "MkdirAll should not return an error")
			}
			if tc.existingFile && !tc.fileIsDir {
				err := os.WriteFile(tokenPath, []byte("existing file"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}
			if tc.fileIsDir {
				err := os.MkdirAll(tokenPath, 0700)
				require.NoError(t, err, "MkdirAll should not return an error")
			}
			if tc.parentIsFile {
				parentPath := filepath.Dir(tokenPath)
				err := os.WriteFile(parentPath, []byte("existing file"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			err := token.CacheAuthInfo(tokenPath, testToken)
			if tc.wantError {
				require.Error(t, err, "CacheAuthInfo should return an error")
				return
			}
			require.NoError(t, err, "CacheAuthInfo should not return an error")
		})
	}
}

func TestLoadAuthInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		expectedRet *token.AuthCachedInfo
		fileExists  bool
		invalidJSON bool

		wantError bool
	}{
		"Successfully_load_token_from_existing_file": {fileExists: true, expectedRet: testToken},
		"Error_when_file_does_not_exist":             {wantError: true},
		"Error_when_file_contains_invalid_JSON":      {fileExists: true, invalidJSON: true, wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")
			if tc.fileExists {
				err := os.MkdirAll(filepath.Dir(tokenPath), 0700)
				require.NoError(t, err, "MkdirAll should not return an error")

				if tc.invalidJSON {
					err = os.WriteFile(tokenPath, []byte("invalid json"), 0600)
					require.NoError(t, err, "WriteFile should not return an error")
				} else {
					err = token.CacheAuthInfo(tokenPath, testToken)
					require.NoError(t, err, "CacheAuthInfo should not return an error")
				}
			}

			got, err := token.LoadAuthInfo(tokenPath)
			if tc.wantError {
				require.Error(t, err, "LoadAuthInfo should return an error")
				return
			}
			require.NoError(t, err, "LoadAuthInfo should not return an error")
			// The marker is only stored when set: a cache with groups but no
			// marker is indistinguishable from one written before the marker
			// existed, so the decoder records it as resolved.
			expected := *tc.expectedRet
			expected.GroupsResolved = true
			require.Equal(t, &expected, got, "LoadAuthInfo should return the expected value")
		})
	}
}

// TestLoadAuthInfoLegacyToken verifies how caches written before the
// GroupsResolved and registration-timestamp fields existed are loaded: only a
// cache that recorded authenticated user information counts as groups-resolved,
// and none of them carries a registration timestamp.
func TestLoadAuthInfoLegacyToken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		content            string
		wantData           []byte
		wantGroupsResolved bool
		wantGroups         []info.Group
	}{
		"Cache_without_authenticated_user_is_not_groups_resolved": {
			content: `{
	"Token": {
		"access_token": "accesstoken",
		"refresh_token": "refreshtoken"
	},
	"DeviceRegistrationData": "bGVnYWN5LWRldmljZS1kYXRh"
}`,
			wantData: []byte("legacy-device-data"),
		},
		"Provider_ID_without_groups_is_not_groups_resolved": {
			content: `{
	"Token": {
		"access_token": "accesstoken",
		"refresh_token": "refreshtoken"
	},
	"UserInfo": {
		"name": "legacy-user",
		"provider_id": "legacy-user-id"
	},
	"DeviceRegistrationData": "bGVnYWN5LWRldmljZS1kYXRh"
}`,
			wantData: []byte("legacy-device-data"),
		},
		"Authenticated_user_with_groups_is_groups_resolved": {
			content: `{
	"Token": {
		"access_token": "accesstoken",
		"refresh_token": "refreshtoken"
	},
	"UserInfo": {
		"name": "legacy-user",
		"provider_id": "legacy-user-id",
		"groups": [{"name": "legacy-group", "ugid": "legacy-group-id"}]
	}
}`,
			wantGroupsResolved: true,
			wantGroups:         []info.Group{{Name: "legacy-group", UGID: "legacy-group-id"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
			require.NoError(t, os.WriteFile(tokenPath, []byte(tc.content), 0600))

			got, err := token.LoadAuthInfo(tokenPath)
			require.NoError(t, err)
			require.Equal(t, tc.wantData, got.DeviceRegistrationData)
			require.Equal(t, tc.wantGroupsResolved, got.GroupsResolved,
				"a legacy cache must only count as groups-resolved when it recorded authenticated user information")
			require.Equal(t, tc.wantGroups, got.UserInfo.Groups)
			require.Zero(t, got.DeviceRegistrationDataObtainedAt,
				"legacy caches without the registration timestamp must load without one")
		})
	}
}
