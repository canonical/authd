//go:build withmsentraid

package msentraid_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

var discoveryURLMu sync.RWMutex

func TestNew(t *testing.T) {
	p := msentraid.New()

	require.NotEmpty(t, p, "New should return a non-empty provider")
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

func TestGetUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		IDToken *testIDToken

		wantErr bool
	}{
		"Successfully_get_user_info": {},

		"Error_when_id_token_is_missing_required_oid_claims": {IDToken: missingOIDClaimIDToken, wantErr: true},
		"Error_when_id_token_claims_are_invalid":             {IDToken: invalidIDToken, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idToken := validIDToken
			if tc.IDToken != nil {
				idToken = tc.IDToken
			}

			p := msentraid.New()

			got, err := p.GetUserInfo(idToken, false)
			if tc.wantErr {
				require.Error(t, err, "GetUserInfo should return an error")
				return
			}
			require.NoError(t, err, "GetUserInfo should not return an error")

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestUserInfoFromAccessToken(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid":  "saved-user-id",
		"upn":  "test-user@email.com",
		"name": "test-user",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	got, err := msentraid.New().UserInfoFromAccessToken(accessTokenStr)
	require.NoError(t, err, "UserInfoFromAccessToken should not return an error")
	require.Equal(t, info.NewUser("test-user@email.com", "", "saved-user-id", "", "test-user", nil), got)
}

func TestRefreshEntraToken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		refreshHandler http.HandlerFunc
		wantErr        bool
		wantErrSubstr  string
	}{
		"Active_user_refresh_succeeds": {},
		"Disabled_user_returns_AADSTS50057": {
			refreshHandler: disabledRefreshHandler,
			wantErr:        true,
			wantErrSubstr:  "AADSTS50057",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mockServer, cleanup := startMockMSServer(t, &mockMSServerConfig{RefreshHandler: tc.refreshHandler})
			t.Cleanup(cleanup)

			got, err := msentraid.New().RefreshEntraToken(
				context.Background(),
				mockServer.URL+"/tenant-id/v2.0",
				"refreshtoken",
			)
			if tc.wantErr {
				require.Error(t, err, "RefreshEntraToken should fail")
				require.Contains(t, err.Error(), tc.wantErrSubstr, "unexpected error from refresh")
				return
			}
			require.NoError(t, err, "RefreshEntraToken should succeed for an active user")
			require.NotEmpty(t, got.AccessToken, "expected a rotated token on success")
			require.Nil(t, got.Extra("preferred_username"), "refresh should not add redundant preferred_username extras")
			require.Nil(t, got.Extra("sub"), "refresh should not add redundant sub extras")
			require.Nil(t, got.Extra("name"), "refresh should not add redundant name extras")
		})
	}
}

