package msentraid_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/msentraid"
	"golang.org/x/oauth2"
)

func TestNew(t *testing.T) {
	p := msentraid.New()

	require.NotEmpty(t, p, "New should return a non-empty provider")
}

func TestCheckTokenScopes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		scopes            string
		noExtraScopeField bool

		wantErr bool
	}{
		"success when checking all scopes are present":       {scopes: msentraid.AllExpectedScopes()},
		"success even if getting more scopes than requested": {scopes: msentraid.AllExpectedScopes() + " extra-scope"},

		"error with missing scopes":       {scopes: "profile email", wantErr: true},
		"error without extra scope field": {noExtraScopeField: true, wantErr: true},
		"error with empty scopes":         {scopes: "", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()

			token := &oauth2.Token{}
			if !tc.noExtraScopeField {
				token = token.WithExtra(map[string]interface{}{"scope": any(tc.scopes)})
			}

			err := p.CheckTokenScopes(token)
			if tc.wantErr {
				require.Error(t, err, "CheckTokenScopes should return an error")
				return
			}

			require.NoError(t, err, "CheckTokenScopes should not return an error")
		})
	}
}

func TestVerifyUsername(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		requestedUsername string
		authenticatedUser string

		wantErr bool
	}{
		"Success when usernames are the same":   {requestedUsername: "foo@bar", authenticatedUser: "foo@bar"},
		"Success when usernames differ in case": {requestedUsername: "foo@bar", authenticatedUser: "Foo@bar"},

		"Error when usernames differ": {requestedUsername: "foo@bar", authenticatedUser: "bar@foo", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()

			err := p.VerifyUsername(tc.requestedUsername, tc.authenticatedUser)
			if tc.wantErr {
				require.Error(t, err, "VerifyUsername should return an error")
				return
			}

			require.NoError(t, err, "VerifyUsername should not return an error")
		})
	}
}
