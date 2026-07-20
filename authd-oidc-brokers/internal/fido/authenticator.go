//go:build withmsentraid

package fido

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/canonical/authd/log"
	libfido2 "github.com/keys-pub/go-libfido2"
)

const (
	// fidoErrUVBlocked is CTAP2's FIDO_ERR_UV_BLOCKED (0x3c): the
	// authenticator's built-in user verification (e.g. fingerprint) is
	// temporarily blocked, most commonly after too many unmatched touches on a
	// biometric device. This vendored go-libfido2 predates CTAP2.1 and has no
	// named error for it, so it surfaces only as the generic
	// libfido2.Error{Code: 0x3c}.
	fidoErrUVBlocked = 0x3c
	// fidoErrUVInvalid is CTAP2's FIDO_ERR_UV_INVALID (0x3f): built-in user
	// verification failed. Prompting for the PIN lets the same device satisfy
	// user verification without unplugging it.
	fidoErrUVInvalid = 0x3f
)

// relyingPartyID is the WebAuthn relying party ID that Entra ID registers
// credentials under.
const relyingPartyID = "login.microsoft.com"

// Authenticator performs WebAuthn Get ceremonies with the first connected
// FIDO2 device via libfido2. The zero value is ready to use.
type Authenticator struct{}

// DevicePresent reports whether at least one FIDO device is connected. It is
// used to gate the FIDO auth modes: a session that cannot reach a device
// (e.g. SSH into a headless server) must fall back to another flow.
func (Authenticator) DevicePresent() bool {
	locations, err := libfido2.DeviceLocations()
	if err != nil {
		return false
	}
	return len(locations) > 0
}

// DeviceRequiresPIN reports whether the connected device needs a client PIN
// for user verification. Devices with built-in user verification (e.g. a
// fingerprint reader) or without a configured PIN do not.
func (Authenticator) DeviceRequiresPIN() (bool, error) {
	device, err := firstDevice()
	if err != nil {
		return false, err
	}

	info, err := device.Info()
	if errors.Is(err, libfido2.ErrNotFIDO2) {
		// U2F-only devices have no PIN concept.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query FIDO device info: %v", err)
	}

	var pinSet, builtinUV bool
	for _, option := range info.Options {
		switch option.Name {
		case "clientPin":
			pinSet = option.Value == libfido2.True
		case "uv":
			builtinUV = option.Value == libfido2.True
		}
	}
	requiresPIN := pinSet && !builtinUV
	// A wrong decision here is the usual cause of a failed ceremony, so record
	// what the device actually reported.
	log.Debugf(context.Background(), "FIDO device capabilities: clientPin=%v uv=%v -> requiresPIN=%v", pinSet, builtinUV, requiresPIN)
	return requiresPIN, nil
}

// Assert performs the WebAuthn Get ceremony for the given MFA-flow challenge
// and allow list, blocking until the user touches the device, the device
// times out, or ctx is canceled. It returns the assertion JSON that
// libhimmelblau's acquire_token_by_mfa_flow expects as auth_data.
//
// pin may be empty when the device does not require one (see
// DeviceRequiresPIN). Failures that the broker can act on are reported as the
// package's sentinel errors (ErrPINRequired, ErrPINInvalid, ...).
func (Authenticator) Assert(ctx context.Context, challenge string, allowList []string, pin string) (string, error) {
	device, err := firstDevice()
	if err != nil {
		return "", err
	}

	credentialIDs, err := decodeAllowList(allowList)
	if err != nil {
		return "", err
	}

	clientData := clientDataJSON(challenge)
	clientDataHash := sha256.Sum256(clientData)

	opts := &libfido2.AssertionOpts{UP: libfido2.True}
	if pin == "" {
		// Ask for built-in user verification (e.g. fingerprint) when no PIN
		// is used; with a PIN, user verification is provided by the PIN
		// protocol and requesting UV as well fails on PIN-only devices.
		if builtinUV, err := hasBuiltinUV(device); err == nil && builtinUV {
			opts.UV = libfido2.True
		}
	}

	type assertResult struct {
		assertion *libfido2.Assertion
		err       error
	}
	resultCh := make(chan assertResult, 1)
	go func() {
		assertion, err := device.Assertion(relyingPartyID, clientDataHash[:], credentialIDs, pin, opts)
		resultCh <- assertResult{assertion, err}
	}()

	var result assertResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		// Interrupt the ceremony so the device stops blinking, then wait for
		// the in-flight call to return: the Device must not be garbage
		// collected while libfido2 still uses it.
		if err := device.Cancel(); err != nil {
			result = <-resultCh
			return "", errors.Join(ErrCanceled, fmt.Errorf("failed to cancel FIDO assertion: %v", err))
		}
		<-resultCh
		return "", ErrCanceled
	}
	if result.err != nil {
		// The raw error carries the CTAP code that mapAssertionError collapses
		// into a sentinel; log it with the verification we asked for.
		log.Debugf(ctx, "FIDO assertion ceremony failed (pinProvided=%v, uvRequested=%v): %v", pin != "", opts.UV == libfido2.True, result.err)
		return "", mapAssertionError(result.err)
	}

	authData, err := rawAuthData(result.assertion.AuthDataCBOR)
	if err != nil {
		return "", err
	}

	return buildAssertionJSON(
		result.assertion.CredentialID,
		clientData,
		authData,
		result.assertion.Sig,
		result.assertion.User.ID,
	)
}

