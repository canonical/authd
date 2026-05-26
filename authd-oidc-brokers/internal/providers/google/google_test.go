package google_test

import (
	"encoding/json"
	"fmt"
	"testing"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
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

func TestGetUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		claims    map[string]interface{}
		isRefresh bool
		wantErr   bool
	}{
		"Error_when_name_claim_is_missing_on_initial_auth": {
			claims: map[string]interface{}{
				"sub":            "sub123",
				"email":          "user@example.com",
				"email_verified": true,
			},
			wantErr: true,
		},
		"Success_when_name_claim_is_missing_on_refresh": {
			claims: map[string]interface{}{
				"sub":            "sub123",
				"email":          "user@example.com",
				"email_verified": true,
			},
			isRefresh: true,
		},
		"Success_when_name_claim_is_present_on_initial_auth": {
			claims: map[string]interface{}{
				"sub":            "sub123",
				"email":          "user@example.com",
				"email_verified": true,
				"name":           "Test User",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := google.New()
			_, err := p.GetUserInfo(&mockIDToken{claims: tc.claims}, tc.isRefresh)
			if tc.wantErr {
				var missingClaimErr *providerErrors.MissingClaimError
				require.ErrorAs(t, err, &missingClaimErr)
				require.Equal(t, "name", missingClaimErr.Claim)
				return
			}
			require.NoError(t, err)
		})
	}
}

type mockIDToken struct {
	claims map[string]interface{}
}

func (m *mockIDToken) Claims(v interface{}) error {
	data, err := json.Marshal(m.claims)
	if err != nil {
		return fmt.Errorf("failed to marshal claims: %v", err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal claims: %v", err)
	}

	return nil
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
