// Package sessionmode defines the session modes supported by the broker.
package sessionmode

const (
	// Login is used when the session is for user login.
	Login = "auth"
	// ChangePassword is used when the session is for changing the user password.
	ChangePassword = "passwd"
)
