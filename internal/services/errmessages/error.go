package errmessages

// ToDisplayError defines an error that needs to be sent unaltered to the client.
type ToDisplayError struct {
	error
}

// NewToDisplayError returns a new ErrorToDisplay.
func NewToDisplayError(err error) error {
	return ToDisplayError{err}
}

func (e ToDisplayError) Unwrap() error {
	return e.error
}
