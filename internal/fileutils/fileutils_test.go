package fileutils_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testutils"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// errAny represents any error type, for testing purposes.
var errAny = errors.New("this is an error type for testing purposes")

func TestFileExists(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
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
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
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

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
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

func TestCopyFile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sourceDoesNotExist bool
		destExists         bool
		destIsDir          bool
		parentDoesNotExist bool
		fileMode           os.FileMode

		wantError bool
	}{
		"Creates_file_when_it_does_not_exist":            {destExists: false},
		"Preserves_the_file_permission":                  {destExists: false, fileMode: 0o400},
		"Preserves_the_file_execution bit":               {destExists: false, fileMode: 0o700},
		"Does_not_return_error_when_file_already_exists": {destExists: true},

		"Returns_error_when_source_does_not_exists":          {sourceDoesNotExist: true, destIsDir: true, wantError: true},
		"Returns_error_when_file_is_a_directory":             {destIsDir: true, wantError: true},
		"Returns_error_when_parent_directory_does_not_exist": {parentDoesNotExist: true, wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			srcPath := filepath.Join(tempDir, "file")
			destPath := filepath.Join(tempDir, "dest")

			if tc.destExists && !tc.destIsDir {
				err := os.WriteFile(srcPath, []byte(uuid.NewString()), 0o600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			if tc.destIsDir {
				destPath = filepath.Join(tempDir, "dir")
				err := os.Mkdir(destPath, 0o700)
				require.NoError(t, err, "Mkdir should not return an error")
			}

			if tc.parentDoesNotExist {
				destPath = filepath.Join(tempDir, "dir", "file")
			}

			var wantContent string
			if !tc.sourceDoesNotExist {
				wantContent = uuid.NewString()
				err := os.WriteFile(srcPath, []byte(wantContent), 0o600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			if tc.fileMode == 0 {
				tc.fileMode = 0o600
			}

			if !tc.sourceDoesNotExist {
				err := os.Chmod(srcPath, tc.fileMode)
				require.NoError(t, err, "Chmod should not return an error")
			}

			err := fileutils.CopyFile(srcPath, destPath)
			if tc.wantError {
				require.Error(t, err, "Copy should return an error")

				exists, err := fileutils.FileExists(destPath)
				require.NoError(t, err, "FileExists should not return an error")
				require.Equal(t, tc.destExists || tc.destIsDir, exists, "File should exist")
				return
			}

			require.NoError(t, err, "Copy should not return an error")

			fileInfo, err := os.Stat(destPath)
			require.NoError(t, err, "Stat should not return an error")

			require.False(t, fileInfo.IsDir(), "File %q should not be a dir", destPath)
			require.Equal(t, tc.fileMode, fileInfo.Mode(), "File %q mode does not match: %O vs %O",
				destPath, tc.fileMode, fileInfo.Mode())

			if fileInfo.Mode() < 400 {
				// Let's mark the file readable again.
				err := os.Chmod(destPath, 0o400)
				require.NoError(t, err, "Chmod should not return an error")
			}

			copyContent, err := os.ReadFile(destPath)
			require.NoError(t, err, "ReadFile %q should not return an error", destPath)
			require.Equal(t, wantContent, string(copyContent), "File contents does not match")
		})
	}
}

func TestLrename(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sourceDoesNotExist     bool
		destIsFile             bool
		destIsSymlink          bool
		destIsDanglingSymlink  bool
		destIsDir              bool
		destIsUnreadable       bool
		destParentDoesNotExist bool

		wantError error
	}{
		"Successfully_rename_file_if_destination_does_not_exist": {},
		"Successfully_rename_file_if_destination_is_a_file":      {destIsFile: true},
		"Successfully_rename_file_if_destination_is_a_symlink":   {destIsSymlink: true},
		"Successfully_rename_file_if_destination_is_unreadable":  {destIsFile: true, destIsUnreadable: true},

		"Error_when_source_does_not_exist":                       {sourceDoesNotExist: true, wantError: errAny},
		"Error_when_destination_is_a_directory":                  {destIsDir: true, wantError: errAny},
		"Error_when_destination_parent_directory_does_not_exist": {destParentDoesNotExist: true, wantError: errAny},
		"Error_when_destination_is_a_dangling_symlink":           {destIsDanglingSymlink: true, wantError: fileutils.SymlinkResolutionError{}},
		"Error_unwrap_when_destination_is_a_dangling_symlink":    {destIsDanglingSymlink: true, wantError: os.ErrNotExist},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			srcPath := filepath.Join(tempDir, "source")
			destPath := filepath.Join(tempDir, "dest")

			if !tc.sourceDoesNotExist {
				err := os.WriteFile(srcPath, []byte("test content"), 0o600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			if tc.destIsFile {
				err := os.WriteFile(destPath, []byte("existing content"), 0o600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			if tc.destIsSymlink {
				symlinkTarget := filepath.Join(tempDir, "symlink_target")
				err := os.WriteFile(symlinkTarget, []byte("symlink content"), 0o600)
				require.NoError(t, err, "WriteFile should not return an error")

				err = os.Symlink(symlinkTarget, destPath)
				require.NoError(t, err, "Symlink should not return an error")
			}

			if tc.destIsDanglingSymlink {
				err := os.Symlink("nonexistent_target", destPath)
				require.NoError(t, err, "Symlink should not return an error")
			}

			if tc.destIsDir {
				err := os.Mkdir(destPath, 0o700)
				require.NoError(t, err, "Mkdir should not return an error")
			}

			if tc.destIsUnreadable {
				err := os.Chmod(destPath, 0o000)
				require.NoError(t, err, "Chmod should not return an error")
				// Restore permissions after test
				defer func() {
					//nolint:gosec // G302 Permissions 0700 are not insecure for a directory
					err := os.Chmod(destPath, 0700)
					require.NoError(t, err, "Chmod should not return an error")
				}()
			}

			if tc.destParentDoesNotExist {
				destPath = filepath.Join(tempDir, "nonexistent", "dest")
			}

			err := fileutils.Lrename(srcPath, destPath)
			if errors.Is(tc.wantError, errAny) {
				require.Error(t, err, "Lrename should return an error")
				return
			}
			if tc.wantError != nil {
				require.ErrorIs(t, err, tc.wantError, "Error should match")
				return
			}
			require.NoError(t, err, "Lrename should not return an error")

			// Verify the source no longer exists
			exists, err := fileutils.FileExists(srcPath)
			require.NoError(t, err, "FileExists should not return an error")
			require.False(t, exists, "Source file should no longer exist")

			// Verify the destination exists
			exists, err = fileutils.FileExists(destPath)
			require.NoError(t, err, "FileExists should not return an error")
			require.True(t, exists, "Destination file should exist")
		})
	}
}

func TestLockDir(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// First lock should succeed
	t.Log("Acquiring first lock")
	unlock, err := fileutils.LockDir(tempDir)
	require.NoError(t, err, "LockDir should not return an error")

	// Second lock should block, so we run it in a goroutine and check it doesn't return immediately
	unlockCh := make(chan func() error, 1)
	go func() {
		t.Log("Acquiring second lock (should block)")
		unlock2, err := fileutils.LockDir(tempDir)
		t.Logf("Second LockDir returned with error: %v", err)
		unlockCh <- unlock2
	}()
	select {
	case <-unlockCh:
		require.Fail(t, "LockDir should block when trying to lock an already locked directory")
	case <-time.After(testutils.MultipliedSleepDuration(100 * time.Millisecond)):
		// Expected behavior, LockDir is blocking
	}

	// Unlock the first lock
	t.Log("Releasing first lock")
	err = unlock()
	require.NoError(t, err, "Unlock should not return an error")

	// Now we should be able to acquire the lock again
	select {
	case unlock = <-unlockCh:
		// Expected behavior, LockDir returned after the first lock was released
		t.Log("Releasing lock")
		err = unlock()
		require.NoError(t, err, "Unlock should not return an error")
	case <-time.After(testutils.MultipliedSleepDuration(5 * time.Second)):
		require.Fail(t, "LockDir should have returned after the first lock was released")
	}
}
