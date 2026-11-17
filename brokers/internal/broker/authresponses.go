package broker

import (
	"fmt"

	"github.com/canonical/authd/brokers/internal/providers/info"
)

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

// Keep in sync with internal/brokers/auth.DenialCodePermissionDenied: the
// brokers module cannot import that package.
const denialCodePermissionDenied = "permission-denied"

// errorMessage represents the error message that is returned to authd.
type errorMessage struct {
	Message string `json:"message"`
	// Code classifies terminal denials without tying PAM clients to message text.
	Code string `json:"code,omitempty"`
}

func (errorMessage) isAuthenticatedDataResponse() {}

func permissionDeniedMessage(msg string) errorMessage {
	return errorMessage{Message: msg, Code: denialCodePermissionDenied}
}

func permissionDeniedMessagef(format string, args ...any) errorMessage {
	return permissionDeniedMessage(fmt.Sprintf(format, args...))
}
