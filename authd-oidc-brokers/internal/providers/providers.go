// Package providers define provider-specific configurations and functions to be used by the OIDC broker.
package providers

import (
	"context"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider defines the core provider-specific methods required by the broker.
// Every provider implementation must satisfy this interface.
type Provider interface {
	AdditionalScopes() []string
	AuthOptions() []oauth2.AuthCodeOption
	DisplayName() string
	GetUserInfo(claimer info.Claimer, isRefresh bool) (info.User, error)
	IsTokenExpiredError(err *oauth2.RetrieveError) bool
	NormalizeUsername(username string) string
	// SupportedOnlineAuthModes returns the authentication modes that require a
	// working connection to the identity provider (in contrast to the local
	// password mode, which the broker prepends). These are not necessarily OIDC
	// flows: entra_password issues OAuth 2.0 tokens without an OIDC id_token.
	SupportedOnlineAuthModes() []string
	VerifyUsername(requestedUsername, authenticatedUsername string) error
}

// GroupFetcher is implemented by providers that can fetch group memberships from the identity provider.
type GroupFetcher interface {
	GetGroups(
		ctx context.Context,
		clientID string,
		issuerURL string,
		token *oauth2.Token,
		providerMetadata map[string]interface{},
		deviceRegistrationData []byte,
		needsAccessTokenForGraphAPI bool,
	) ([]info.Group, error)
}

// MetadataProvider is implemented by providers that supply extra metadata or token fields.
type MetadataProvider interface {
	GetMetadata(provider *oidc.Provider) (map[string]interface{}, error)
	GetExtraFields(token *oauth2.Token) map[string]interface{}
}

// DeviceRegisterer is implemented by providers that support device registration.
type DeviceRegisterer interface {
	IsTokenForDeviceRegistration(token *oauth2.Token) (bool, error)
	MaybeRegisterDevice(
		ctx context.Context,
		token *oauth2.Token,
		username string,
		issuerURL string,
		deviceRegistrationData []byte,
	) ([]byte, func(), error)
}

// UserDisabledChecker is implemented by providers that can detect disabled users
// from token refresh errors.
type UserDisabledChecker interface {
	IsUserDisabledError(err *oauth2.RetrieveError) bool
}

// GraphClientSecretSetter is implemented by providers that can use the OIDC
// app's client secret for app-only (client credentials) group lookup as a
// fallback when the delegated token cannot be used against the Graph API.
type GraphClientSecretSetter interface {
	SetGraphClientSecret(secret string)
}

// Capability is an optional interface that allows a Provider to expose optional
// interfaces dynamically, similar to errors.As. Composed or wrapped providers
// should implement this to avoid combinatorial type-switch boilerplate.
type Capability interface {
	// ProviderAs sets *target to the capability of type T if available, and
	// returns true. If the capability is not available, it returns false
	// without modifying *target.
	ProviderAs(target any) bool
}

// ProviderAs checks whether p exposes optional capability T. It first checks
// if p implements Capability (for composed/wrapped providers), then falls back
// to a direct interface assertion.
func ProviderAs[T any](p Provider) (T, bool) {
	if c, ok := p.(Capability); ok {
		var t T
		if c.ProviderAs(&t) {
			return t, true
		}
		var zero T
		return zero, false
	}
	t, ok := p.(T)
	return t, ok
}