func TestGetGroups(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")
	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	tests := map[string]struct {
		tokenScopes        []string
		providerMetadata   map[string]any
		acquireAccessToken bool

		groupEndpointHandler http.HandlerFunc

		wantErr bool
	}{
		"Successfully_get_groups":                               {},
		"Successfully_get_groups_with_local_groups":             {groupEndpointHandler: localGroupHandler},
		"Successfully_get_groups_with_mixed_groups":             {groupEndpointHandler: mixedGroupHandler},
		"Successfully_get_groups_filtering_non_security_groups": {groupEndpointHandler: nonSecurityGroupHandler},
		"Successfully_get_groups_with_acquired_access_token":    {acquireAccessToken: true},

		"Error_when_msgraph_host_is_invalid":             {providerMetadata: map[string]any{"msgraph_host": "invalid"}, wantErr: true},
		"Error_when_token_does_not_have_required_scopes": {tokenScopes: []string{"not the required scopes"}, wantErr: true},
		"Error_when_getting_user_groups_fails":           {groupEndpointHandler: errorGroupHandler, wantErr: true},
		"Error_when_group_is_missing_id":                 {groupEndpointHandler: missingIDGroupHandler, wantErr: true},
		"Error_when_group_is_missing_display_name":       {groupEndpointHandler: missingDisplayNameGroupHandler, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.tokenScopes == nil {
				tc.tokenScopes = strings.Split(msentraid.AllExpectedScopes(), " ")
			}

			if tc.providerMetadata == nil {
				mockServer, cleanup := startMockMSServer(t, &mockMSServerConfig{
					GroupEndpointHandler: tc.groupEndpointHandler,
				})
				t.Cleanup(cleanup)
				tc.providerMetadata = map[string]any{"msgraph_host": mockServer.URL}
			}

			var deviceRegistrationData []byte
			if tc.acquireAccessToken {
				var cleanup func()
				deviceRegistrationData, cleanup, err = maybeRegisterDevice(t, nil)
				t.Cleanup(cleanup)
				require.NoError(t, err, "maybeRegisterDevice should not return an error")
			}

			p := msentraid.New()
			p.SetTokenScopesForGraphAPI(tc.tokenScopes)

			got, err := p.GetGroups(
				context.Background(),
				"",
				"",
				token,
				tc.providerMetadata,
				deviceRegistrationData,
				tc.acquireAccessToken,
			)
			if tc.wantErr {
				require.Error(t, err, "GetUserInfo should return an error")
				return
			}
			require.NoError(t, err, "GetUserInfo should not return an error")

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestGetGroupsUsesCurrentTokenWhenAlreadyGraphCapable(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All User.Read",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	p := msentraid.New()

	got, err := p.GetGroups(
		context.Background(),
		"",
		"",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		true,
	)
	require.NoError(t, err, "GetGroups should use the current token when it already has Graph scopes")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestGetGroupsUsesClientCredentialsFallback(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": "00000000-0000-0000-0000-000000000000",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	got, err := p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		false,
	)
	require.NoError(t, err, "GetGroups should fall back to client credentials when the delegated token lacks Graph scope")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestGetGroupsDeviceRegistrationTokenDoesNotUseClientCredentials(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": "00000000-0000-0000-0000-000000000000",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	// needsAccessTokenForGraphAPI=true marks a device-registration token, which
	// must be exchanged via the PRT path (strategy 2) rather than the app-only
	// client-credentials path, even when a client secret is configured. With no
	// device registration data the PRT path fails, but it must NOT silently fall
	// through to client credentials (which would otherwise succeed here).
	_, err = p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		true,
	)
	require.Error(t, err, "GetGroups must not use client credentials for a device-registration token")
	require.Contains(t, err.Error(), "device registration",
		"GetGroups should fail on the device-registration token-exchange path, not client credentials")
}

func TestGetGroupsInvalidTokenWithClientCredentialsReturnsError(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	token := &oauth2.Token{AccessToken: "invalid-token"}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	_, err := p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		false,
	)
	require.Error(t, err, "GetGroups should return an error instead of panicking on invalid delegated tokens")
}

// TestClassifyGraphTokenAcquisitionError verifies the #1721 regression
// boundary at the classification level: only a positively-confirmed
// device-authentication failure (AADSTS50155) must be classified as a
// RetryWithDeviceAuthError. Every other error -- including
// ErrMissingClientCredentials (AADSTS7000218) and any other himmelblau
// error (unknown/future AADSTS codes, or any other acquisition failure) --
// must NOT be classified as one, since that would
// make the broker clear the cached device registration data and force a new
// Entra device enrollment even though the registration itself was never
// confirmed invalid.
func TestClassifyGraphTokenAcquisitionError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err error

		wantRetryWithDeviceAuth bool
		wantForDisplay          bool
		wantDeviceDisabled      bool
	}{
		"Device_disabled_denies_login": {
			err:                himmelblau.ErrDeviceDisabled,
			wantDeviceDisabled: true,
		},
		"Invalid_redirect_URI_is_displayed_to_the_user": {
			err:            himmelblau.ErrInvalidRedirectURI,
			wantForDisplay: true,
		},
		"Device_authentication_failed_retries_with_device_auth": {
			err:                     himmelblau.ErrDeviceAuthenticationFailed,
			wantRetryWithDeviceAuth: true,
		},
		"Missing_client_credentials_is_a_plain_error": {
			err: himmelblau.ErrMissingClientCredentials,
		},
		"Unclassified_token_acquisition_error_is_a_plain_error": {
			err: errors.New("unclassified token acquisition error"),
		},
		"Unrelated_error_is_a_plain_error": {
			err: fmt.Errorf("some other unrelated error"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := msentraid.ClassifyGraphTokenAcquisitionError(tc.err)
			require.Error(t, got, "classifyGraphTokenAcquisitionError should always return an error for a non-nil input")

			var retryWithDeviceAuthErr *providerErrors.RetryWithDeviceAuthError
			require.Equal(t, tc.wantRetryWithDeviceAuth, errors.As(got, &retryWithDeviceAuthErr),
				"unexpected RetryWithDeviceAuthError classification")

			var forDisplayErr *providerErrors.ForDisplayError
			require.Equal(t, tc.wantForDisplay, errors.As(got, &forDisplayErr),
				"unexpected ForDisplayError classification")

			require.Equal(t, tc.wantDeviceDisabled, errors.Is(got, providerErrors.ErrDeviceDisabled),
				"unexpected ErrDeviceDisabled classification")
		})
	}
}

