// Package sessionmode defines the session modes supported by the broker.
package sessionmode

const (
	// Login is used when the session is for user login.
	Login = "login"
	// LoginOld is the old name for the login session, which is now deprecated but still used by authd until all broker
	// installations are updated.
	LoginOld = "auth"
	// ChangePassword is used when the session is for changing the user password.
	ChangePassword = "change-password"
	// ChangePasswordOld is the old name for the change-password session, which is now deprecated but still used by authd
	// until all broker installations are updated.
	ChangePasswordOld = "passwd"
)
