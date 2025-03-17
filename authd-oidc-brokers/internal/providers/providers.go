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
	CheckTokenScopes(token *oauth2.Token) error
	GetExtraFields(token *oauth2.Token) map[string]interface{}
	GetMetadata(provider *oidc.Provider) (map[string]interface{}, error)
	GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken info.Claimer, providerMetadata map[string]interface{}) (info.User, error)
	NormalizeUsername(username string) string
	SupportedOIDCAuthModes() []string
	VerifyUsername(requestedUsername, authenticatedUsername string) error
	IsTokenExpiredError(err oauth2.RetrieveError) bool
}
