package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ensureDirWithPerms creates a directory at path if it doesn't exist yet with perm as permissions.
// If the path exists, it will check if it’s a directory with those perms.
func ensureDirWithPerms(path string, perm os.FileMode, owner int) error {
	dir, err := os.Stat(path)
	if err == nil {
		if !dir.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}
		if dir.Mode() != (perm | fs.ModeDir) {
			return fmt.Errorf("permissions should be %v but are %v", perm|fs.ModeDir, dir.Mode())
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
