//go:build withmsentraid

package msentraid_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/msentraid"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils/golden"
	"github.com/ubuntu/authd/log"
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
		invalidIDToken     bool
		tokenScopes        []string
		providerMetadata   map[string]any
		acquireAccessToken bool

		groupEndpointHandler http.HandlerFunc

		wantErr bool
	}{
		"Successfully_get_user_info": {},

		"Error_when_id_token_claims_are_invalid": {invalidIDToken: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idToken := validIDToken
			if tc.invalidIDToken {
				idToken = invalidIDToken
			}

			p := msentraid.New()

			got, err := p.GetUserInfo(idToken)
			if tc.wantErr {
				require.Error(t, err, "GetUserInfo should return an error")
				return
			}
			require.NoError(t, err, "GetUserInfo should not return an error")

			golden.CheckOrUpdateYAML(t, got)
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
			p.SetNeedsAccessTokenForGraphAPI(tc.acquireAccessToken)
			p.SetTokenScopesForGraphAPI(tc.tokenScopes)

			got, err := p.GetGroups(
				context.Background(),
				"",
				"",
				token,
				tc.providerMetadata,
				deviceRegistrationData,
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

func TestIsTokenForDeviceRegistration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		appID        string
		invalidToken bool

		want    bool
		wantErr bool
	}{
		"Success_when_token_has_microsoft_broker_app_ID": {appID: consts.MicrosoftBrokerAppID, want: true},
		"Success_when_token_has_other_app_ID":            {appID: "some-other-app-id", want: false},
		"Success_when_token_has_empty_app_ID":            {appID: "", want: false},

		"Error_when_token_has_no_app_ID": {appID: "-", wantErr: true},
		"Error_when_token_is_invalid":    {invalidToken: true, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			claims := jwt.MapClaims{"appid": tc.appID}
			if tc.appID == "-" {
				claims = jwt.MapClaims{}
			}

			accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			accessTokenString, err := accessToken.SignedString(testutils.MockKey)
			require.NoError(t, err, "Failed to sign access token")

			if tc.invalidToken {
				accessTokenString = "invalid-token"
			}

			token := &oauth2.Token{AccessToken: accessTokenString}

			p := msentraid.New()
			got, err := p.IsTokenForDeviceRegistration(token)

			if tc.wantErr {
				require.Error(t, err, "IsTokenForDeviceRegistration should return an error")
				return
			}
			require.NoError(t, err, "IsTokenForDeviceRegistration should not return an error")
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

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	m.Run()
}
