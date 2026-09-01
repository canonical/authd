// Package himmelblau provides functions to use the libhimmelblau library.
package himmelblau

import (
	"fmt"
	"strings"
)

// ErrDeviceDisabled is returned when the device is disabled in Microsoft Entra ID.
var ErrDeviceDisabled = fmt.Errorf("device is disabled in Microsoft Entra ID")

// ErrInvalidRedirectURI is returned when the redirect URI of the client application is missing or invalid.
var ErrInvalidRedirectURI = fmt.Errorf("invalid redirect URI")

// ErrMissingClientCredentials is returned when the token endpoint requires
// client credentials but none were supplied.
var ErrMissingClientCredentials = fmt.Errorf("token endpoint requires client credentials")

// ErrDeviceAuthenticationFailed is returned when Microsoft Entra reports that
// the device itself failed authentication (AADSTS50155). Microsoft returns
// this generic code for several distinct situations, including (per
// Microsoft's docs and himmelblau-idm/himmelblau's handling of the same
// code) an administrator deleting the device object, but also a transient
// replication delay right after a device was (re-)registered. himmelblau-idm
// retries once after a short delay before treating a fresh registration's
// AADSTS50155 as final (see the DEVICE_AUTH_FAIL retry in
// unix_user_online_auth_step, src/common/src/idprovider/himmelblau.rs).
//
// An administrator deleting the device object is the manual positive control
// for this signal, but it is not a universal mapping for every tenant or
// authentication operation. Microsoft can still return a different code for
// another device-invalid scenario, and a freshly registered device may report
// AADSTS50155 transiently while Entra replicates it. The latter is why this
// code should not be treated as proof that every AADSTS50155 is permanent
// without a bounded retry/grace period.
var ErrDeviceAuthenticationFailed = fmt.Errorf("device authentication failed in Microsoft Entra ID")

// Entra AADSTS error codes as defined in
// https://learn.microsoft.com/en-us/entra/identity-platform/reference-error-codes
const (
	// AADSTS135011 Device used during the authentication is disabled.
	deviceDisabledErrorCode = 135011
	// AADSTS50011 InvalidReplyTo - The reply address is missing, misconfigured,
	// or doesn't match reply addresses configured for the app.
	invalidRedirectURIErrorCode = 50011
	// AADSTS500113 - No reply address is registered for the application.
	// A separate documented code, but the same reply-address misconfiguration
	// family as AADSTS50011.
	noReplyAddressErrorCode = 500113
	// AADSTS7000218 RequestBodyMustContainClientAssertion - The request body
	// must contain a client_assertion or client_secret.
	missingClientCredentialsErrorCode = 7000218
	// AADSTS50155 DeviceAuthenticationFailed - the device used during
	// authentication is no longer known/valid to Microsoft Entra, e.g.
	// because an administrator deleted the device object. See the caveats on
	// ErrDeviceAuthenticationFailed for the non-universal and transient-race
	// limitations.
	deviceAuthenticationFailedErrorCode = 50155
)

func tokenAcquisitionError(errorCodes []uint32, message string) error {
	for _, errorCode := range errorCodes {
		// Match AADSTS codes exactly. Every code handled here is a
		// documented, distinct error. A wrong guess is costly: 50155
		// triggers re-enrollment, while the redirect-URI and
		// device-disabled codes deny the login. An unrecognized code falls
		// through to the catch-all below, which preserves cached state.
		switch errorCode {
		case invalidRedirectURIErrorCode, noReplyAddressErrorCode:
			return withAADSTSErrorCode(ErrInvalidRedirectURI, errorCode, message)
		case missingClientCredentialsErrorCode:
			return withAADSTSErrorCode(ErrMissingClientCredentials, errorCode, message)
		case deviceDisabledErrorCode:
			return withAADSTSErrorCode(ErrDeviceDisabled, errorCode, message)
		case deviceAuthenticationFailedErrorCode:
			return withAADSTSErrorCode(ErrDeviceAuthenticationFailed, errorCode, message)
		}
	}

	// The token acquisition failed for a reason we don't specifically
	// recognize. Unlike ErrDeviceAuthenticationFailed, this is NOT positive
	// evidence that the device registration itself is invalid, so callers
	// must not treat it as such (e.g. by clearing cached registration data).
	return fmt.Errorf("error acquiring access token using refresh token: %v", message)
}

func withAADSTSErrorCode(err error, errorCode uint32, message string) error {
	aadstsCode := fmt.Sprintf("AADSTS%d", errorCode)
	if strings.Contains(message, aadstsCode) {
		return fmt.Errorf("%w: %s", err, message)
	}
	return fmt.Errorf("%w (%s): %s", err, aadstsCode, message)
}