// firstDevice returns the first connected FIDO device. Sessions with several
// connected devices are not supported: the ceremony runs on the first one.
func firstDevice() (*libfido2.Device, error) {
	locations, err := libfido2.DeviceLocations()
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate FIDO devices: %v", err)
	}
	if len(locations) == 0 {
		return nil, ErrNoDevice
	}
	// The ceremony always uses locations[0]; flag when several are connected so
	// a wrong-device pick is visible.
	if len(locations) > 1 {
		log.Debugf(context.Background(), "%d FIDO devices connected; using the first (%q)", len(locations), locations[0].Path)
	}
	device, err := libfido2.NewDevice(locations[0].Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open FIDO device %q: %v", locations[0].Path, err)
	}
	return device, nil
}

// hasBuiltinUV reports whether the device performs user verification on its
// own (e.g. a fingerprint reader).
func hasBuiltinUV(device *libfido2.Device) (bool, error) {
	info, err := device.Info()
	if err != nil {
		return false, err
	}
	for _, option := range info.Options {
		if option.Name == "uv" {
			return option.Value == libfido2.True, nil
		}
	}
	return false, nil
}

// mapAssertionError translates libfido2 errors to the package's sentinel
// errors where the broker can act on them, keeping the original error text
// for the logs.
func mapAssertionError(err error) error {
	switch {
	case errors.Is(err, libfido2.ErrPinRequired):
		return ErrPINRequired
	case errors.Is(err, libfido2.ErrPinNotSet):
		// Prompting for a PIN cannot help: the key has none configured.
		return fmt.Errorf("the security key requires a PIN but none is configured on it: %v", err)
	case errors.Is(err, libfido2.ErrPinInvalid):
		return ErrPINInvalid
	case errors.Is(err, libfido2.ErrPinAuthBlocked), errors.Is(err, libfido2.ErrPinPolicyViolation):
		return ErrPINBlocked
	case errors.Is(err, libfido2.ErrActionTimeout):
		return ErrTimeout
	case errors.Is(err, libfido2.ErrKeepaliveCancel):
		return ErrCanceled
	case errors.Is(err, libfido2.ErrNoCredentials):
		return ErrNoCredentials
	default:
		var ferr libfido2.Error
		if errors.As(err, &ferr) && (ferr.Code == fidoErrUVBlocked || ferr.Code == fidoErrUVInvalid) {
			// The device supports both a PIN and built-in UV, so Assert
			// requested fingerprint verification (see the pin == "" branch
			// there); with fingerprint verification failed or blocked, the
			// PIN is the next recovery path. CTAP2 accepts it as an equally
			// valid verification method regardless of what blocked UV.
			return ErrPINRequired
		}
		return fmt.Errorf("FIDO assertion failed: %v", err)
	}
}
