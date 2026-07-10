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
	// A PIN policy violation is CTAP2.1's forcePINChange signal, so no PIN the
	// user can enter here works: it needs its own dead end rather than the
	// wrong-PIN re-prompt or the blocked-PIN reinsert advice.
	require.ErrorIs(t, mapAssertionError(libfido2.ErrPinPolicyViolation), ErrPINChangeRequired)
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

	// FIDO_ERR_USER_ACTION_TIMEOUT (0x2f) and FIDO_ERR_PIN_BLOCKED (0x32):
	// the authenticator's own touch wait expired, and the hard PIN block after
	// exhausted retries. This vendored go-libfido2 has no named errors for
	// these codes (its ErrActionTimeout and ErrPinAuthBlocked are the distinct
	// 0x3a and 0x34), so they must be recognized by code: the former is the
	// only touch-timeout signal the broker's retry path can act on, the
	// latter requires an authenticator reset.
	require.ErrorIs(t, mapAssertionError(libfido2.Error{Code: fidoErrUserActionTimeout}), ErrTimeout)
	require.ErrorIs(t, mapAssertionError(libfido2.Error{Code: fidoErrPINBlocked}), ErrPINResetRequired)

	// Unclassified errors stay generic but keep their message.
	err := mapAssertionError(libfido2.ErrTX)
	require.NotErrorIs(t, err, ErrPINRequired)
	require.ErrorContains(t, err, "tx")
}
