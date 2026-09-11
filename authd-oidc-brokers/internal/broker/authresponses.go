package broker

import "github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"

// cachedPasswordMessage is the user-facing notice attached to a granted
// response after the user's Entra password is saved as the local password
// (during the Entra password + MFA flow). It is broker-owned so it can be
// localized independently of authd.
const cachedPasswordMessage = "Your local password has been set to your Entra password"

const (
	//nolint:gosec // G101: these are user-facing messages, not credentials.
	entraPasswordMatchesMessage = "Your Entra password already matches your local password. No change was needed."
	entraPasswordUpdatedMessage = "Your local password has been updated to your Entra password."
	entraPasswordKeptMessage    = "Your existing local password was kept."
)

type isAuthenticatedDataResponse interface {
	isAuthenticatedDataResponse()
}

// userInfoMessage represents the user information message that is returned to authd.
type userInfoMessage struct {
	UserInfo info.User `json:"userinfo"`
	Message  string    `json:"message,omitempty"`
}

func (userInfoMessage) isAuthenticatedDataResponse() {}

// errorMessage represents the error message that is returned to authd.
type errorMessage struct {
	Message string `json:"message"`
}

func (errorMessage) isAuthenticatedDataResponse() {}

// authNextMessage carries internal state to the PAM adapter without displaying
// an extra prompt to the user.
type authNextMessage struct {
	Message                 string `json:"message"`
	EntraPasswordUnverified bool   `json:"entra_password_unverified,omitempty"`
}

func (authNextMessage) isAuthenticatedDataResponse() {}
