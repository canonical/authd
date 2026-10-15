//go:build withmsentraid

package msentraid

import (
	"context"
	"strings"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"golang.org/x/oauth2"
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

// SetAcquireAccessTokenForGraphAPI replaces the function that acquires the Microsoft Graph API token.
// It returns a function that restores the previous one.
func SetAcquireAccessTokenForGraphAPI(fn func(ctx context.Context, clientID string, tenantID string, token *oauth2.Token, data himmelblau.DeviceRegistrationData, scopes []string) (string, error)) (restore func()) {
	previous := acquireAccessTokenForGraphAPI
	acquireAccessTokenForGraphAPI = fn
	return func() { acquireAccessTokenForGraphAPI = previous }
}

func MissingGraphScope(tokenScopes, requiredScopes []string) string {
	return missingGraphScope(tokenScopes, requiredScopes)
}

var ParseUnixID = parseUnixID

var ProcessSecurityGroupsWithGID = processSecurityGroupsWithGID

var GroupSelectFields = groupSelectFields
