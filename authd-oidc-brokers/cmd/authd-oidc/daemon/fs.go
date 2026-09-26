package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var errInvalidConfigPermissions = errors.New("invalid broker configuration permissions")

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
			return fmt.Errorf("%w: directory %q is owned by %d but should be owned by %d",
				errInvalidConfigPermissions, path, stat.Uid, owner)
		}

		return nil
	}
	return os.Mkdir(path, perm)
}

func checkTrustedDir(path string, owner int) error {
	dir, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if dir.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: directory %q must not be a symlink", errInvalidConfigPermissions, path)
	}
	if !dir.IsDir() {
		return &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}

	stat, ok := dir.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("%w: directory %q is owned by %d but should be owned by %d",
			errInvalidConfigPermissions, path, stat.Uid, owner)
	}
	if dir.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%w: directory %q has insecure permissions %v: it must not be writable by group or others",
			errInvalidConfigPermissions, path, dir.Mode().Perm())
	}

	return nil
}

func checkFilePerms(path string, perm os.FileMode, owner int) error {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: file %q must not be a symlink", errInvalidConfigPermissions, path)
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("path %q is not a regular file", path)
	}

	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("%w: file %q is owned by %d but should be owned by %d",
			errInvalidConfigPermissions, path, stat.Uid, owner)
	}
	if fileInfo.Mode() != perm {
		return fmt.Errorf("%w: file %q has insecure permissions: %v (should be %v)",
			errInvalidConfigPermissions, path, fileInfo.Mode(), perm)
	}
	return nil
}
