// Package errors provides custom error types which can be returned by the providers
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
