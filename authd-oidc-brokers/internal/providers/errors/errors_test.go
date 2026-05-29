package errors_test

import (
	"errors"
	"testing"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/stretchr/testify/require"
)

func TestRetryWithDeviceAuthError(t *testing.T) {
	t.Parallel()

	t.Run("Error_returns_wrapped_error_message_when_error_is_set", func(t *testing.T) {
		t.Parallel()

		inner := errors.New("inner error")
		err := &providerErrors.RetryWithDeviceAuthError{Err: inner}
		require.Equal(t, "inner error", err.Error())
	})

	t.Run("Error_returns_default_message_when_error_is_nil", func(t *testing.T) {
		t.Parallel()

		err := &providerErrors.RetryWithDeviceAuthError{}
		require.Equal(t, "token acquisition failed, retry with device authentication", err.Error())
	})

	t.Run("Unwrap_returns_wrapped_error", func(t *testing.T) {
		t.Parallel()

		inner := errors.New("inner error")
		err := &providerErrors.RetryWithDeviceAuthError{Err: inner}
		require.Equal(t, inner, err.Unwrap())
		require.ErrorIs(t, err, inner)
	})

	t.Run("Unwrap_returns_nil_when_no_wrapped_error", func(t *testing.T) {
		t.Parallel()

		err := &providerErrors.RetryWithDeviceAuthError{}
		require.Nil(t, err.Unwrap())
	})

	t.Run("errors_As_matches_RetryWithDeviceAuthError", func(t *testing.T) {
		t.Parallel()

		inner := errors.New("cause")
		original := &providerErrors.RetryWithDeviceAuthError{Err: inner}
		var target *providerErrors.RetryWithDeviceAuthError
		require.ErrorAs(t, original, &target)
		require.Equal(t, original, target)
	})
}
