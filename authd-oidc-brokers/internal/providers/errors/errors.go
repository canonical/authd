// Package errors provides custom error types which can be returned by the providers
//
// The package name conflicts with `errors` from the standard library.
// That's not ideal, but we're planning a major refactoring of the broker and
// provider packages in the future, so it's not worth the effort to fix this now.
package errors

import stderrors "errors"

// ErrDeviceDisabled is returned when the device is disabled in the identity provider.
var ErrDeviceDisabled = stderrors.New("device is disabled")

// ErrInvalidRedirectURI is returned when the redirect URI of the client application is missing or invalid.
var ErrInvalidRedirectURI = stderrors.New("invalid redirect URI")

// RetryWithDeviceCodeFlowError is returned when token acquisition fails and the user should retry
// using device code flow (e.g. because the device was deleted by an administrator).
type RetryWithDeviceCodeFlowError struct {
	Err error
}

// RetryWithDeviceAuthError is kept as an alias for backward compatibility.
type RetryWithDeviceAuthError = RetryWithDeviceCodeFlowError

func (e *RetryWithDeviceCodeFlowError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "token acquisition failed, retry with device code flow"
}

func (e *RetryWithDeviceCodeFlowError) Unwrap() error {
	return e.Err
}

// ForDisplayError wraps an error with a message that is safe to display to the user.
// It does not indicate whether a caller may fall back to cached data.
type ForDisplayError struct {
	Message string
	Err     error
}

func (e *ForDisplayError) Error() string {
	return e.Message
}

func (e *ForDisplayError) Unwrap() error {
	return e.Err
}

// AuthoritativeError marks an error as an authoritative provider result, such
// as an identity or configuration failure. Callers can use errors.As to
// distinguish it from a transient failure. It may be wrapped by a
// ForDisplayError when the result should also be shown to the user.
type AuthoritativeError struct {
	Err error
}

func (e *AuthoritativeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "authoritative provider error"
}

func (e *AuthoritativeError) Unwrap() error {
	return e.Err
}

// NonAuthoritativeError marks a refresh failure for which cached credentials
// may be used when provider access checks are optional. It may be wrapped by a
// ForDisplayError when the failure should also be shown to the user.
type NonAuthoritativeError struct {
	Err error
}

func (e *NonAuthoritativeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "non-authoritative provider error"
}

func (e *NonAuthoritativeError) Unwrap() error {
	return e.Err
}

// MissingClaimError is an error type for missing claims in the ID token or the claims returned by the UserInfo endpoint.
type MissingClaimError struct {
	Claim string
}

func (e *MissingClaimError) Error() string {
	return e.Claim + " claim is missing"
}

// NewMissingClaimError creates a new MissingClaimError for the specified claim.
func NewMissingClaimError(claim string) error {
	return &MissingClaimError{Claim: claim}
}
