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
// the device itself failed authentication (AADSTS50155). Entra returns this
// code both when the device object is gone (for example after an administrator
// deleted it) and, transiently, while a newly registered device has not been
// replicated yet. Callers must therefore retry a fresh registration for a
// bounded time before treating the failure as final, like himmelblau-idm does
// (see the DEVICE_AUTH_FAIL retry in unix_user_online_auth_step,
// src/common/src/idprovider/himmelblau.rs).
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
	// AADSTS50155 DeviceAuthenticationFailed; see ErrDeviceAuthenticationFailed.
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
			return withAADSTSErrorCode(fmt.Errorf("%w (AADSTS codes %v)", ErrInvalidRedirectURI, errorCodes),
				errorCode, message)
		case missingClientCredentialsErrorCode:
			return withAADSTSErrorCode(fmt.Errorf("%w (AADSTS codes %v)", ErrMissingClientCredentials, errorCodes),
				errorCode, message)
		case deviceDisabledErrorCode:
			return withAADSTSErrorCode(fmt.Errorf("%w (AADSTS codes %v)", ErrDeviceDisabled, errorCodes),
				errorCode, message)
		case deviceAuthenticationFailedErrorCode:
			return withAADSTSErrorCode(fmt.Errorf("%w (AADSTS codes %v)", ErrDeviceAuthenticationFailed, errorCodes),
				errorCode, message)
		}
	}

	// The token acquisition failed for a reason we don't specifically
	// recognize. Unlike ErrDeviceAuthenticationFailed, this is NOT positive
	// evidence that the device registration itself is invalid, so callers
	// must not treat it as such (e.g. by clearing cached registration data).
	// Keep the codes in the message: libhimmelblau can report them only through
	// the codes list, and the code is what an administrator needs to diagnose
	// the failure.
	return fmt.Errorf("error acquiring access token using refresh token (AADSTS codes %v): %v", errorCodes, message)
}

func withAADSTSErrorCode(err error, errorCode uint32, message string) error {
	aadstsCode := fmt.Sprintf("AADSTS%d", errorCode)
	if containsAADSTSCode(message, aadstsCode) {
		return fmt.Errorf("%w: %s", err, message)
	}
	return fmt.Errorf("%w (%s): %s", err, aadstsCode, message)
}

// containsAADSTSCode reports whether message mentions exactly code. A plain
// substring search would also match a longer code that starts with it, so a
// message holding AADSTS500113 would suppress adding the distinct AADSTS50011
// and hide that code from the administrator.
func containsAADSTSCode(message, code string) bool {
	for rest := message; ; {
		index := strings.Index(rest, code)
		if index < 0 {
			return false
		}
		rest = rest[index+len(code):]
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			return true
		}
	}
}
