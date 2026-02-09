package errno_test

import (
	"errors"
	"testing"

	"github.com/canonical/authd/internal/errno"
	"github.com/stretchr/testify/require"
)

func TestNoError(t *testing.T) {
	t.Parallel()

	errno.Lock()
	t.Cleanup(errno.Unlock)

	require.NoError(t, errno.Get())
}

func TestGetWithoutLocking(t *testing.T) {
	// This test can't be parallel, since other tests may locking meanwhile.

	require.PanicsWithValue(t, "Using errno without locking!", func() { _ = errno.Get() })
}

func TestSetWithoutLocking(t *testing.T) {
	// This test can't be parallel, since other tests may locking meanwhile.

	require.PanicsWithValue(t, "Using errno without locking!", func() { errno.Set(nil) })
}

func TestSetInvalidError(t *testing.T) {
	t.Parallel()

	errno.Lock()
	t.Cleanup(errno.Unlock)

	require.PanicsWithValue(t, "Not a valid errno value", func() { errno.Set(errors.New("invalid")) })
}

func TestErrorValues(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		err error
	}{
		"No_error":                  {},
		"No_such_file_or_directory": {err: errno.ErrNoEnt},
		"No_such_process":           {err: errno.ErrSrch},
		"Bad_file_descriptor":       {err: errno.ErrBadf},
		"Operation_not_permitted":   {err: errno.ErrPerm},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			errno.Lock()
			t.Cleanup(errno.Unlock)

			errno.Set(tc.err)
			t.Logf("Checking for error %v", tc.err)
			require.ErrorIs(t, errno.Get(), tc.err, "Errno is not matching")
		})
	}
}
