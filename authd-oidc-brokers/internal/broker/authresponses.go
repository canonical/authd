package broker

import "github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"

// cachedPasswordMessage is the user-facing notice attached to a granted
// response after the user's Entra password is saved as the local password
// (during the Entra password + MFA flow). It is broker-owned so it can be
// localized independently of authd.
const cachedPasswordMessage = "Your local password has been set to your Entra password"

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
