package broker

// Broker responses.
const (
	// AuthGranted is the response when the authentication is granted.
	AuthGranted = "granted"
	// AuthDenied is the response when the authentication is denied.
	AuthDenied = "denied"
	// AuthCancelled is the response when the authentication is cancelled.
	AuthCancelled = "cancelled"
	// AuthRetry is the response when the authentication needs to be retried (another chance).
	AuthRetry = "retry"
	// AuthNext is the response when another MFA (including changing password) authentication is necessary.
	AuthNext = "next"
)

// AuthReplies is the list of all possible authentication replies.
var AuthReplies = []string{AuthGranted, AuthDenied, AuthCancelled, AuthRetry, AuthNext}

const (
	// AuthDataSecret is the key for the secret in the authentication data.
	AuthDataSecret = "secret"
	// AuthDataSecretOld is the old key for the secret in the authentication data, which is now deprecated
	// TODO(UDENG-5844): Remove this once all authd installations use "secret" instead of "challenge".
	AuthDataSecretOld = "challenge"
)
