//go:build withmsentraid

package msentraid

import (
	"strings"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
)

// AllExpectedScopes returns all the default expected scopes for a new provider.
func AllExpectedScopes() string {
	return strings.Join(New().expectedScopes, " ")
}

// SetTokenScopesForGraphAPI can be used in tests to set the scopes for the Microsoft Graph API access token.
func (p *Provider) SetTokenScopesForGraphAPI(scopes []string) {
	p.tokenScopesForGraphAPI = scopes
}

func GraphScopesForUnixAttributes(config info.UnixAttributeConfig) []string {
	return graphScopesForUnixAttributes(config)
}

func MissingGraphScope(tokenScopes, requiredScopes []string) string {
	return missingGraphScope(tokenScopes, requiredScopes)
}

var ParseUnixID = parseUnixID

var ProcessSecurityGroupsWithGID = processSecurityGroupsWithGID

var GroupSelectFields = groupSelectFields
