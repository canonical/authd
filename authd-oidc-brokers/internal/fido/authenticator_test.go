//go:build withmsentraid

package fido

import (
	"testing"

	libfido2 "github.com/keys-pub/go-libfido2"
	"github.com/stretchr/testify/require"
)

func TestMapAssertionError(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, mapAssertionError(libfido2.ErrPinRequired), ErrPINRequired)
	require.ErrorIs(t, mapAssertionError(libfido2.ErrPinInvalid), ErrPINInvalid)
	require.ErrorIs(t, mapAssertionError(libfido2.ErrPinAuthBlocked), ErrPINBlocked)
	require.ErrorIs(t, mapAssertionError(libfido2.ErrPinPolicyViolation), ErrPINBlocked)
	require.ErrorIs(t, mapAssertionError(libfido2.ErrActionTimeout), ErrTimeout)
	require.ErrorIs(t, mapAssertionError(libfido2.ErrKeepaliveCancel), ErrCanceled)

	// A key without a configured PIN must NOT ask the user for a PIN.
	require.NotErrorIs(t, mapAssertionError(libfido2.ErrPinNotSet), ErrPINRequired)

	require.ErrorIs(t, mapAssertionError(libfido2.ErrNoCredentials), ErrNoCredentials)

	// FIDO_ERR_UV_BLOCKED (0x3c) and FIDO_ERR_UV_INVALID (0x3f): a Bio-series
	// key's fingerprint verification is blocked or failed. This vendored
	// go-libfido2 has no named errors for these CTAP2.1 codes, so they must be
	// recognized by code and routed to the PIN fallback rather than failing
	// generically; unplugging the key is not required.
	require.ErrorIs(t, mapAssertionError(libfido2.Error{Code: fidoErrUVBlocked}), ErrPINRequired)
	require.ErrorIs(t, mapAssertionError(libfido2.Error{Code: fidoErrUVInvalid}), ErrPINRequired)

	// Unclassified errors stay generic but keep their message.
	err := mapAssertionError(libfido2.ErrTX)
	require.NotErrorIs(t, err, ErrPINRequired)
	require.ErrorContains(t, err, "tx")
}
