//go:build withmsentraid

package msentraid

import (
	"context"
	"strings"

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

// ClassifyGraphTokenAcquisitionError exposes classifyGraphTokenAcquisitionError for tests.
func ClassifyGraphTokenAcquisitionError(err error) error {
	return classifyGraphTokenAcquisitionError(err)
}

// AcquireGraphAccessTokenWithRetry exposes acquireGraphAccessTokenWithRetry for tests.
func AcquireGraphAccessTokenWithRetry(
	ctx context.Context,
	acquire func() (string, error),
) (string, error) {
	return acquireGraphAccessTokenWithRetry(ctx, acquire)
}

// SetGraphAccessTokenAcquirerForTests overrides the Graph token exchange for tests.
func (p *Provider) SetGraphAccessTokenAcquirerForTests(acquirer func(
	context.Context,
	string,
	string,
	*oauth2.Token,
	himmelblau.DeviceRegistrationData,
) (string, error)) {
	p.graphAccessTokenAcquirer = acquirer
}