func TestAcquireGraphAccessTokenWithRetry(t *testing.T) {
	t.Parallel()

	otherErr := errors.New("token endpoint unavailable")
	tests := map[string]struct {
		errs          []error
		tokens        []string
		wantToken     string
		wantErr       error
		wantCalls     int
		wantMinCalls  int
		retryInterval time.Duration
		retryTimeout  time.Duration
		cancelRequest bool
	}{
		"Transient_device_authentication_failure_retries_until_success": {
			errs:          []error{himmelblau.ErrDeviceAuthenticationFailed, himmelblau.ErrDeviceAuthenticationFailed, nil},
			tokens:        []string{"", "", "access-token"},
			wantToken:     "access-token",
			wantCalls:     3,
			retryInterval: 0,
			retryTimeout:  time.Second,
		},
		"Persistent_device_authentication_failure_is_returned": {
			errs:          []error{himmelblau.ErrDeviceAuthenticationFailed},
			wantErr:       himmelblau.ErrDeviceAuthenticationFailed,
			wantMinCalls:  2,
			retryInterval: 0,
			retryTimeout:  5 * time.Millisecond,
		},
		"Retry_result_with_other_error_is_not_destructive": {
			errs:          []error{himmelblau.ErrDeviceAuthenticationFailed, otherErr},
			wantErr:       otherErr,
			wantCalls:     2,
			retryInterval: 0,
			retryTimeout:  time.Second,
		},
		"Other_errors_are_not_retried": {
			errs:          []error{otherErr},
			wantErr:       otherErr,
			wantCalls:     1,
			retryInterval: time.Second,
			retryTimeout:  time.Second,
		},
		"Cancelled_request_is_not_retried": {
			errs:          []error{himmelblau.ErrDeviceAuthenticationFailed},
			wantErr:       context.Canceled,
			wantCalls:     1,
			retryInterval: time.Second,
			retryTimeout:  time.Second,
			cancelRequest: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			calls := 0
			gotToken, gotErr := msentraid.AcquireGraphAccessTokenWithRetry(ctx, func() (string, error) {
				calls++
				if tc.cancelRequest {
					cancel()
				}
				token := ""
				if calls <= len(tc.tokens) {
					token = tc.tokens[calls-1]
				}
				err := tc.errs[min(calls-1, len(tc.errs)-1)]
				return token, err
			}, tc.retryInterval, tc.retryTimeout)

			if tc.wantErr != nil {
				require.ErrorIs(t, gotErr, tc.wantErr)
			} else {
				require.NoError(t, gotErr)
			}
			require.Equal(t, tc.wantToken, gotToken)
			if tc.wantMinCalls > 0 {
				require.GreaterOrEqual(t, calls, tc.wantMinCalls)
			} else {
				require.Equal(t, tc.wantCalls, calls)
			}
		})
	}
}

