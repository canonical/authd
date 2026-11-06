//go:build withmsentraid

package msentraid

import (
	"context"
	"strings"
	"time"

	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/brokers/internal/providers/msentraid/himmelblau"
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
