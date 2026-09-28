package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSetFilePermsRejectsSymlinkedParent(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "target")
	require.NoError(t, os.Mkdir(targetDir, 0700), "Setup: could not create target directory")

	targetFile := filepath.Join(targetDir, "broker.conf")
	//nolint:gosec // The test verifies that permission repair does not follow a symlink.
	require.NoError(t, os.WriteFile(targetFile, nil, 0644), "Setup: could not create target file")

	linkDir := filepath.Join(tmpDir, "link")
	require.NoError(t, os.Symlink(targetDir, linkDir), "Setup: could not create directory symlink")

	err := setFilePerms(filepath.Join(linkDir, "broker.conf"), 0600, os.Geteuid())
	require.Error(t, err, "permission changes must not follow a symlinked immediate parent")

	fileInfo, err := os.Stat(targetFile)
	require.NoError(t, err, "Could not stat target file")
	require.Equal(t, os.FileMode(0644), fileInfo.Mode().Perm(),
		"Target file permissions should not be changed")
}

func TestSetFilePermsAllowsSymlinkedHigherAncestor(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "target")
	configDir := filepath.Join(targetDir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0700), "Setup: could not create config directory")

	targetFile := filepath.Join(configDir, "broker.conf")
	//nolint:gosec // The test verifies permission repair below a trusted symlinked ancestor.
	require.NoError(t, os.WriteFile(targetFile, nil, 0644), "Setup: could not create config file")

	linkDir := filepath.Join(tmpDir, "link")
	require.NoError(t, os.Symlink(targetDir, linkDir), "Setup: could not create directory symlink")

	for _, perm := range []os.FileMode{0600, 0400} {
		err := setFilePerms(filepath.Join(linkDir, "config", "broker.conf"), perm, os.Geteuid())
		require.NoError(t, err, "Higher ancestors are trusted and may contain administrator-managed symlinks")

		fileInfo, err := os.Stat(targetFile)
		require.NoError(t, err, "Could not stat config file")
		require.Equal(t, perm, fileInfo.Mode().Perm(),
			"Config file permissions should match the requested mode")
	}
}

func TestSetFilePermsAtUsesOpenedFileAfterParentIsReplaced(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dropInDir := filepath.Join(configDir, "broker.conf.d")
	targetDir := filepath.Join(tmpDir, "target")
	require.NoError(t, os.Mkdir(configDir, 0700), "Setup: could not create config directory")
	require.NoError(t, os.Mkdir(dropInDir, 0700), "Setup: could not create drop-in directory")
	require.NoError(t, os.Mkdir(targetDir, 0700), "Setup: could not create target directory")

	filePath := filepath.Join(dropInDir, "extra.conf")
	targetPath := filepath.Join(targetDir, "extra.conf")
	//nolint:gosec // The test verifies that permission repair uses the original file descriptor.
	require.NoError(t, os.WriteFile(filePath, nil, 0644), "Setup: could not create drop-in file")
	//nolint:gosec // The target file must remain insecure so the test can detect redirection.
	require.NoError(t, os.WriteFile(targetPath, nil, 0644), "Setup: could not create target file")
	// This is the writable ancestor that could replace broker.conf.d by a symlink.
	//nolint:gosec // The test requires a writable ancestor to exercise the symlink swap.
	require.NoError(t, os.Chmod(configDir, 0777), "Setup: could not make config directory writable")

	parentFD, baseName, err := openParentDirNoFollow(filePath)
	require.NoError(t, err, "Could not open the drop-in directory")
	defer unix.Close(parentFD)

	fileFD, err := unix.Openat(parentFD, baseName, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	require.NoError(t, err, "Could not open the drop-in file")
	defer unix.Close(fileFD)

	movedDropInDir := filepath.Join(configDir, "broker.conf.d.original")
	require.NoError(t, os.Rename(dropInDir, movedDropInDir), "Setup: could not move drop-in directory")
	require.NoError(t, os.Symlink(targetDir, dropInDir), "Setup: could not replace drop-in directory with a symlink")

	require.NoError(t, setFilePermsAt(filePath, parentFD, baseName, fileFD, 0600, os.Geteuid()),
		"Permission repair should use the already-open file and directory")

	fileInfo, err := os.Stat(filepath.Join(movedDropInDir, "extra.conf"))
	require.NoError(t, err, "Could not stat the opened drop-in file")
	require.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm(),
		"Permissions should be changed on the file opened before the directory swap")

	fileInfo, err = os.Stat(targetPath)
	require.NoError(t, err, "Could not stat the symlink target")
	require.Equal(t, os.FileMode(0644), fileInfo.Mode().Perm(),
		"Permissions must not be changed on the symlink target")
}
