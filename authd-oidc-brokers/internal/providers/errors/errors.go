// Package errors provides custom error types which can be returned by the providers
//
// The package name conflicts with `errors` from the standard library.
// That's not ideal, but we're planning a major refactoring of the broker and
// provider packages in the future, so it's not worth the effort to fix this now.
//
//nolint:revive // See comment above
package errors

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
