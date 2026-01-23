// Package providers define provider-specific configurations and functions to be used by the OIDC broker.
package providers

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// Provider defines provider-specific methods to be used by the broker.
type Provider interface {
	AdditionalScopes() []string
	AuthOptions() []oauth2.AuthCodeOption
	GetExtraFields(token *oauth2.Token) map[string]interface{}
	GetMetadata(provider *oidc.Provider) (map[string]interface{}, error)

	GetUserInfo(idToken info.Claimer) (info.User, error)

	GetGroups(
		ctx context.Context,
		clientID string,
		issuerURL string,
		token *oauth2.Token,
		providerMetadata map[string]interface{},
		deviceRegistrationData []byte,
	) ([]info.Group, error)

	IsTokenExpiredError(err *oauth2.RetrieveError) bool
	IsUserDisabledError(err *oauth2.RetrieveError) bool
	IsTokenForDeviceRegistration(token *oauth2.Token) (bool, error)

	MaybeRegisterDevice(
		ctx context.Context,
		token *oauth2.Token,
		username string,
		issuerURL string,
		deviceRegistrationData []byte,
	) ([]byte, func(), error)

	NormalizeUsername(username string) string
	SupportedOIDCAuthModes() []string
	VerifyUsername(requestedUsername, authenticatedUsername string) error
	SupportsDeviceRegistration() bool
}
