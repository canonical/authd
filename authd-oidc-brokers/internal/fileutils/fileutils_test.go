package fileutils_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/fileutils"
)

func TestFileExists(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name            string
		fileExists      bool
		parentDirIsFile bool

		wantExists bool
		wantError  bool
	}{
		"Returns_true_when_file_exists":                      {fileExists: true, wantExists: true},
		"Returns_false_when_file_does_not_exist":             {fileExists: false, wantExists: false},
		"Returns_false_when_parent_directory_does_not_exist": {fileExists: false, wantExists: false},

		"Error_when_parent_directory_is_a_file": {parentDirIsFile: true, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			path := filepath.Join(tempDir, "file")
			if tc.fileExists {
				err := fileutils.Touch(path)
				require.NoError(t, err, "Touch should not return an error")
			}
			if tc.parentDirIsFile {
				path = filepath.Join(tempDir, "file", "file")
				err := fileutils.Touch(filepath.Join(tempDir, "file"))
				require.NoError(t, err, "Touch should not return an error")
			}

			exists, err := fileutils.FileExists(path)
			if tc.wantError {
				require.Error(t, err, "FileExists should return an error")
			} else {
				require.NoError(t, err, "FileExists should not return an error")
			}
			require.Equal(t, tc.wantExists, exists, "FileExists should return the expected result")
		})
	}
}

func TestIsDirEmpty(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		isEmpty      bool
		isFile       bool
		doesNotExist bool

		wantEmpty bool
		wantError bool
	}{
		"Returns_true_when_directory_is_empty":      {isEmpty: true, wantEmpty: true},
		"Returns_false_when_directory_is_not_empty": {wantEmpty: false},

		"Error_when_directory_does_not_exist": {doesNotExist: true, wantError: true},
		"Error_when_directory_is_a_file":      {isFile: true, wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			path := filepath.Join(tempDir, "dir")

			if !tc.doesNotExist {
				err := os.Mkdir(path, 0o700)
				require.NoError(t, err, "Mkdir should not return an error")
			}

			if !tc.isEmpty && !tc.doesNotExist && !tc.isFile {
				err := fileutils.Touch(filepath.Join(tempDir, "dir", "file"))
				require.NoError(t, err, "Touch should not return an error")
			}
			if tc.isFile {
				path = filepath.Join(tempDir, "file")
				err := fileutils.Touch(path)
				require.NoError(t, err, "Touch should not return an error")
			}

			empty, err := fileutils.IsDirEmpty(path)
			if tc.wantError {
				require.Error(t, err, "IsDirEmpty should return an error")
			} else {
				require.NoError(t, err, "IsDirEmpty should not return an error")
			}
			require.Equal(t, tc.wantEmpty, empty, "IsDirEmpty should return the expected result")
		})
	}
}

func TestTouch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name               string
		fileExists         bool
		fileIsDir          bool
		parentDoesNotExist bool

		wantError bool
	}{
		"Creates_file_when_it_does_not_exist":            {fileExists: false},
		"Does_not_return_error_when_file_already_exists": {fileExists: true},

		"Returns_error_when_file_is_a_directory":             {fileIsDir: true, wantError: true},
		"Returns_error_when_parent_directory_does_not_exist": {parentDoesNotExist: true, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			path := filepath.Join(tempDir, "file")

			if tc.fileExists && !tc.fileIsDir {
				err := fileutils.Touch(path)
				require.NoError(t, err, "Touch should not return an error")
			}

			if tc.fileIsDir {
				path = filepath.Join(tempDir, "dir")
				err := os.Mkdir(path, 0o700)
				require.NoError(t, err, "Mkdir should not return an error")
			}

			if tc.parentDoesNotExist {
				path = filepath.Join(tempDir, "dir", "file")
			}

			err := fileutils.Touch(path)
			if tc.wantError {
				require.Error(t, err, "Touch should return an error")
				return
			}

			require.NoError(t, err, "Touch should not return an error")
		})
	}
}
