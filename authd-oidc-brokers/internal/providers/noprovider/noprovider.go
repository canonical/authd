// Package noprovider is the generic oidc extension.
package noprovider

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// NoProvider is a generic OIDC provider.
type NoProvider struct{}

// New returns a new NoProvider.
func New() NoProvider {
	return NoProvider{}
}

// CheckTokenScopes should check the token scopes, but we're not sure
// if there is a generic way to do this, so for now it's a no-op.
func (p NoProvider) CheckTokenScopes(token *oauth2.Token) error {
	return nil
}

// AdditionalScopes returns the generic scopes required by the provider.
func (p NoProvider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess}
}

// AuthOptions is a no-op when no specific provider is in use.
func (p NoProvider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetExtraFields returns the extra fields of the token which should be stored persistently.
func (p NoProvider) GetExtraFields(token *oauth2.Token) map[string]interface{} {
	return nil
}

// GetMetadata is a no-op when no specific provider is in use.
func (p NoProvider) GetMetadata(provider *oidc.Provider) (map[string]interface{}, error) {
	return nil, nil
}

// GetUserInfo is a no-op when no specific provider is in use.
func (p NoProvider) GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken, providerMetadata map[string]interface{}) (info.User, error) {
	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	userGroups, err := p.getGroups(accessToken)
	if err != nil {
		return info.User{}, err
	}

	return info.NewUser(
		userClaims.Email,
		userClaims.Home,
		userClaims.Sub,
		userClaims.Shell,
		userClaims.Gecos,
		userGroups,
	), nil
}

// NormalizeUsername parses a username into a normalized version.
func (p NoProvider) NormalizeUsername(username string) string {
	return username
}

// VerifyUsername checks if the requested username matches the authenticated user.
func (p NoProvider) VerifyUsername(requestedUsername, username string) error {
	if p.NormalizeUsername(requestedUsername) != p.NormalizeUsername(username) {
		return fmt.Errorf("requested username %q does not match the authenticated user %q", requestedUsername, username)
	}
	return nil
}

// SupportedOIDCAuthModes returns the OIDC authentication modes supported by the provider.
func (p NoProvider) SupportedOIDCAuthModes() []string {
	return []string{authmodes.Device, authmodes.DeviceQr}
}

type claims struct {
	Email string `json:"email"`
	Sub   string `json:"sub"`
	Home  string `json:"home"`
	Shell string `json:"shell"`
	Gecos string `json:"gecos"`
}

// userClaims returns the user claims parsed from the ID token.
func (p NoProvider) userClaims(idToken *oidc.IDToken) (claims, error) {
	var userClaims claims
	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}
	return userClaims, nil
}

// getGroups is a no-op when no specific provider is in use.
func (p NoProvider) getGroups(_ *oauth2.Token) ([]info.Group, error) {
	return nil, nil
}

// IsTokenExpiredError returns true if the reason for the error is that the refresh token is expired.
func (p NoProvider) IsTokenExpiredError(err oauth2.RetrieveError) bool {
	// There is no generic error for this, so we return false.
	return false
}
