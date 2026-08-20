// Package himmelblau provides functions to use the libhimmelblau library.
package himmelblau

import (
	"fmt"
	"strconv"
	"strings"
)

// ErrDeviceDisabled is returned when the device is disabled in Microsoft Entra ID.
var ErrDeviceDisabled = fmt.Errorf("device is disabled in Microsoft Entra ID")

// ErrInvalidRedirectURI is returned when the redirect URI of the client application is missing or invalid.
var ErrInvalidRedirectURI = fmt.Errorf("invalid redirect URI")

// ErrMissingClientCredentials is returned when the token endpoint requires
// client credentials but none were supplied (AADSTS7000218).
var ErrMissingClientCredentials = fmt.Errorf("token endpoint requires client credentials (AADSTS7000218)")

// ErrDeviceAuthenticationFailed is returned when Microsoft Entra reports that
// the device itself failed authentication (AADSTS50155). Microsoft returns
// this generic code for several distinct situations, including (per
// Microsoft's docs and himmelblau-idm/himmelblau's handling of the same
// code) an administrator deleting the device object, but also a transient
// replication delay right after a device was (re-)registered. himmelblau-idm
// retries once after a short delay before treating a fresh registration's
// AADSTS50155 as final (see the DEVICE_AUTH_FAIL retry in
// unix_user_online_auth_step, src/common/src/idprovider/himmelblau.rs --
// NOT the check_new_device_enrollment_required! macro, which deletes the
// cached HSM keys unconditionally and has no retry/grace period).
//
// A live Entra tenant validation confirmed that administrator deletion of the
// device object produced AADSTS50155 during the Graph-token exchange. This
// confirms it as a valid self-heal signal for that operation and tenant, but
// not as a universal mapping for every tenant or authentication operation.
// Microsoft can still return a different code for another device-invalid
// scenario, and a freshly registered device may report AADSTS50155
// transiently while Entra replicates it. The latter is why this code should
// not be treated as proof that every AADSTS50155 is permanent without a
// bounded retry/grace period.
var ErrDeviceAuthenticationFailed = fmt.Errorf("device authentication failed in Microsoft Entra ID (AADSTS50155)")

// Entra AADSTS error codes as defined in
// https://learn.microsoft.com/en-us/entra/identity-platform/reference-error-codes
const (
	// AADSTS135011 Device used during the authentication is disabled.
	deviceDisabledErrorCode = 135011
	// AADSTS50011 InvalidReplyTo - The reply address is missing, misconfigured,
	// or doesn't match reply addresses configured for the app.
	invalidRedirectURIErrorCode = 50011
	// AADSTS7000218 RequestBodyMustContainClientAssertion - The request body
	// must contain a client_assertion or client_secret.
	missingClientCredentialsErrorCode = 7000218
	// AADSTS50155 DeviceAuthenticationFailed - the device used during
	// authentication is no longer known/valid to Microsoft Entra, e.g.
	// because an administrator deleted the device object. A live tenant
	// validation confirmed that administrator-deletion case; see the caveats
	// on ErrDeviceAuthenticationFailed for the remaining non-universal and
	// transient-race limitations.
	deviceAuthenticationFailedErrorCode = 50155
)

func tokenAcquisitionError(errorCodes []uint32, message string) error {
	for _, errorCode := range errorCodes {
		errorCodeStr := strconv.Itoa(int(errorCode))
		switch {
		// AADSTS error codes can have additional digits or subcodes appended
		// (e.g. AADSTS500113 as a variation of AADSTS50011).
		// Checking the prefix ensures we catch all variations of the base error code.
		case strings.HasPrefix(errorCodeStr, strconv.Itoa(deviceDisabledErrorCode)):
			return ErrDeviceDisabled
		case strings.HasPrefix(errorCodeStr, strconv.Itoa(invalidRedirectURIErrorCode)):
			return ErrInvalidRedirectURI
		case strings.HasPrefix(errorCodeStr, strconv.Itoa(missingClientCredentialsErrorCode)):
			return ErrMissingClientCredentials
		// Unlike the codes above, AADSTS50155/DEVICE_AUTH_FAIL has no
		// documented subcode family, and misclassifying an unrelated code as
		// this one is destructive (it clears the cached device
		// registration). Require an exact match rather than a prefix match
		// so an undocumented/future code that merely starts with "50155"
		// (e.g. a hypothetical AADSTS501559) falls through to the
		// non-destructive catch-all below instead of triggering
		// re-enrollment.
		case errorCode == deviceAuthenticationFailedErrorCode:
			return ErrDeviceAuthenticationFailed
		}
	}

	// The token acquisition failed for a reason we don't specifically
	// recognize. Unlike ErrDeviceAuthenticationFailed, this is NOT positive
	// evidence that the device registration itself is invalid, so callers
	// must not treat it as such (e.g. by clearing cached registration data).
	return TokenAcquisitionError{msg: fmt.Sprintf("error acquiring access token using refresh token: %v", message)}
}

// TokenAcquisitionError is returned when an error occurs while acquiring a token via libhimmelblau.
type TokenAcquisitionError struct {
	msg string
}

func (e TokenAcquisitionError) Error() string {
	return e.msg
}