func TestAcquireGraphAccessTokenWithRetryCancelledDuringDelay(t *testing.T) {
	t.Parallel()

	// The retry windows are an hour, so only this deadline can end the
	// test. Keep it generous so CI scheduling cannot starve the first
	// acquire call before it is counted.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	calls := 0
	gotToken, gotErr := msentraid.AcquireGraphAccessTokenWithRetry(ctx, func() (string, error) {
		calls++
		return "", himmelblau.ErrDeviceAuthenticationFailed
	}, time.Hour, time.Hour)

	require.ErrorIs(t, gotErr, context.DeadlineExceeded,
		"a request cancelled during the replication delay must not be retried")
	require.Zero(t, gotToken)
	require.Equal(t, 1, calls)
}

func TestAcquireGraphAccessTokenWithRetryRejectsTokenAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	gotToken, gotErr := msentraid.AcquireGraphAccessTokenWithRetry(ctx, func() (string, error) {
		cancel()
		return "access-token", nil
	}, 0)

	require.ErrorIs(t, gotErr, context.Canceled,
		"a token returned after request cancellation must not be accepted")
	require.Empty(t, gotToken)
}

func TestAcquireGraphAccessTokenWithRetryRejectsRetryTokenAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	gotToken, gotErr := msentraid.AcquireGraphAccessTokenWithRetry(ctx, func() (string, error) {
		calls++
		if calls == 1 {
			return "", himmelblau.ErrDeviceAuthenticationFailed
		}
		cancel()
		return "access-token", nil
	}, 0)

	require.ErrorIs(t, gotErr, context.Canceled,
		"a retry token returned after request cancellation must not be accepted")
	require.Empty(t, gotToken)
	require.Equal(t, 2, calls)
}

