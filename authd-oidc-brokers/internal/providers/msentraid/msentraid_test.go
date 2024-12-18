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
		"Success_when_checking_all_scopes_are_present":       {scopes: msentraid.AllExpectedScopes()},
		"Success_even_if_getting_more_scopes_than_requested": {scopes: msentraid.AllExpectedScopes() + " extra-scope"},

		"Error_with_missing_scopes":       {scopes: "profile email", wantErr: true},
		"Error_without_extra_scope_field": {noExtraScopeField: true, wantErr: true},
		"Error_with_empty_scopes":         {scopes: "", wantErr: true},
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

func TestNormalizeUsername(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		username string

		wantNormalized string
	}{
		"Shouldnt_change_all_lower_case": {
			username:       "name@email.com",
			wantNormalized: "name@email.com",
		},
		"Should_convert_all_to_lower_case": {
			username:       "NAME@email.com",
			wantNormalized: "name@email.com",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			ret := p.NormalizeUsername(tc.username)
			require.Equal(t, tc.wantNormalized, ret)
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
		"Success_when_usernames_are_the_same":   {requestedUsername: "foo-bar@example", authenticatedUser: "foo-bar@example"},
		"Success_when_usernames_differ_in_case": {requestedUsername: "foo-bar@example", authenticatedUser: "Foo-Bar@example"},

		"Error_when_usernames_differ": {requestedUsername: "foo@example", authenticatedUser: "bar@foo", wantErr: true},
		"Error_when_requested_username_contains_invalid_characters": {
			requestedUsername: "fóó@example", authenticatedUser: "foo@example", wantErr: true,
		},
		"Error_when_authenticated_username_contains_invalid_characters": {
			requestedUsername: "foo@example", authenticatedUser: "fóó@example", wantErr: true,
		},
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
