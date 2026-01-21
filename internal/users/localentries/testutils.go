package localentries

import (
	"github.com/canonical/authd/internal/testsdetection"
)

var originalDefaultOptions = defaultOptions

const (
	// Z_ForTests_GroupFilePathEnv is the env variable to set the group file path during
	// integration tests.
	// nolint:revive,nolintlint // We want to use underscores in the function name here.
	Z_ForTests_GroupFilePathEnv = "AUTHD_INTEGRATIONTESTS_GROUP_FILE_PATH"

	// Z_ForTests_GroupFileOutputPathEnv is the env variable to set the group file output
	// path during integration tests.
	// nolint:revive,nolintlint // We want to use underscores in the function name here.
	Z_ForTests_GroupFileOutputPathEnv = "AUTHD_INTEGRATIONTESTS_GROUP_OUTPUT_FILE_PATH"
)

// Z_ForTests_RestoreDefaultOptions restores the defaultOptions to their original values.
//
// nolint:revive,nolintlint // We want to use underscores in the function name here.
func Z_ForTests_RestoreDefaultOptions() {
	testsdetection.MustBeTesting()

	defaultOptions = originalDefaultOptions
}

// Z_ForTests_SetGroupPath sets the groupPath for the defaultOptions.
// Tests using this can't be run in parallel.
// Call Z_ForTests_RestoreDefaultOptions to restore the original value.
//
// nolint:revive,nolintlint // We want to use underscores in the function name here.
func Z_ForTests_SetGroupPath(inputGroupPath, outputGroupPath string) {
	testsdetection.MustBeTesting()

	defaultOptions.inputGroupPath = inputGroupPath
	defaultOptions.outputGroupPath = outputGroupPath
}
