// Package errors provides custom error types which can be returned by the providers
package errors

import (
	"fmt"
)

// ForDisplayError is an error type for errors that are meant to be displayed to the user.
type ForDisplayError struct {
	message string
}

func (e ForDisplayError) Error() string {
	return e.message
}

// NewForDisplayError creates a new ForDisplayError with the given format and arguments.
func NewForDisplayError(format string, v ...interface{}) ForDisplayError {
	return ForDisplayError{message: fmt.Sprintf(format, v...)}
}
