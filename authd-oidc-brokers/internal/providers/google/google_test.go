package google_test

import (
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/google"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestNew(t *testing.T) {
	t.Parallel()

	p := google.New()

	require.Empty(t, p, "New should return the default provider implementation with no parameters")
}

func TestAdditionalScopes(t *testing.T) {
	t.Parallel()

	p := google.New()

	require.Empty(t, p.AdditionalScopes(), "Google provider should not require additional scopes")
}

func TestIsTokenExpiredError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCode        string
		errorDescription string

		wantExpired bool
	}{
		"Token_expired_or_revoked":   {errorCode: "invalid_grant", errorDescription: "Token has been expired or revoked.", wantExpired: true},
		"Token_expired_or_revoked_2": {errorCode: "invalid_grant", errorDescription: "Token has been expired or revoked", wantExpired: true},

		"Non_invalid_grant_error":           {errorCode: "access_denied", errorDescription: "Token has been expired or revoked.", wantExpired: false},
		"Unknown_invalid_grant_description": {errorCode: "invalid_grant", errorDescription: "The user has not consented to the application.", wantExpired: false},
		"Empty_description":                 {errorCode: "invalid_grant", errorDescription: "", wantExpired: false},
		"Admin_policy_enforced":             {errorCode: "invalid_grant", errorDescription: "admin_policy_enforced", wantExpired: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := google.New()
			err := &oauth2.RetrieveError{
				ErrorCode:        tc.errorCode,
				ErrorDescription: tc.errorDescription,
			}
			got := p.IsTokenExpiredError(err)
			require.Equal(t, tc.wantExpired, got, "IsTokenExpiredError returned unexpected result")
		})
	}
}
