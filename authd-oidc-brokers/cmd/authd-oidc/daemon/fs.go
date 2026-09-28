package daemon

import (
	"fmt"
	"os"
	"syscall"
)

type invalidConfigPermissionsError struct {
	message string
}

func (e *invalidConfigPermissionsError) Error() string {
	return e.message
}

func newInvalidConfigPermissionsError(format string, args ...any) *invalidConfigPermissionsError {
	return &invalidConfigPermissionsError{message: fmt.Sprintf(format, args...)}
}

type safeFileModeMismatchError struct {
	*invalidConfigPermissionsError
}

func (e *safeFileModeMismatchError) Unwrap() error {
	return e.invalidConfigPermissionsError
}

func newSafeFileModeMismatchError(format string, args ...any) *safeFileModeMismatchError {
	return &safeFileModeMismatchError{
		invalidConfigPermissionsError: newInvalidConfigPermissionsError(format, args...),
	}
}

// ensureDirWithOwner creates a directory at path with the given perm if it doesn't exist yet.
// If the path exists, it will check that it is a directory owned by owner, but will not fail if
// the permissions differ from perm, as incorrect directory permissions are not a security risk as
// long as the files inside have secure permissions.
func ensureDirWithOwner(path string, perm os.FileMode, owner int) error {
	dir, err := os.Stat(path)
	if err == nil {
		if !dir.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}
		stat, ok := dir.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
		}
		if int(stat.Uid) != owner {
			return newInvalidConfigPermissionsError("directory %q is owned by UID %d but should be owned by %d",
				path, stat.Uid, owner)
		}

		return nil
	}
	return os.Mkdir(path, perm)
}

func checkTrustedDir(path string, owner int) (bool, error) {
	dir, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if dir.Mode()&os.ModeSymlink != 0 {
		return false, newInvalidConfigPermissionsError("directory %q must not be a symlink", path)
	}
	if !dir.IsDir() {
		return false, &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}

	stat, ok := dir.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return false, newInvalidConfigPermissionsError("directory %q is owned by %d but should be owned by %d",
			path, stat.Uid, owner)
	}
	if dir.Mode().Perm()&0022 != 0 {
		return false, newInvalidConfigPermissionsError("directory %q has insecure permissions %v: it must not be writable by group or others",
			path, dir.Mode().Perm())
	}

	return dir.Mode().Perm()&0077 == 0, nil
}

func checkFilePerms(path string, perm os.FileMode, owner int, parentDirOwnerOnly bool) error {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		fileInfo, err = os.Stat(path)
		if err != nil {
			return err
		}
		// The link's parent directory does not protect the target.
		parentDirOwnerOnly = false
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("path %q is not a regular file", path)
	}

	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return newInvalidConfigPermissionsError("file %q is owned by %d but should be owned by %d",
			path, stat.Uid, owner)
	}
	if fileInfo.Mode() == perm {
		return nil
	}

	mode := fileInfo.Mode().Perm()
	if mode&0400 == 0 {
		return newInvalidConfigPermissionsError("file %q must be readable by its owner (permissions are %v)",
			path, mode)
	}
	if parentDirOwnerOnly {
		// Older brokers used owner-only drop-in directories to protect files with broader modes.
		return nil
	}
	if mode&0022 != 0 {
		return newInvalidConfigPermissionsError("file %q must not be writable by group or others (permissions are %v)",
			path, mode)
	}

	return newSafeFileModeMismatchError("file %q has permissions: %v (should be %v)",
		path, fileInfo.Mode(), perm)
}
