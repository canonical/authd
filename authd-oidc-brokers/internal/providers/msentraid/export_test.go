package msentraid

import "strings"

// AllExpectedScopes returns all the default expected scopes for a new provider.
func AllExpectedScopes() string {
	return strings.Join(New().expectedScopes, " ")
}

// SkipAccessTokenForGraphAPI can be used in tests to skip acquiring an access token for the Microsoft Graph API in
// GetUserInfo via libhimmelblau.
func (p *Provider) SkipAccessTokenForGraphAPI() {
	p.skipAccessTokenForGraphAPI = true
}

// SetTokenScopesForGraphAPI can be used in tests to set the scopes for the Microsoft Graph API access token.
func (p *Provider) SetTokenScopesForGraphAPI(scopes []string) {
	p.tokenScopesForGraphAPI = scopes
}

// SetAuthorityBaseURL sets the base URL for the token authority, used in tests to override the default.
func SetAuthorityBaseURL(url string) {
	authorityBaseURL = url
}
