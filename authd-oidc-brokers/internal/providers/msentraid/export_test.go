//go:build withmsentraid

package msentraid

import (
	"context"
	"strings"
	"time"
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
	retryTimeout time.Duration,
) (string, error) {
	return acquireGraphAccessTokenWithRetry(ctx, acquire, retryInterval, retryTimeout)
}

// SetGraphTokenAcquisitionRetryForTests shortens the AADSTS50155 retry interval
// and window so tests do not wait for the production timings.
func (p *Provider) SetGraphTokenAcquisitionRetryForTests(retryInterval, retryTimeout time.Duration) {
	p.graphTokenAcquisitionRetryInterval = retryInterval
	p.graphTokenAcquisitionRetryTimeout = retryTimeout
}

// SetGraphAccessTokenAcquirerForTests overrides the Graph token exchange for tests.
func (p *Provider) SetGraphAccessTokenAcquirerForTests(acquirer graphAccessTokenAcquirer) {
	p.graphAccessTokenAcquirerForTests = acquirer
}
