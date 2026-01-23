// Package localgrouptestutils export users test functionalities used by other packages to change cmdline and group file.
package localgrouptestutils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testsdetection"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/localentries"
	"github.com/stretchr/testify/require"
)

func init() {
	// No import outside of testing environment.
	testsdetection.MustBeTesting()
}

var (
	defaultOptions struct {
		groupPath string
	}
)

// SetGroupPath sets the groupPath for the defaultOptions.
// Tests using this can't be run in parallel.
func SetGroupPath(groupPath string) {
	defaultOptions.groupPath = groupPath
}

// RequireGroupFile compare the output of the generated group file with the
// golden file.
func RequireGroupFile(t *testing.T, destGroupFile, goldenGroupPath string) {
	t.Helper()

	destGroupBackupFile := destGroupFile + "-"
	groupSuffix := ".group"
	backupSuffix := groupSuffix + ".backup"

	// TODO: this should be extracted in testutils, but still allow post-treatement of file like sorting.
	referenceFilePath := goldenGroupPath + groupSuffix
	if golden.UpdateEnabled() {
		// The file may already not exists.
		_ = os.Remove(goldenGroupPath + groupSuffix)
		_ = os.Remove(goldenGroupPath + backupSuffix)
		referenceFilePath = destGroupFile
	}

	var shouldExists bool
	if _, err := os.Stat(referenceFilePath); err == nil {
		shouldExists = true
	}
	if !shouldExists {
		require.NoFileExists(t, destGroupFile, "UpdateLocalGroups should not update the group files, but it did")
		require.NoFileExists(t, destGroupBackupFile, "UpdateLocalGroups should not update the group file, but it did")
		return
	}

	gotGroups, err := os.ReadFile(destGroupFile)
	require.NoError(t, err, "Teardown: could not read dest group file")

	golden.CheckOrUpdate(t, string(gotGroups), golden.WithPath(goldenGroupPath),
		golden.WithSuffix(groupSuffix))

	gotGroupsBackup, err := os.ReadFile(destGroupBackupFile)
	require.NoError(t, err, "Teardown: could not read dest group backup file")
	golden.CheckOrUpdate(t, string(gotGroupsBackup), golden.WithPath(goldenGroupPath),
		golden.WithSuffix(backupSuffix))
}

// SetupGroupMock setup the group mock and return the path to the file where the
// output will be written.
//
// Tests that require this can not be run in parallel.
func SetupGroupMock(t *testing.T, groupsFilePath string) string {
	t.Helper()

	testsdetection.MustBeTesting()

	t.Cleanup(localentries.Z_ForTests_RestoreDefaultOptions)

	var tempGroupFile string
	if exists, _ := fileutils.FileExists(groupsFilePath); exists {
		tempGroupFile = filepath.Join(t.TempDir(), filepath.Base(groupsFilePath))
		err := fileutils.CopyFile(groupsFilePath, tempGroupFile)
		require.NoError(t, err, "failed to copy group file for testing")
	}

	destGroupFile := filepath.Join(t.TempDir(), "group")
	localentries.Z_ForTests_SetGroupPath(tempGroupFile, destGroupFile)

	return destGroupFile
}
