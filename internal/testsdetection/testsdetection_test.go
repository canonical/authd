package testsdetection_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/canonical/authd/internal/testsdetection"
	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
)

func TestMustBeTestingInTests(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		require.Nil(t, r, "MustBeTesting should not panic as we are running in tests")
	}()

	testsdetection.MustBeTesting()
}

func TestMustBeTestingInProcess(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		integrationtestsTag bool

		wantPanic bool
	}{
		"Pass_when_called_in_an_integration_tests_build": {integrationtestsTag: true, wantPanic: false},

		"Error_(panics)_when_called_in_non_tests_and_no_integration_tests": {integrationtestsTag: false, wantPanic: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			args := []string{"run"}
			if tc.integrationtestsTag {
				args = append(args, "-tags=integrationtests")
			}
			if testutils.CoverDirForTests() != "" {
				args = append(args, "-cover")
			}
			if testutils.IsRace() {
				args = append(args, "-race")
			}
			args = append(args, "testdata/binary.go")

			// Execute our subprocess
			cmd := exec.Command("go", args...)
			cmd.Env = testutils.AppendCovEnv(os.Environ())
			out, err := cmd.CombinedOutput()

			if tc.wantPanic {
				require.Errorf(t, err, "MustBeTesting should have panicked the subprocess: %s", out)
				return
			}
			require.NoErrorf(t, err, "MustBeTesting should have returned without panicking the subprocess: %s", out)
		})
	}
}
