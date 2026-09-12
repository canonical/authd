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
			require.Equal(t, tc.expectedRet, got, "LoadAuthInfo should return the expected value")
		})
	}
}

func TestLoadAuthInfoLegacyTokenDefaultsInvalidationMarkerToFalse(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{
		"Token": {
			"access_token": "accesstoken",
			"refresh_token": "refreshtoken"
		},
		"DeviceRegistrationData": "bGVnYWN5LWRldmljZS1kYXRh"
	}`), 0600))

	got, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.Equal(t, []byte("legacy-device-data"), got.DeviceRegistrationData)
	require.False(t, got.DeviceRegistrationDataInvalidated,
		"legacy caches without the marker must load as not invalidated")
	require.False(t, got.DeviceRegistrationDataValidationPending,
		"legacy caches without the field must load as not pending")
	require.False(t, got.GroupsResolved,
		"legacy caches without the field must not be treated as groups-resolved")
}

func TestLoadAuthInfoLegacyTokenWithProviderIDButWithoutGroupsDoesNotInferGroupsResolved(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{
		"Token": {
			"access_token": "accesstoken",
			"refresh_token": "refreshtoken"
		},
		"UserInfo": {
			"name": "legacy-user",
			"provider_id": "legacy-user-id"
		},
		"DeviceRegistrationData": "bGVnYWN5LWRldmljZS1kYXRh"
	}`), 0600))

	got, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.False(t, got.GroupsResolved,
		"a provider ID without cached groups must not imply that group lookup succeeded")
}

func TestLoadAuthInfoLegacyTokenInfersGroupsResolved(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "parent", "token.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{
		"Token": {
			"access_token": "accesstoken",
			"refresh_token": "refreshtoken"
		},
		"UserInfo": {
			"name": "legacy-user",
			"provider_id": "legacy-user-id",
			"groups": [{"name": "legacy-group", "ugid": "legacy-group-id"}]
		}
	}`), 0600))

	got, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.True(t, got.GroupsResolved,
		"legacy caches with authenticated user information must preserve cached-group fallback")
	require.Equal(t, []info.Group{{Name: "legacy-group", UGID: "legacy-group-id"}}, got.UserInfo.Groups)
}
