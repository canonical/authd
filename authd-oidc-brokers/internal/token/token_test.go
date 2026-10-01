package token_test

import (
	"encoding/json"
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
		expectedRet  *token.AuthCachedInfo
		fileExists   bool
		invalidJSON  bool
		missingToken bool

		wantError    bool
		wantNotExist bool
	}{
		"Successfully_load_token_from_existing_file": {fileExists: true, expectedRet: testToken},
		"Error_when_file_does_not_exist":             {wantError: true, wantNotExist: true},
		"Error_when_file_contains_invalid_JSON":      {fileExists: true, invalidJSON: true, wantError: true},
		"Error_when_file_has_no_OAuth_token":         {fileExists: true, missingToken: true, wantError: true},
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
				} else if tc.missingToken {
					err = os.WriteFile(tokenPath, []byte(`{"ExtraFields":{}}`), 0600)
					require.NoError(t, err, "WriteFile should not return an error")
				} else {
					err = token.CacheAuthInfo(tokenPath, testToken)
					require.NoError(t, err, "CacheAuthInfo should not return an error")
				}
			}

			got, err := token.LoadAuthInfo(tokenPath)
			if tc.wantError {
				require.Error(t, err, "LoadAuthInfo should return an error")
				if tc.wantNotExist {
					require.ErrorIs(t, err, os.ErrNotExist, "missing token errors should preserve os.ErrNotExist")
				}
				return
			}
			require.NoError(t, err, "LoadAuthInfo should not return an error")
			require.Equal(t, tc.expectedRet, got, "LoadAuthInfo should return the expected value")
		})
	}
}

func TestCacheAuthInfoRoundTripsUnixIDsAndPreservesCacheLayout(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	cached := &token.AuthCachedInfo{
		Token: &oauth2.Token{AccessToken: "access", RefreshToken: "refresh"},
		UserInfo: info.User{
			Name:   "alice",
			UID:    &uid,
			Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}},
		},
	}
	tokenPath := filepath.Join(t.TempDir(), "issuer", "provider-id", "token.json")

	require.NoError(t, token.CacheAuthInfo(tokenPath, cached))
	fileInfo, err := os.Stat(tokenPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())
	directoryInfo, err := os.Stat(filepath.Dir(tokenPath))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), directoryInfo.Mode().Perm())

	data, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	userInfo, ok := raw["UserInfo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1001), userInfo["uid"])
	groups, ok := userInfo["groups"].([]any)
	require.True(t, ok)
	firstGroup, ok := groups[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(2001), firstGroup["gid"])

	loaded, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.Equal(t, cached.UserInfo, loaded.UserInfo)
}

func TestLoadAuthInfoAcceptsLegacyUserInfoWithoutUnixIDs(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{"Token":{"access_token":"legacy-token"},"UserInfo":{"name":"alice","groups":[{"name":"engineering","ugid":"group-id"}]}}`), 0600))

	loaded, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.Nil(t, loaded.UserInfo.UID)
	require.Nil(t, loaded.UserInfo.Groups[0].GID)
}
