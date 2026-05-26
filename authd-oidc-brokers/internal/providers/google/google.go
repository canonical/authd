// Package google is the google specific extension.
package google

import (
	"slices"
	"strings"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/genericprovider"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// Provider is the google provider implementation.
type Provider struct {
	genericprovider.GenericProvider
}

// New returns a new GoogleProvider.
func New() Provider {
	return Provider{
		GenericProvider: genericprovider.New(),
	}
}

// DisplayName returns the display name of the provider.
func (Provider) DisplayName() string {
	return "Google IAM"
}

// AdditionalScopes returns the generic scopes required by the provider.
// Note that we do not return oidc.ScopeOfflineAccess, as for TV/limited input devices, the API call will fail as not
// supported by this application type. However, the refresh token will be acquired and is functional to refresh without
// user interaction.
// If we start to support other kinds of applications, we should revisit this.
// More info on https://developers.google.com/identity/protocols/oauth2/limited-input-device#allowedscopes.
func (Provider) AdditionalScopes() []string {
	return []string{}
}

// GetUserInfo returns the user info from the provided Claimer.
func (p Provider) GetUserInfo(claimer info.Claimer, isRefresh bool) (info.User, error) {
	userInfo, err := p.GenericProvider.GetUserInfo(claimer, isRefresh)
	if err != nil {
		return info.User{}, err
	}

	nameClaimPresent, err := hasNonEmptyStringClaim(claimer, "name")
	if err != nil {
		return info.User{}, err
	}
	if !isRefresh && !nameClaimPresent {
		return info.User{}, providerErrors.NewMissingClaimError("name")
	}

	return userInfo, nil
}

func hasNonEmptyStringClaim(claimer info.Claimer, claim string) (bool, error) {
	var claims map[string]interface{}
	if err := claimer.Claims(&claims); err != nil {
		return false, err
	}

	value, ok := claims[claim].(string)
	return ok && value != "", nil
}

// IsTokenExpiredError returns true if the reason for the error is that the refresh token is expired.
func (Provider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	if err.ErrorCode != "invalid_grant" {
		return false
	}

	expiredDescriptions := []string{
		"Token has been expired or revoked",
	}

	return slices.ContainsFunc(expiredDescriptions, func(desc string) bool {
		return strings.Contains(err.ErrorDescription, desc)
	})
}