func TestGetGroupsRetriesTransientDeviceAuthenticationFailure(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "User.Read",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	graphAccessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	graphAccessTokenStr, err := graphAccessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign Graph access token")

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	var acquireCalls atomic.Int32
	p := msentraid.New()
	p.SetTokenScopesForGraphAPI([]string{"GroupMember.Read.All"})
	p.SetGraphAccessTokenAcquirerForTests(func(
		context.Context,
		string,
		string,
		*oauth2.Token,
		himmelblau.DeviceRegistrationData,
	) (string, error) {
		if acquireCalls.Add(1) < 3 {
			return "", himmelblau.ErrDeviceAuthenticationFailed
		}
		return graphAccessTokenStr, nil
	})

	deviceRegistrationData, err := json.Marshal(himmelblau.DeviceRegistrationData{})
	require.NoError(t, err)

	got, err := p.GetGroups(
		context.Background(),
		"client-id",
		"tenant-id",
		&oauth2.Token{AccessToken: accessTokenStr, RefreshToken: "refresh-token"},
		map[string]any{"msgraph_host": mockServer.URL},
		deviceRegistrationData,
		true,
	)
	require.NoError(t, err, "GetGroups should retry transient device authentication failures")
	require.Equal(t, int32(3), acquireCalls.Load(), "Graph token acquisition should be retried until it succeeds")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestGetGroupsClientCredentialsUsesConfiguredIssuerAndGraphHosts(t *testing.T) {
	t.Parallel()

	const (
		clientID     = "client-id"
		clientSecret = "client-secret"
		tenantID     = "tenant-id"
		userOID      = "user-oid"
	)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": userOID,
		"scp": "User.Read",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	var tokenEndpointCalled atomic.Bool
	var graphEndpointCalled atomic.Bool
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/"+tenantID+"/oauth2/v2.0/token":
			tokenEndpointCalled.Store(true)
			require.NoError(t, r.ParseForm(), "failed to parse client credentials form")
			require.Equal(t, "client_credentials", r.Form.Get("grant_type"))
			require.Equal(t, clientID, r.Form.Get("client_id"))
			require.Equal(t, clientSecret, r.Form.Get("client_secret"))
			require.Equal(t, mockServer.URL+"/.default", r.Form.Get("scope"))

			appToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
			appTokenStr, err := appToken.SignedString(testutils.MockKey)
			require.NoError(t, err, "failed to sign app token")

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, appTokenStr)

		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/users/"+userOID+"/") &&
			strings.Contains(r.URL.Path, "/transitiveMemberOf/") &&
			strings.HasSuffix(r.URL.Path, "graph.group"):
			graphEndpointCalled.Store(true)
			simpleGroupHandler(w, r)

		default:
			require.Fail(t, "unexpected request", "method=%s path=%s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(mockServer.Close)

	p := msentraid.New()
	p.SetGraphClientSecret(clientSecret)

	got, err := p.GetGroups(
		context.Background(),
		clientID,
		fmt.Sprintf("%s/%s/v2.0", mockServer.URL, tenantID),
		&oauth2.Token{AccessToken: accessTokenStr},
		map[string]any{"msgraph_host": mockServer.URL + "/v1.0"},
		nil,
		false,
	)
	require.NoError(t, err, "GetGroups should use client credentials against configured hosts")
	require.True(t, tokenEndpointCalled.Load(), "client credentials token endpoint should have been called")
	require.True(t, graphEndpointCalled.Load(), "Graph users endpoint should have been called")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestIsTokenForDeviceRegistration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deviceRegistrationData []byte

		want bool
	}{
		"True_when_device_registration_data_is_present": {deviceRegistrationData: []byte("device-registration-data"), want: true},
		"False_when_device_registration_data_is_absent": {deviceRegistrationData: nil, want: false},
		"False_when_device_registration_data_is_empty":  {deviceRegistrationData: []byte{}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			got := p.IsTokenForDeviceRegistration(&token.AuthCachedInfo{DeviceRegistrationData: tc.deviceRegistrationData})

			require.Equal(t, tc.want, got, "IsTokenForDeviceRegistration should return the expected value")
		})
	}
}

func TestMaybeRegisterDevice(t *testing.T) {
	t.Parallel()

	registrationData, err := json.Marshal(&himmelblau.DeviceRegistrationData{
		DeviceID:      "test-device-id",
		CertKey:       []byte("test-cert-key"),
		TransportKey:  []byte("test-transport-key"),
		AuthValue:     "test-auth-value",
		TPMMachineKey: []byte("test-tpm-machine-key"),
	})
	require.NoError(t, err, "Failed to marshal device registration data")

	type args = maybeRegisterDeviceArgs

	tests := map[string]struct {
		args

		wantErr bool
	}{
		"Successfully_registers_device":       {},
		"Reuses_existing_device_registration": {args: args{oldData: registrationData}},

		"Error_when_username_does_not_have_a_domain": {args: args{username: "userwithoutdomain"}, wantErr: true},
		"Error_when_discover_url_is_invalid_format":  {args: args{discoveryURL: "invalid-url"}, wantErr: true},
		"Error_when_discover_url_is_unreachable":     {args: args{discoveryURL: "http://invalid-url"}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registrationData, cleanup, err := maybeRegisterDevice(t, &tc.args)
			t.Cleanup(cleanup)
			if tc.wantErr {
				require.Error(t, err, "MaybeRegisterDevice should return an error")
				return
			}
			require.NoError(t, err, "MaybeRegisterDevice should not return an error")

			if tc.oldData != nil {
				require.Equal(t, tc.oldData, registrationData, "MaybeRegisterDevice should return the existing registration data")
			}

			// We don't compare the registration data with a golden file, because it differs every time due to the
			// generated keys. Instead, we just check that it's not empty.
			require.NotEmpty(t, registrationData, "MaybeRegisterDevice should return non-empty registration data")
		})
	}
}

