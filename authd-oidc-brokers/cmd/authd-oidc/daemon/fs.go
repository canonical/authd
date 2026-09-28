package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
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

func checkTrustedDir(path string, owner int) error {
	dir, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !dir.IsDir() {
		return &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}

	stat, ok := dir.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("owner should be %d but is %d", owner, stat.Uid)
	}
	if dir.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("directory %q has insecure permissions %v: it must not be writable by group or others",
			path, dir.Mode().Perm())
	}

	return nil
}

// openParentDirNoFollow pins the immediate parent without following a symlink.
// Higher ancestors are trusted: deployments must keep them root-controlled and
// not writable by untrusted users. This helper does not validate those ancestors.
func openParentDirNoFollow(path string) (int, string, error) {
	if path == "" {
		return -1, "", &os.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return -1, "", fmt.Errorf("failed to make file path %q absolute: %w", path, err)
	}
	baseName := filepath.Base(absPath)
	if baseName == string(filepath.Separator) {
		return -1, "", &os.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
	}

	parentPath := filepath.Dir(absPath)
	dirFD, err := unix.Open(parentPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", &os.PathError{Op: "open", Path: parentPath, Err: err}
	}

	return dirFD, baseName, nil
}

func checkTrustedDirFD(dirFD int, path string, owner int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(dirFD, &stat); err != nil {
		return fmt.Errorf("failed to get directory information for %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("owner should be %d but is %d", owner, stat.Uid)
	}
	if stat.Mode&0022 != 0 {
		return fmt.Errorf("directory %q has insecure permissions %v: it must not be writable by group or others",
			path, os.FileMode(stat.Mode&0777))
	}

	return nil
}

func setFilePerms(path string, perm os.FileMode, owner int) error {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("path %v is not a regular file", path)
	}

	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get syscall.Stat_t for %s", path)
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("file %q is owned by %d but should be owned by %d", path, stat.Uid, owner)
	}

	if fileInfo.Mode() != perm {
		parentFD, baseName, err := openParentDirNoFollow(path)
		if err != nil {
			return fmt.Errorf("cannot securely open parent directory for file %q: %w", path, err)
		}
		defer unix.Close(parentFD)

		fileFD, err := unix.Openat(parentFD, baseName, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return &os.PathError{Op: "open", Path: path, Err: err}
		}
		defer unix.Close(fileFD)

		if err := setFilePermsAt(path, parentFD, baseName, fileFD, perm, owner); err != nil {
			return err
		}
	}
	return nil
}

func setFilePermsAt(path string, parentFD int, baseName string, fileFD int, perm os.FileMode, owner int) error {
	if err := checkTrustedDirFD(parentFD, filepath.Dir(path), owner); err != nil {
		return fmt.Errorf("cannot secure file %q because its parent directory is not trusted: %w", path, err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		return fmt.Errorf("failed to get file information for %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("path %v is not a regular file", path)
	}
	if int(stat.Uid) != owner {
		return fmt.Errorf("file %q is owned by %d but should be owned by %d", path, stat.Uid, owner)
	}

	mode := fileModeToUnix(perm)
	if stat.Mode&07777 == mode {
		return nil
	}

	if err := unix.Fchmodat(fileFD, "", mode, unix.AT_EMPTY_PATH); err == nil {
		return nil
	} else if !errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("failed to set permissions on file %q to %v: %w", path, perm, err)
	}

	return fchmodAtFallback(path, parentFD, baseName, stat, mode, perm)
}

func fileModeToUnix(mode os.FileMode) uint32 {
	unixMode := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		unixMode |= unix.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		unixMode |= unix.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		unixMode |= unix.S_ISVTX
	}
	return unixMode
}

func fchmodAtFallback(path string, parentFD int, baseName string, expected unix.Stat_t, mode uint32, perm os.FileMode) error {
	// Older kernels lack Fchmodat2, so use a readable or writable descriptor instead.
	fileFD, err := unix.Openat(parentFD, baseName,
		unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("failed to set permissions on file %q to %v: %w", path, perm, err)
		}
		readErr := err
		fileFD, err = unix.Openat(parentFD, baseName,
			unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("failed to set permissions on file %q to %v: opening for reading failed: %v; opening for writing failed: %w",
				path, perm, readErr, err)
		}
	}
	defer unix.Close(fileFD)

	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		return fmt.Errorf("failed to get file information for %q: %w", path, err)
	}
	if stat.Dev != expected.Dev || stat.Ino != expected.Ino {
		return fmt.Errorf("file %q changed while setting permissions", path)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("path %v is not a regular file", path)
	}
	if int(stat.Uid) != int(expected.Uid) {
		return fmt.Errorf("file %q changed owner while setting permissions", path)
	}

	if err := unix.Fchmod(fileFD, mode); err != nil {
		return fmt.Errorf("failed to set permissions on file %q to %v: %w", path, perm, err)
	}
	return nil
}
