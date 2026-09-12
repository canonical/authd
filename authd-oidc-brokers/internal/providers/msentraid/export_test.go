//go:build withmsentraid

package msentraid

import (
	"context"
	"strings"
	"time"

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
	retryInterval time.Duration,
	retryTimeout ...time.Duration,
) (string, error) {
	timeout := graphTokenAcquisitionRetryTimeout
	if len(retryTimeout) > 0 {
		timeout = retryTimeout[0]
	}
	return acquireGraphAccessTokenWithRetry(ctx, acquire, retryInterval, timeout)
}

// SetGraphAccessTokenAcquirerForTests overrides the Graph token exchange for tests.
func (p *Provider) SetGraphAccessTokenAcquirerForTests(acquirer func(
	context.Context,
	string,
	string,
	*oauth2.Token,
	himmelblau.DeviceRegistrationData,
) (string, error)) {
	p.graphAccessTokenAcquirerForTests = acquirer
}
