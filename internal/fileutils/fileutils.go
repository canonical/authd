// Package fileutils provides utility functions for file operations.
package fileutils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// FileExists checks if a file exists at the given path.
func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return !errors.Is(err, os.ErrNotExist), nil
}

// IsDirEmpty checks if the specified directory is empty.
func IsDirEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// Touch creates an empty file at the given path, if it doesn't already exist.
func Touch(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return file.Close()
}

// CopyFile copies a file from a source to a destination path, preserving the file mode.
func CopyFile(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	fileInfo, err := src.Stat()
	if err != nil {
		return err
	}

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileInfo.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	return dst.Sync()
}

// SymlinkResolutionError is the error returned when symlink resolution fails.
type SymlinkResolutionError struct {
	msg string
	err error
}

func (e SymlinkResolutionError) Error() string {
	return fmt.Sprintf("%s: %v", e.msg, e.err)
}

func (e SymlinkResolutionError) Unwrap() error {
	return e.err
}

// Is makes this error insensitive to the internal values.
func (e SymlinkResolutionError) Is(target error) bool {
	return target == SymlinkResolutionError{}
}

// Lrename renames a file or directory, resolving symlinks in the destination path.
// If the symlink resolution fails, it returns a SymlinkResolutionError.
func Lrename(oldPath, newPath string) error {
	// Resolve the destination path if it's a symlink.
	fi, err := os.Lstat(newPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return os.Rename(oldPath, newPath)
	}

	newPath, err = filepath.EvalSymlinks(newPath)
	if err != nil {
		return SymlinkResolutionError{msg: "failed to resolve symlinks in Lrename", err: err}
	}

	return os.Rename(oldPath, newPath)
}

// LockDir creates a lock file in the specified directory and acquires an exclusive lock on it.
// It blocks until the lock is available and returns an unlock function to release the lock.
func LockDir(dir string) (func() error, error) {
	lockPath := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}

	unlock := func() error {
		if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}

	return unlock, nil
}

// ChownRecursiveFrom changes ownership of files and directories under the
// specified root directory from the current UID/GID (fromUID, fromGID) to the
// new UID/GID (toUID, toGID).
//
// It mirrors the behavior of chown_tree from shadow-utils:
// https://github.com/shadow-maint/shadow/blob/e7ccd3df6845c184d155a2dd573f52d239c94337/lib/chowndir.c#L129-L141
//
// Symlinks are not followed.
//
// If toUID or toGID is negative, the UID or GID is not changed.
func ChownRecursiveFrom(root string, fromUID, fromGID uint32, toUID, toGID int32) error {
	if toUID < 0 && toGID < 0 {
		return nil
	}

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("failed to get raw stat for %q", path)
		}

		if toUID >= 0 && stat.Uid == fromUID {
			if err := os.Lchown(path, int(toUID), -1); err != nil {
				return fmt.Errorf("failed to change ownership: %w", err)
			}
		}

		if toGID >= 0 && stat.Gid == fromGID {
			if err := os.Lchown(path, -1, int(toGID)); err != nil {
				return fmt.Errorf("failed to change group ownership: %w", err)
			}
		}

		return nil
	})
}
