package errors_test

import (
	"errors"
	"testing"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/stretchr/testify/require"
)

func TestErrorWrapperContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		newError       func(error) error
		defaultMessage string
		assertAs       func(*testing.T, error)
	}{
		{
			name: "RetryWithDeviceAuthError",
			newError: func(err error) error {
				return &providerErrors.RetryWithDeviceAuthError{Err: err}
			},
			defaultMessage: "token acquisition failed, retry with device code flow",
			assertAs: func(t *testing.T, err error) {
				var target *providerErrors.RetryWithDeviceAuthError
				require.ErrorAs(t, err, &target)
				require.Equal(t, err, target)
			},
		},
		{
			name: "AuthoritativeError",
			newError: func(err error) error {
				return &providerErrors.AuthoritativeError{Err: err}
			},
			defaultMessage: "authoritative provider error",
			assertAs: func(t *testing.T, err error) {
				var target *providerErrors.AuthoritativeError
				require.ErrorAs(t, err, &target)
				require.Equal(t, err, target)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			inner := errors.New("inner error")
			wrapped := tc.newError(inner)
			require.Equal(t, "inner error", wrapped.Error())
			require.Equal(t, inner, errors.Unwrap(wrapped))
			require.ErrorIs(t, wrapped, inner)
			tc.assertAs(t, wrapped)

			withoutInner := tc.newError(nil)
			require.Equal(t, tc.defaultMessage, withoutInner.Error())
			require.Nil(t, errors.Unwrap(withoutInner))
		})
	}
}
