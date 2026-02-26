// TiCS: disabled // Should only be built when running integration tests.

//go:build integrationtests

package main

import (
	"fmt"
	"os"

	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/testsdetection"
	"github.com/canonical/authd/internal/users/localentries"
	userslocking "github.com/canonical/authd/internal/users/locking"
)

// load any behaviour modifiers from env variable.
func init() {
	testsdetection.MustBeTesting()

	if os.Getenv("AUTHD_INTEGRATIONTESTS_CURRENT_USER_AS_ROOT") != "" {
		permissions.Z_ForTests_DefaultCurrentUserAsRoot()
	}

	grpFilePath := os.Getenv(localentries.Z_ForTests_GroupFilePathEnv)
	if grpFilePath == "" {
		panic(fmt.Sprintf("%q must be set", localentries.Z_ForTests_GroupFilePathEnv))
	}
	grpFileOutputPath := os.Getenv(localentries.Z_ForTests_GroupFileOutputPathEnv)
	if grpFileOutputPath == "" {
		grpFileOutputPath = grpFilePath
	}
	localentries.Z_ForTests_SetGroupPath(grpFilePath, grpFileOutputPath)

	userslocking.Z_ForTests_OverrideLocking()
}
