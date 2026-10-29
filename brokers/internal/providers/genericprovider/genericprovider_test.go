package genericprovider_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	providerErrors "github.com/canonical/authd/brokers/internal/providers/errors"
	"github.com/canonical/authd/brokers/internal/providers/genericprovider"
	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestGetUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		claims      map[string]interface{}
		wantUser    info.User
		wantErr     bool
		wantErrType error
	}{
		"Successfully_get_user_info_with_all_fields": {
			claims: map[string]interface{}{
				"sub":            "sub123",
				"email":          "user@example.com",
				"email_verified": true,
				"home":           "/home/user",
				"shell":          "/bin/bash",
				"name":           "Test User",
			},
			wantUser: info.NewUser("user@example.com", "/home/user", "sub123", "/bin/bash", "Test User", nil),
		},
		"Successfully_get_user_info_with_minimal_fields": {
			claims: map[string]interface{}{
				"sub":            "sub123",
				"email":          "user@example.com",
				"email_verified": true,
			},
			wantUser: info.NewUser("user@example.com", "", "sub123", "", "", nil),
		},

		"Error_when_sub_is_missing": {
			claims: map[string]interface{}{
				"email":          "user@example.com",
				"email_verified": false,
			},
			wantErr: true,
		},
		"Error_when_email_is_missing": {
			claims: map[string]interface{}{
				"sub": "sub123",
			},
			wantErr: true,
		},
		"Error_when_email_verified_is_missing": {
			claims: map[string]interface{}{
				"sub":   "sub123",
				"email": "user@example.com",
			},
			wantErr:     true,
			wantErrType: &providerErrors.MissingClaimError{Claim: "email_verified"},
		},
		"Error_when_email_is_not_verified": {
			claims: map[string]interface{}{
				"email":          "user@example.com",
				"sub":            "sub123",
				"email_verified": false,
			},
			wantErr:     true,
			wantErrType: &providerErrors.ForDisplayError{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := genericprovider.New()
			mockToken := &mockIDToken{claims: tc.claims}

			user, err := p.GetUserInfo(mockToken, false)
			t.Logf("GetUserInfo error: %v", err)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrType != nil {
					require.ErrorAs(t, err, &tc.wantErrType)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantUser, user)
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

func TestGetGroups(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		groupsClaim   string
		allowedGroups []string
		// claims is the payload of the ID token. It is ignored if rawIDToken is set.
		claims     map[string]interface{}
		rawIDToken string
		noIDToken  bool
		nilToken   bool

		wantGroups []info.Group
		wantErr    bool
	}{
		"Adds_the_allowed_groups_with_the_prefix": {
			allowedGroups: []string{"devops", "docker"},
			claims: map[string]interface{}{
				"groups": []string{"linux-devops", "linux-docker", "linux-root", "offline_access", "devops"},
			},
			wantGroups: []info.Group{{Name: "devops"}, {Name: "docker"}},
		},
		"Compares_the_group_names_in_lower_case": {
			allowedGroups: []string{"DevOps"},
			claims:        map[string]interface{}{"groups": []string{"LINUX-DEVOPS"}},
			wantGroups:    []info.Group{{Name: "devops"}},
		},
		"Removes_duplicate_groups": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": []string{"linux-devops", "Linux-DevOps"}},
			wantGroups:    []info.Group{{Name: "devops"}},
		},
		"Reads_the_configured_claim": {
			groupsClaim:   "roles",
			allowedGroups: []string{"devops", "docker"},
			claims: map[string]interface{}{
				"groups": []string{"linux-docker"},
				"roles":  []string{"linux-devops"},
			},
			wantGroups: []info.Group{{Name: "devops"}},
		},
		"Uses_the_default_claim_when_the_configured_claim_is_blank": {
			groupsClaim:   "  ",
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": []string{"linux-devops"}},
			wantGroups:    []info.Group{{Name: "devops"}},
		},
		"Trims_the_allowed_groups": {
			allowedGroups: []string{" devops "},
			claims:        map[string]interface{}{"groups": []string{"linux-devops"}},
			wantGroups:    []info.Group{{Name: "devops"}},
		},
		"Does_not_trim_the_group_names_in_the_claim": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": []string{" linux-devops", "linux-devops "}},
			wantGroups:    []info.Group{},
		},
		"Reads_a_claim_with_a_single_string": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": "linux-devops"},
			wantGroups:    []info.Group{{Name: "devops"}},
		},
		"Ignores_a_group_with_only_the_prefix": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": []string{"linux-"}},
			wantGroups:    []info.Group{},
		},
		"Ignores_blank_allowed_groups": {
			allowedGroups: []string{" ", ""},
			claims:        map[string]interface{}{"groups": []string{"linux-devops"}},
			wantGroups:    nil,
		},
		"Returns_no_groups_when_no_groups_are_allowed": {
			claims:     map[string]interface{}{"groups": []string{"linux-devops"}},
			wantGroups: nil,
		},
		"Returns_no_groups_when_no_groups_are_allowed_even_without_ID_token": {
			noIDToken:  true,
			wantGroups: nil,
		},
		"Returns_no_groups_when_the_claim_is_missing": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"sub": "sub123"},
			wantGroups:    []info.Group{},
		},
		"Returns_no_groups_when_the_claim_has_a_value_that_is_not_a_string": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": []interface{}{"linux-devops", 42}},
			wantGroups:    []info.Group{},
		},
		"Returns_no_groups_when_the_claim_is_not_a_list": {
			allowedGroups: []string{"devops"},
			claims:        map[string]interface{}{"groups": 42},
			wantGroups:    []info.Group{},
		},

		"Error_when_there_is_no_token": {
			allowedGroups: []string{"devops"},
			nilToken:      true,
			wantErr:       true,
		},
		"Error_when_the_token_has_no_ID_token": {
			allowedGroups: []string{"devops"},
			noIDToken:     true,
			wantErr:       true,
		},
		"Error_when_the_ID_token_does_not_have_three_parts": {
			allowedGroups: []string{"devops"},
			rawIDToken:    "header.payload",
			wantErr:       true,
		},
		"Error_when_the_ID_token_payload_is_not_base64": {
			allowedGroups: []string{"devops"},
			rawIDToken:    "header.not*base64.signature",
			wantErr:       true,
		},
		// A base64url part of one character is not valid, but it matches the
		// characters of the compact serialization.
		"Error_when_the_ID_token_payload_cannot_be_decoded": {
			allowedGroups: []string{"devops"},
			rawIDToken:    "header.a.signature",
			wantErr:       true,
		},
		"Error_when_the_ID_token_payload_is_not_JSON": {
			allowedGroups: []string{"devops"},
			rawIDToken:    "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".signature",
			wantErr:       true,
		},
		// The payload has 28 bytes, so its base64url encoding ends with "==".
		"Error_when_the_ID_token_payload_has_padding": {
			allowedGroups: []string{"devops"},
			rawIDToken:    "header." + base64.URLEncoding.EncodeToString([]byte(`{"groups": ["linux-devops"]}`)) + ".signature",
			wantErr:       true,
		},
		// The verifier of the broker accepts this serialization. The extra
		// member splits into three parts with an unsigned middle part, which
		// must not be read as the payload.
		"Error_when_the_ID_token_is_in_JWS_JSON_serialization": {
			allowedGroups: []string{"devops"},
			rawIDToken: `{"payload":"` + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"sub123"}`)) +
				`","protected":"header","signature":"signature","x":"a.` +
				base64.RawURLEncoding.EncodeToString([]byte(`{"groups":["linux-devops"]}`)) + `.b"}`,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := genericprovider.New()
			if tc.allowedGroups != nil {
				p.SetGroupsClaimConfig(tc.groupsClaim, tc.allowedGroups)
			}

			var token *oauth2.Token
			if !tc.nilToken {
				token = &oauth2.Token{AccessToken: "accesstoken"}
				if !tc.noIDToken {
					rawIDToken := tc.rawIDToken
					if rawIDToken == "" {
						rawIDToken = unsignedIDToken(t, tc.claims)
					}
					token = token.WithExtra(map[string]interface{}{"id_token": rawIDToken})
				}
			}

			groups, err := p.GetGroups(context.Background(), "client-id", "https://issuer.example.com", token, nil, nil, false)
			if tc.wantErr {
				require.Error(t, err)
				var forDisplayErr *providerErrors.ForDisplayError
				require.ErrorAs(t, err, &forDisplayErr, "GetGroups should return an error that can be shown to the user")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantGroups, groups)
		})
	}
}

