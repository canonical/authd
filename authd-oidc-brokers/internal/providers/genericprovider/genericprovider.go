// Package genericprovider is the generic oidc extension.
package genericprovider

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// GenericProvider is a generic OIDC provider.
type GenericProvider struct{}

// New returns a new GenericProvider.
func New() GenericProvider {
	return GenericProvider{}
}

// DisplayName returns the display name of the provider.
func (p GenericProvider) DisplayName() string {
	return "the identity provider"
}

// AdditionalScopes returns the generic scopes required by the provider.
func (p GenericProvider) AdditionalScopes() []string {
	return []string{}
}

// AuthOptions is a no-op when no specific provider is in use.
func (p GenericProvider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetUserInfo returns user information from the claims of the provided Claimer.
func (p GenericProvider) GetUserInfo(claimer info.Claimer, _ bool) (info.User, error) {
	var claimsMap map[string]interface{}
	if err := claimer.Claims(&claimsMap); err != nil {
		return info.User{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}

	// Check required claims
	sub, ok := claimsMap["sub"].(string)
	if !ok || sub == "" {
		return info.User{}, providerErrors.NewMissingClaimError("sub")
	}

	email, ok := claimsMap["email"].(string)
	if !ok || email == "" {
		return info.User{}, providerErrors.NewMissingClaimError("email")
	}

	rawEmailVerified, present := claimsMap["email_verified"]
	if !present {
		return info.User{}, &providerErrors.ForDisplayError{
			Message: "Authentication failure: email not verified",
			Err:     providerErrors.NewMissingClaimError("email_verified"),
		}
	}
	if verified, ok := rawEmailVerified.(bool); !ok || !verified {
		return info.User{}, &providerErrors.ForDisplayError{
			Message: "Authentication failure: email not verified",
			Err:     errors.New("email_verified claim value is false or malformed"),
		}
	}

	// Optional claims: home, shell, name
	home, _ := claimsMap["home"].(string)
	shell, _ := claimsMap["shell"].(string)
	gecos, _ := claimsMap["name"].(string)

	return info.NewUser(
		email,
		home,
		sub,
		shell,
		gecos,
		nil,
	), nil
}

// NormalizeUsername parses a username into a normalized version.
func (p GenericProvider) NormalizeUsername(username string) string {
	return username
}

// VerifyUsername checks if the requested username matches the authenticated user.
func (p GenericProvider) VerifyUsername(requestedUsername, username string) error {
	if p.NormalizeUsername(requestedUsername) != p.NormalizeUsername(username) {
		msg := fmt.Sprintf("Authentication failure: requested username %q does not match the authenticated user %q", requestedUsername, username)
		return &providerErrors.ForDisplayError{Message: msg}
	}
	return nil
}

// SupportedOnlineAuthModes returns the authentication modes supported by the
// provider that require a connection to the identity provider.
func (p GenericProvider) SupportedOnlineAuthModes() []string {
	return []string{authmodes.Device, authmodes.DeviceQr}
}

// IsTokenExpiredError returns true if the reason for the error is that the refresh token is expired.
func (p GenericProvider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	if err.ErrorCode != "invalid_grant" {
		return false
	}

	expiredDescriptions := []string{
		"Session not active",         // Keycloak: online user session expired
		"Offline session not active", // Keycloak: offline session expired or revoked
		"Token is not active",        // Keycloak: refresh token JWT expired (exp/nbf check)
		"Stale token",                // Keycloak: token issued before not-before policy or reuse detected
	}

	return slices.ContainsFunc(expiredDescriptions, func(desc string) bool {
		return strings.Contains(err.ErrorDescription, desc)
	})
}
