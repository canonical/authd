// Package auth contains the authentication related code.
package auth

const (
	// Granted is the response when the authentication is granted.
	Granted = "granted"
	// Denied is the response when the authentication is denied.
	Denied = "denied"
	// DeniedMaxTries is the response when the maximum number of tries has been exceeded.
	DeniedMaxTries = "denied-max-tries"
	// Cancelled is the response when the authentication is cancelled.
	Cancelled = "cancelled"
	// Retry is the response when the authentication needs to be retried (another chance).
	Retry = "retry"
	// Next is the response when another MFA (including changing password) authentication is necessary.
	Next = "next"
)

// Replies is the list of all possible authentication replies.
var Replies = []string{Granted, Denied, DeniedMaxTries, Cancelled, Retry, Next}

const (
	// SessionModeLogin is used when the session is for user login.
	// TODO: We can change this to "login" once all broker installations are updated to use the new name.
	SessionModeLogin = "auth"
	// SessionModeChangePassword is used when the session is for changing the user password.
	// TODO: We can change this to "change-password" once all broker installations are updated to use the new name.
	SessionModeChangePassword = "passwd"
)
