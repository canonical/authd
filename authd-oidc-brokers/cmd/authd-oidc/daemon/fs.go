package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ensureDirWithPerms creates a directory at path if it doesn't exist yet with perm as permissions.
// If the path exists, it will check if it’s a directory with those perms.
func ensureDirWithPerms(path string, perm os.FileMode) error {
	dir, err := os.Stat(path)
	if err == nil {
		if !dir.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}
		if dir.Mode() != (perm | fs.ModeDir) {
			return fmt.Errorf("permissions %v don't match what we desired: %v", dir.Mode(), perm|fs.ModeDir)
		}

		return nil
	}
	return os.Mkdir(path, perm)
}
