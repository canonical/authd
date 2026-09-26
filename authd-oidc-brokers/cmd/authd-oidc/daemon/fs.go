package daemon

import (
	"fmt"
	"os"
	"syscall"
)

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
			return fmt.Errorf("owner should be %d but is %d", owner, stat.Uid)
		}

		return nil
	}
	return os.Mkdir(path, perm)
}

func checkFilePerms(path string, perm os.FileMode) error {
	dir, err := os.Stat(path)
	if err != nil {
		return err
	}

	if !dir.Mode().IsRegular() {
		return fmt.Errorf("path %v is not a regular file", path)
	}

	if dir.Mode() != perm {
		return fmt.Errorf("file %v has insecure permissions: %v (should be %v)", path, dir.Mode(), perm)
	}
	return nil
}
