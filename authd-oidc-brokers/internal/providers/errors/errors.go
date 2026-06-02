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

// RetryWithDeviceAuthError is returned when token acquisition fails and the user should retry
// using device authentication (e.g. because the device was deleted by an administrator).
type RetryWithDeviceAuthError struct {
	Err error
}

func (e *RetryWithDeviceAuthError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "token acquisition failed, retry with device authentication"
}

func (e *RetryWithDeviceAuthError) Unwrap() error {
	return e.Err
}

// ForDisplayError is an error type for errors that are meant to be displayed to the user.
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
