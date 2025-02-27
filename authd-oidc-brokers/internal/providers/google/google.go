// Package google is the google specific extension.
package google

import (
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/genericprovider"
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

// AdditionalScopes returns the generic scopes required by the provider.
// Note that we do not return oidc.ScopeOfflineAccess, as for TV/limited input devices, the API call will fail as not
// supported by this application type. However, the refresh token will be acquired and is functional to refresh without
// user interaction.
// If we start to support other kinds of applications, we should revisit this.
// More info on https://developers.google.com/identity/protocols/oauth2/limited-input-device#allowedscopes.
func (Provider) AdditionalScopes() []string {
	return []string{}
}
