// Package noprovider is the generic oidc extension.
package noprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
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

// CurrentAuthenticationModesOffered returns the generic authentication modes supported by the provider.
func (p NoProvider) CurrentAuthenticationModesOffered(
	sessionMode string,
	supportedAuthModes map[string]string,
	tokenExists bool,
	providerReachable bool,
	endpoints map[string]struct{},
	currentAuthStep int,
) ([]string, error) {
	var offeredModes []string
	switch sessionMode {
	case "passwd":
		if !tokenExists {
			return nil, errors.New("user has no cached token")
		}
		offeredModes = []string{"password"}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}

	default: // auth mode
		if _, ok := endpoints["device_auth"]; ok && providerReachable {
			offeredModes = []string{"device_auth"}
		}
		if tokenExists {
			offeredModes = append([]string{"password"}, offeredModes...)
		}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}
	}

	for _, mode := range offeredModes {
		if _, ok := supportedAuthModes[mode]; !ok {
			return nil, fmt.Errorf("auth mode %q required by the provider, but is not supported locally", mode)
		}
	}

	return offeredModes, nil
}

// GetUserInfo is a no-op when no specific provider is in use.
func (p NoProvider) GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken) (info.User, error) {
	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	userGroups, err := p.getGroups(accessToken)
	if err != nil {
		return info.User{}, err
	}

	return info.NewUser(
		userClaims.PreferredUserName,
		userClaims.Home,
		userClaims.Sub,
		userClaims.Shell,
		userClaims.Gecos,
		userGroups,
	), nil
}

// VerifyUsername checks if the requested username matches the authenticated user.
func (p NoProvider) VerifyUsername(requestedUsername, username string) error {
	if requestedUsername != username {
		return fmt.Errorf("requested username %q does not match the authenticated user %q", requestedUsername, username)
	}
	return nil
}

type claims struct {
	PreferredUserName string `json:"preferred_username"`
	Sub               string `json:"sub"`
	Home              string `json:"home"`
	Shell             string `json:"shell"`
	Gecos             string `json:"gecos"`
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