// unsignedIDToken returns a raw ID token with the given claims as payload. The
// header and the signature are not valid, because GetGroups does not verify
// the token.
func unsignedIDToken(t *testing.T, claims map[string]interface{}) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	require.NoError(t, err, "Setup: marshalling the claims should not have failed")

	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestIsTokenExpiredError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCode        string
		errorDescription string

		wantExpired bool
	}{
		"Keycloak_session_not_active":         {errorCode: "invalid_grant", errorDescription: "Session not active", wantExpired: true},
		"Keycloak_offline_session_not_active": {errorCode: "invalid_grant", errorDescription: "Offline session not active", wantExpired: true},
		"Keycloak_token_not_active":           {errorCode: "invalid_grant", errorDescription: "Token is not active", wantExpired: true},
		"Keycloak_stale_token":                {errorCode: "invalid_grant", errorDescription: "Stale token", wantExpired: true},

		"Non_invalid_grant_error":           {errorCode: "access_denied", errorDescription: "Session not active", wantExpired: false},
		"Keycloak_user_disabled":            {errorCode: "invalid_grant", errorDescription: "User disabled", wantExpired: false},
		"Unknown_invalid_grant_description": {errorCode: "invalid_grant", errorDescription: "The user has not consented to the application.", wantExpired: false},
		"Empty_description":                 {errorCode: "invalid_grant", errorDescription: "", wantExpired: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := genericprovider.New()
			err := &oauth2.RetrieveError{
				ErrorCode:        tc.errorCode,
				ErrorDescription: tc.errorDescription,
			}
			got := p.IsTokenExpiredError(err)
			require.Equal(t, tc.wantExpired, got, "IsTokenExpiredError returned unexpected result")
		})
	}
}