type maybeRegisterDeviceArgs struct {
	username     string
	oldData      []byte
	discoveryURL string
}

func maybeRegisterDevice(
	t *testing.T,
	args *maybeRegisterDeviceArgs,
) ([]byte, func(), error) {
	// Start the mock MS server (or reuse the existing one)
	ensureMockMSServerForDeviceRegistration(t)
	mockServer := mockMSServerForDeviceRegistration

	if args == nil {
		args = &maybeRegisterDeviceArgs{}
	}

	if args.discoveryURL == "" {
		args.discoveryURL = mockServer.URL
	}

	if args.username == "" {
		args.username = "user@example.com"
	}

	// Make libhimmelblau use the mock MS server. These settings are global,
	// so test case which need different settings must not run in parallel.
	if args.discoveryURL == "" {
		// We don't need to set the environment variable, just ensure no other test is modifying it while we run.
		discoveryURLMu.RLock()
		defer discoveryURLMu.RUnlock()
	} else {
		// Set the environment variable for the duration of the test.
		discoveryURLMu.Lock()
		oldValue := os.Getenv("HIMMELBLAU_DISCOVERY_URL")
		err := os.Setenv("HIMMELBLAU_DISCOVERY_URL", args.discoveryURL)
		require.NoError(t, err, "Failed to set HIMMELBLAU_DISCOVERY_URL environment variable")
		defer func() {
			err := os.Setenv("HIMMELBLAU_DISCOVERY_URL", oldValue)
			discoveryURLMu.Unlock()
			require.NoError(t, err, "Failed to unset HIMMELBLAU_DISCOVERY_URL environment variable")
		}()
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")
	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	tenantID := "8de88d99-6d0f-44d7-a8a5-925b012e5940"
	issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)

	p := msentraid.New()

	return p.MaybeRegisterDevice(
		context.Background(),
		token,
		args.username,
		issuerURL,
		args.oldData,
	)
}

func TestIsTokenExpiredError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCode        string
		errorDescription string

		wantExpired bool
	}{
		"AADSTS50078_mfa_session_expired":              {errorCode: "invalid_grant", errorDescription: "AADSTS50078: Presented multi-factor authentication has expired due to policies configured by your administrator.", wantExpired: true},
		"AADSTS50089_flow_token_expired":               {errorCode: "invalid_grant", errorDescription: "AADSTS50089: Flow token has expired. User needs to reauthenticate.", wantExpired: true},
		"AADSTS50173_token_expired":                    {errorCode: "invalid_grant", errorDescription: "AADSTS50173: The provided grant has expired", wantExpired: true},
		"AADSTS70008_token_expired_due_to_inactivity":  {errorCode: "invalid_grant", errorDescription: "AADSTS70008: The refresh token has expired due to inactivity.", wantExpired: true},
		"AADSTS70043_token_expired":                    {errorCode: "invalid_grant", errorDescription: "AADSTS70043: The refresh token has expired or is invalid", wantExpired: true},
		"AADSTS700082_token_expired_due_to_inactivity": {errorCode: "invalid_grant", errorDescription: "AADSTS700082: The refresh token has expired due to inactivity.", wantExpired: true},

		"AADSTS50057_user_disabled": {errorCode: "invalid_grant", errorDescription: "AADSTS50057: The user account is disabled.", wantExpired: false},
		"Other_invalid_grant":       {errorCode: "invalid_grant", errorDescription: "AADSTS65001: The user or administrator has not consented to use the application.", wantExpired: false},
		"Non_invalid_grant_error":   {errorCode: "access_denied", errorDescription: "AADSTS50173: The provided grant has expired", wantExpired: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			err := &oauth2.RetrieveError{
				ErrorCode:        tc.errorCode,
				ErrorDescription: tc.errorDescription,
			}
			got := p.IsTokenExpiredError(err)
			require.Equal(t, tc.wantExpired, got, "IsTokenExpiredError returned unexpected result")
		})
	}
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	m.Run()
}
