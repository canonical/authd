//go:build withmsentraid

package msentraid

import "strings"

// AllExpectedScopes returns all the default expected scopes for a new provider.
func AllExpectedScopes() string {
	return strings.Join(New().expectedScopes, " ")
}

func (p *Provider) SetNeedsAccessTokenForGraphAPI(value bool) {
	p.needsAccessTokenForGraphAPI = value
}

// SetTokenScopesForGraphAPI can be used in tests to set the scopes for the Microsoft Graph API access token.
func (p *Provider) SetTokenScopesForGraphAPI(scopes []string) {
	p.tokenScopesForGraphAPI = scopes
}
