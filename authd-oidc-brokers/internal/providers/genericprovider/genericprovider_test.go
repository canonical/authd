package genericprovider_test

import (
	"encoding/json"
	"fmt"
	"testing"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/genericprovider"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/stretchr/testify/require"
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
				"gecos":          "Test User",
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
			wantErrType: &providerErrors.ForDisplayError{},
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

			user, err := p.GetUserInfo(mockToken)
			t.Logf("GetUserInfo error: %v", err)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			if tc.wantErrType != nil {
				require.ErrorIs(t, err, tc.wantErrType)
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
