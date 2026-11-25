package pam_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunnerPackagedPasswordControl(t *testing.T) {
	t.Parallel()

	want, err := PackagedControl(Password, map[string]string{"end": "3"})
	require.NoError(t, err, "Setup: could not get the packaged control")
	got, err := ParseControl(RunnerPackagedPasswordControl(3))
	require.NoError(t, err, "Setup: could not parse the control")
	require.Equal(t, want, got, "The runner password control should match the packaged one")
}
