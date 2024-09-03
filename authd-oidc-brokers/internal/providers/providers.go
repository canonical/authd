// Package providers define provider-specific configurations and functions to be used by the OIDC broker.
package providers

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// ProviderInfoer defines provider-specific methods to be used by the broker.
type ProviderInfoer interface {
	AdditionalScopes() []string
	AuthOptions() []oauth2.AuthCodeOption
	CheckTokenScopes(token *oauth2.Token) error
	CurrentAuthenticationModesOffered(
		sessionMode string,
		supportedAuthModes map[string]string,
		tokenExists bool,
		providerReachable bool,
		endpoints map[string]struct{},
		currentAuthStep int,
	) ([]string, error)
	GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken) (info.User, error)
}
