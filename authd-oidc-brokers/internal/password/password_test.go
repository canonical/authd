package password_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/fileutils"
	"github.com/ubuntu/authd-oidc-brokers/internal/password"
)

func TestHashAndStorePassword(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		password        string
		path            string
		pathExists      bool
		parentDirExists bool

		wantErr bool
	}{
		"Success_when_password_file_and_parent_dir_do_not_exist_yet": {password: "test123"},
		"Success_when_parent_directory_already_exists":               {password: "test123", parentDirExists: true},
		"Success_when_password_file_already_exists":                  {password: "test123", pathExists: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.pathExists {
				// The parent directory must also exist for the file to exist.
				tc.parentDirExists = true
			}

			parentDir := t.TempDir()
			if !tc.parentDirExists {
				err := os.Remove(parentDir)
				require.NoError(t, err, "Removing parent directory failed")
			}
			path := filepath.Join(parentDir, "password")

			if tc.pathExists {
				err := fileutils.Touch(path)
				require.NoError(t, err, "Creating empty password file failed")
			}

			err := password.HashAndStorePassword(tc.password, path)
			if err != nil {
				t.Fatalf("HashAndStorePassword() failed: %v", err)
			}
		})
	}
}

func TestCheckPassword(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		password     string
		pathToRead   string
		writeGarbage bool

		wantMatch     bool
		expectedError error
	}{
		"Success_when_password_matches":         {password: "test123", wantMatch: true},
		"No_match_when_password_does_not_match": {password: "not-test123", wantMatch: false},

		"Error_when_password_file_does_not_exist":   {password: "test123", pathToRead: "nonexistent", expectedError: os.ErrNotExist},
		"Error_when_password_file_contains_garbage": {password: "test123", writeGarbage: true, expectedError: base64.CorruptInputError(0)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parentDir := t.TempDir()
			path := filepath.Join(parentDir, "password")

			if tc.pathToRead == "" {
				tc.pathToRead = path
			}

			err := password.HashAndStorePassword("test123", path)
			require.NoError(t, err, "HashAndStorePassword() failed")

			if tc.writeGarbage {
				err := os.WriteFile(path, []byte{0x00}, 0o600)
				require.NoError(t, err, "Writing garbage to password file failed")
			}

			match, err := password.CheckPassword(tc.password, tc.pathToRead)
			if tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError, "CheckPassword() failed")
			} else {
				require.NoError(t, err, "CheckPassword() failed")
			}

			require.Equal(t, tc.wantMatch, match, "CheckPassword() returned unexpected result")
		})
	}
}
