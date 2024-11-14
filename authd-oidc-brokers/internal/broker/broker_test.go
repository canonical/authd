package broker_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/ubuntu/authd-oidc-brokers/internal/password"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"github.com/ubuntu/authd-oidc-brokers/internal/token"
	"gopkg.in/yaml.v3"
)

var defaultProviderURL string

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuer   string
		clientID string
		dataDir  string

		wantErr bool
	}{
		"Successfully_create_new_broker":                              {},
		"Successfully_create_new_even_if_can_not_connect_to_provider": {issuer: "https://notavailable"},

		"Error_if_issuer_is_not_provided":   {issuer: "-", wantErr: true},
		"Error_if_clientID_is_not_provided": {clientID: "-", wantErr: true},
		"Error_if_dataDir_is_not_provided":  {dataDir: "-", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			switch tc.issuer {
			case "":
				tc.issuer = defaultProviderURL
			case "-":
				tc.issuer = ""
			}

			if tc.clientID == "-" {
				tc.clientID = ""
			} else {
				tc.clientID = "test-client-id"
			}

			if tc.dataDir == "-" {
				tc.dataDir = ""
			} else {
				tc.dataDir = t.TempDir()
			}

			bCfg := &broker.Config{DataDir: tc.dataDir}
			bCfg.SetIssuerURL(tc.issuer)
			bCfg.SetClientID(tc.clientID)
			b, err := broker.New(*bCfg)
			if tc.wantErr {
				require.Error(t, err, "New should have returned an error")
				return
			}
			require.NoError(t, err, "New should not have returned an error")
			require.NotNil(t, b, "New should have returned a non-nil broker")
		})
	}
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		customHandlers map[string]testutils.EndpointHandler

		wantOffline bool
	}{
		"Successfully_create_new_session": {},
		"Creates_new_session_in_offline_mode_if_provider_is_not_available": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			wantOffline: true,
		},
		"Creates_new_session_in_offline_mode_if_provider_connection_times_out": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantOffline: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []testutils.ProviderServerOption
			for endpoint, handler := range tc.customHandlers {
				opts = append(opts, testutils.WithHandler(endpoint, handler))
			}

			providerURL, cleanup := testutils.StartMockProviderServer("", nil, opts...)
			t.Cleanup(cleanup)
			cfg := &broker.Config{}
			cfg.SetIssuerURL(providerURL)
			b := newBrokerForTests(t, *cfg, nil)

			id, _, err := b.NewSession("test-user", "lang", "auth")
			require.NoError(t, err, "NewSession should not have returned an error")

			gotOffline, err := b.IsOffline(id)
			require.NoError(t, err, "Session should have been created")

			require.Equal(t, tc.wantOffline, gotOffline, "Session should have been created in the expected mode")
		})
	}
}

var supportedUILayouts = map[string]map[string]string{
	"form": {
		"type":  "form",
		"entry": "chars_password",
	},
	"form-without-entry": {
		"type": "form",
	},

	"qrcode": {
		"type": "qrcode",
		"wait": "true",
	},
	"qrcode-without-wait": {
		"type": "qrcode",
	},
	"qrcode-without-qrcode": {
		"type":           "qrcode",
		"renders_qrcode": "false",
		"wait":           "true",
	},
	"qrcode-without-wait-and-qrcode": {
		"type":           "qrcode",
		"renders_qrcode": "false",
	},

	"newpassword": {
		"type":  "newpassword",
		"entry": "chars_password",
	},
	"newpassword-without-entry": {
		"type": "newpassword",
	},
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionMode      string
		sessionID        string
		supportedLayouts []string

		providerAddress       string
		tokenExists           bool
		secondAuthStep        bool
		unavailableProvider   bool
		deviceAuthUnsupported bool

		wantErr bool
	}{
		// Auth Session
		"Get_device_auth_qr_if_there_is_no_token":                      {},
		"Get_newpassword_if_already_authenticated_with_device_auth_qr": {secondAuthStep: true},
		"Get_password_and_device_auth_qr_if_token_exists":              {tokenExists: true},

		"Get_only_password_if_token_exists_and_provider_is_not_available":                {tokenExists: true, providerAddress: "127.0.0.1:31310", unavailableProvider: true},
		"Get_only_password_if_token_exists_and_provider_does_not_support_device_auth_qr": {tokenExists: true, providerAddress: "127.0.0.1:31311", deviceAuthUnsupported: true},

		// Passwd Session
		"Get_only_password_if_token_exists_and_session_is_passwd":                      {sessionMode: "passwd", tokenExists: true},
		"Get_newpassword_if_already_authenticated_with_password_and_session_is_passwd": {sessionMode: "passwd", tokenExists: true, secondAuthStep: true},

		"Error_if_there_is_no_session": {sessionID: "-", wantErr: true},

		// General errors
		"Error_if_no_authentication_mode_is_supported":        {providerAddress: "127.0.0.1:31312", deviceAuthUnsupported: true, wantErr: true},
		"Error_if_expecting_device_auth_qr_but_not_supported": {supportedLayouts: []string{"qrcode-without-wait"}, wantErr: true},
		"Error_if_expecting_device_auth_but_not_supported":    {supportedLayouts: []string{"qrcode-without-wait-and-qrcode"}, wantErr: true},
		"Error_if_expecting_newpassword_but_not_supported":    {supportedLayouts: []string{"newpassword-without-entry"}, wantErr: true},
		"Error_if_expecting_password_but_not_supported":       {supportedLayouts: []string{"form-without-entry"}, wantErr: true},

		// Passwd session errors
		"Error_if_session_is_passwd_but_token_does_not_exist": {sessionMode: "passwd", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = "auth"
			}

			providerURL := defaultProviderURL
			if tc.providerAddress != "" {
				address := tc.providerAddress
				opts := []testutils.ProviderServerOption{}
				if tc.deviceAuthUnsupported {
					opts = append(opts, testutils.WithHandler(
						"/.well-known/openid-configuration",
						testutils.OpenIDHandlerWithNoDeviceEndpoint("http://"+address),
					))
				}
				if tc.unavailableProvider {
					opts = append(opts, testutils.WithHandler(
						"/.well-known/openid-configuration",
						testutils.UnavailableHandler(),
					))
				}
				var cleanup func()
				providerURL, cleanup = testutils.StartMockProviderServer(address, nil, opts...)
				t.Cleanup(cleanup)
			}
			cfg := &broker.Config{}
			cfg.SetIssuerURL(providerURL)
			b := newBrokerForTests(t, *cfg, nil)
			sessionID, _ := newSessionForTests(t, b, "", tc.sessionMode)
			if tc.sessionID == "-" {
				sessionID = ""
			}
			if tc.tokenExists {
				err := os.MkdirAll(filepath.Dir(b.TokenPathForSession(sessionID)), 0700)
				require.NoError(t, err, "Setup: MkdirAll should not have returned an error")
				err = os.WriteFile(b.TokenPathForSession(sessionID), []byte("some token"), 0600)
				require.NoError(t, err, "Setup: WriteFile should not have returned an error")
			}
			if tc.secondAuthStep {
				b.UpdateSessionAuthStep(sessionID, 1)
			}

			if tc.supportedLayouts == nil {
				tc.supportedLayouts = []string{"form", "qrcode", "newpassword"}
			}
			var layouts []map[string]string
			for _, layout := range tc.supportedLayouts {
				layouts = append(layouts, supportedUILayouts[layout])
			}

			got, err := b.GetAuthenticationModes(sessionID, layouts)
			if tc.wantErr {
				require.Error(t, err, "GetAuthenticationModes should have returned an error")
				return
			}
			require.NoError(t, err, "GetAuthenticationModes should not have returned an error")

			testutils.CheckOrUpdateGoldenYAML(t, got)
		})
	}
}

var supportedLayouts = []map[string]string{
	supportedUILayouts["form"],
	supportedUILayouts["qrcode"],
	supportedUILayouts["newpassword"],
}

var supportedLayoutsWithoutQrCode = []map[string]string{
	supportedUILayouts["form"],
	supportedUILayouts["qrcode-without-qrcode"],
	supportedUILayouts["newpassword"],
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modeName string

		tokenExists      bool
		secondAuthStep   bool
		passwdSession    bool
		customHandlers   map[string]testutils.EndpointHandler
		supportedLayouts []map[string]string

		wantErr bool
	}{
		"Successfully_select_password":       {modeName: authmodes.Password, tokenExists: true},
		"Successfully_select_device_auth_qr": {modeName: authmodes.DeviceQr},
		"Successfully_select_device_auth":    {supportedLayouts: supportedLayoutsWithoutQrCode, modeName: authmodes.Device},
		"Successfully_select_newpassword":    {modeName: authmodes.NewPassword, secondAuthStep: true},

		"Selected_newpassword_shows_correct_label_in_passwd_session": {modeName: authmodes.NewPassword, passwdSession: true, tokenExists: true, secondAuthStep: true},

		"Error_when_selecting_invalid_mode": {modeName: "invalid", wantErr: true},
		"Error_when_selecting_device_auth_qr_but_provider_is_unavailable": {modeName: authmodes.DeviceQr, wantErr: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.UnavailableHandler(),
			},
		},
		"Error_when_selecting_device_auth_but_provider_is_unavailable": {
			supportedLayouts: supportedLayoutsWithoutQrCode,
			modeName:         authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.UnavailableHandler(),
			},
			wantErr: true,
		},
		"Error_when_selecting_device_auth_qr_but_request_times_out": {modeName: authmodes.DeviceQr, wantErr: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_selecting_device_auth_but_request_times_out": {
			supportedLayouts: supportedLayoutsWithoutQrCode,
			modeName:         authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			providerURL := defaultProviderURL
			if tc.customHandlers != nil {
				var opts []testutils.ProviderServerOption
				for path, handler := range tc.customHandlers {
					opts = append(opts, testutils.WithHandler(path, handler))
				}
				var cleanup func()
				providerURL, cleanup = testutils.StartMockProviderServer("", nil, opts...)
				defer cleanup()
			}

			sessionType := "auth"
			if tc.passwdSession {
				sessionType = "passwd"
			}

			cfg := &broker.Config{}
			cfg.SetIssuerURL(providerURL)
			b := newBrokerForTests(t, *cfg, nil)
			sessionID, _ := newSessionForTests(t, b, "", sessionType)

			if tc.tokenExists {
				err := os.MkdirAll(filepath.Dir(b.TokenPathForSession(sessionID)), 0700)
				require.NoError(t, err, "Setup: MkdirAll should not have returned an error")
				err = os.WriteFile(b.TokenPathForSession(sessionID), []byte("some token"), 0600)
				require.NoError(t, err, "Setup: WriteFile should not have returned an error")
			}
			if tc.secondAuthStep {
				b.UpdateSessionAuthStep(sessionID, 1)
			}
			if tc.supportedLayouts == nil {
				tc.supportedLayouts = supportedLayouts
			}

			// We need to do a GAM call first to get all the modes.
			_, err := b.GetAuthenticationModes(sessionID, tc.supportedLayouts)
			require.NoError(t, err, "Setup: GetAuthenticationModes should not have returned an error")

			got, err := b.SelectAuthenticationMode(sessionID, tc.modeName)
			if tc.wantErr {
				require.Error(t, err, "SelectAuthenticationMode should have returned an error")
				return
			}
			require.NoError(t, err, "SelectAuthenticationMode should not have returned an error")

			testutils.CheckOrUpdateGoldenYAML(t, got)
		})
	}
}

type isAuthenticatedResponse struct {
	Access string
	Data   string
	Err    string
}

func TestIsAuthenticated(t *testing.T) {
	t.Parallel()

	correctPassword := "password"

	tests := map[string]struct {
		sessionMode string
		username    string

		firstMode        string
		firstChallenge   string
		firstAuthInfo    map[string]any
		badFirstKey      bool
		getUserInfoFails bool

		customHandlers map[string]testutils.EndpointHandler
		address        string

		wantSecondCall  bool
		secondMode      string
		secondChallenge string

		token                *tokenOptions
		invalidAuthData      bool
		dontWaitForFirstCall bool
		readOnlyDataDir      bool
	}{
		"Successfully_authenticate_user_with_device_auth_and_newpassword": {firstChallenge: "-", wantSecondCall: true},
		"Successfully_authenticate_user_with_password":                    {firstMode: authmodes.Password, token: &tokenOptions{}},

		"Authenticating_with_qrcode_reacquires_token":          {firstChallenge: "-", wantSecondCall: true, token: &tokenOptions{}},
		"Authenticating_with_password_refreshes_expired_token": {firstMode: authmodes.Password, token: &tokenOptions{expired: true}},
		"Authenticating_with_password_still_allowed_if_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
		},
		"Authenticating_with_password_still_allowed_if_token_is_expired_and_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
		},
		"Authenticating_still_allowed_if_token_is_missing_scopes": {
			firstChallenge: "-",
			wantSecondCall: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31313", nil),
			},
			address: "127.0.0.1:31313",
		},

		"Error_when_authentication_data_is_invalid":         {invalidAuthData: true},
		"Error_when_challenge_can_not_be_decrypted":         {firstMode: authmodes.Password, badFirstKey: true},
		"Error_when_provided_wrong_challenge":               {firstMode: authmodes.Password, token: &tokenOptions{}, firstChallenge: "wrongpassword"},
		"Error_when_can_not_cache_token":                    {firstChallenge: "-", wantSecondCall: true, readOnlyDataDir: true},
		"Error_when_IsAuthenticated_is_ongoing_for_session": {dontWaitForFirstCall: true, wantSecondCall: true},

		"Error_when_mode_is_password_and_token_does_not_exist": {firstMode: authmodes.Password},
		"Error_when_mode_is_password_but_server_returns_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.BadRequestHandler(),
			},
		},
		"Error_when_mode_is_password_and_token_is_invalid":       {firstMode: authmodes.Password, token: &tokenOptions{invalid: true}},
		"Error_when_token_is_expired_and_refreshing_token_fails": {firstMode: authmodes.Password, token: &tokenOptions{expired: true, noRefreshToken: true}},
		"Error_when_mode_is_password_and_token_refresh_times_out": {firstMode: authmodes.Password, token: &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_existing_token_has_no_user_info_and_fetching_user_info_fails": {firstMode: authmodes.Password, token: &tokenOptions{noUserInfo: true}, getUserInfoFails: true},

		"Error_when_mode_is_qrcode_and_response_is_invalid": {firstAuthInfo: map[string]any{"response": "not a valid response"}},
		"Error_when_mode_is_qrcode_and_link_expires": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.ExpiryDeviceAuthHandler(),
			},
		},
		"Error_when_mode_is_qrcode_and_can_not_get_token": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Error_when_mode_is_qrcode_and_can_not_get_token_due_to_timeout": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_mode_is_link_code_and_response_is_invalid": {
			firstMode:     authmodes.Device,
			firstAuthInfo: map[string]any{"response": "not a valid response"},
		},
		"Error_when_mode_is_link_code_and_link_expires": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.ExpiryDeviceAuthHandler(),
			},
		},
		"Error_when_mode_is_link_code_and_can_not_get_token": {
			firstMode: authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Error_when_mode_is_link_code_and_can_not_get_token_due_to_timeout": {
			firstMode: authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_empty_challenge_is_provided_for_local_password": {firstChallenge: "-", wantSecondCall: true, secondChallenge: "-"},
		"Error_when_mode_is_newpassword_and_session_has_no_token":   {firstMode: authmodes.NewPassword},
		// This test case also tests that errors with double quotes are marshaled to JSON correctly.
		"Error_when_selected_username_does_not_match_the_provider_one": {username: "not-matching", firstChallenge: "-"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = "auth"
			}

			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")

			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			providerURL := defaultProviderURL
			if tc.customHandlers != nil {
				var opts []testutils.ProviderServerOption
				for path, handler := range tc.customHandlers {
					opts = append(opts, testutils.WithHandler(path, handler))
				}
				var cleanup func()
				providerURL, cleanup = testutils.StartMockProviderServer(tc.address, nil, opts...)
				t.Cleanup(cleanup)
			}

			cfg := &broker.Config{DataDir: dataDir}
			cfg.SetIssuerURL(providerURL)
			mockProvider := &testutils.MockProvider{GetUserInfoFails: tc.getUserInfoFails}
			b := newBrokerForTests(t, *cfg, mockProvider)
			sessionID, key := newSessionForTests(t, b, tc.username, tc.sessionMode)

			if tc.token != nil {
				tc.token.issuer = providerURL
				generateAndStoreCachedInfo(t, *tc.token, b.TokenPathForSession(sessionID))
				err = password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}

			var readOnlyDataCleanup, readOnlyTokenCleanup func()
			if tc.readOnlyDataDir {
				if tc.token != nil {
					readOnlyTokenCleanup = testutils.MakeReadOnly(t, b.TokenPathForSession(sessionID))
					t.Cleanup(readOnlyTokenCleanup)
				}
				readOnlyDataCleanup = testutils.MakeReadOnly(t, b.DataDir())
				t.Cleanup(readOnlyDataCleanup)
			}

			switch tc.firstChallenge {
			case "":
				tc.firstChallenge = correctPassword
			case "-":
				tc.firstChallenge = ""
			}

			authData := "{}"
			if tc.firstChallenge != "" {
				eKey := key
				if tc.badFirstKey {
					eKey = ""
				}
				authData = `{"challenge":"` + encryptChallenge(t, tc.firstChallenge, eKey) + `"}`
			}
			if tc.invalidAuthData {
				authData = "invalid json"
			}

			firstCallDone := make(chan struct{})
			go func() {
				defer close(firstCallDone)

				if tc.firstMode == "" {
					tc.firstMode = authmodes.DeviceQr
				}
				updateAuthModes(t, b, sessionID, tc.firstMode)

				if tc.firstAuthInfo != nil {
					for k, v := range tc.firstAuthInfo {
						require.NoError(t, b.SetAuthInfo(sessionID, k, v), "Setup: Failed to set AuthInfo for tests")
					}
				}

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_call"), out, 0600)
				require.NoError(t, err, "Failed to write first response")
			}()

			if !tc.dontWaitForFirstCall {
				<-firstCallDone
			}

			if tc.wantSecondCall {
				// Give some time for the first call
				time.Sleep(10 * time.Millisecond)

				challenge := "passwordpassword"
				if tc.secondChallenge == "-" {
					challenge = ""
				}

				secondAuthData := `{"challenge":"` + encryptChallenge(t, challenge, key) + `"}`
				if tc.invalidAuthData {
					secondAuthData = "invalid json"
				}

				if tc.secondMode == "" {
					tc.secondMode = authmodes.NewPassword
				}

				secondCallDone := make(chan struct{})
				go func() {
					defer close(secondCallDone)

					updateAuthModes(t, b, sessionID, tc.secondMode)

					access, data, err := b.IsAuthenticated(sessionID, secondAuthData)
					require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

					got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
					out, err := yaml.Marshal(got)
					require.NoError(t, err, "Failed to marshal second response")

					err = os.WriteFile(filepath.Join(outDir, "second_call"), out, 0600)
					require.NoError(t, err, "Failed to write second response")
				}()
				<-secondCallDone
			}
			<-firstCallDone

			// We need to restore some permissions in order to save the golden files.
			if tc.readOnlyDataDir {
				readOnlyDataCleanup()
				if tc.token != nil {
					readOnlyTokenCleanup()
				}
			}

			// Ensure that the token content is generic to avoid golden file conflicts
			if _, err := os.Stat(b.TokenPathForSession(sessionID)); err == nil {
				err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely an encrypted token"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic token file")
			}
			passwordPath := b.PasswordFilepathForSession(sessionID)
			if _, err := os.Stat(passwordPath); err == nil {
				err := os.WriteFile(passwordPath, []byte("Definitely a hashed password"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic password file")
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			if _, err := os.Stat(filepath.Dir(b.TokenPathForSession(sessionID))); err == nil {
				toReplace := strings.ReplaceAll(strings.TrimPrefix(providerURL, "http://"), ":", "_")
				tokenDir := filepath.Dir(filepath.Dir(b.TokenPathForSession(sessionID)))
				newTokenDir := strings.ReplaceAll(tokenDir, toReplace, "provider_url")
				err := os.Rename(tokenDir, newTokenDir)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename token directory")
					t.Logf("Failed to rename token directory: %v", err)
				}
			}

			testutils.CheckOrUpdateGoldenFileTree(t, outDir, testutils.GoldenPath(t))
		})
	}
}

// Due to ordering restrictions, this test can not be run in parallel, otherwise the routines would not be ordered as expected.
func TestConcurrentIsAuthenticated(t *testing.T) {
	tests := map[string]struct {
		firstCallDelay  int
		secondCallDelay int

		timeBetween time.Duration
	}{
		"First_auth_starts_and_finishes_before_second":                  {secondCallDelay: 1, timeBetween: 2 * time.Second},
		"First_auth_starts_first_but_second_finishes_first":             {firstCallDelay: 3, timeBetween: time.Second},
		"First_auth_starts_first_then_second_starts_and_first_finishes": {firstCallDelay: 2, secondCallDelay: 3, timeBetween: time.Second},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")
			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			username1 := "user1@example.com"
			username2 := "user2@example.com"

			providerURL, cleanup := testutils.StartMockProviderServer("", &testutils.TokenHandlerOptions{
				IDTokenClaims: []map[string]interface{}{
					{"sub": "user1", "name": "user1", "email": username1},
					{"sub": "user2", "name": "user2", "email": username2},
				},
			})
			defer cleanup()

			cfg := &broker.Config{DataDir: dataDir}
			cfg.SetIssuerURL(providerURL)
			mockProvider := &testutils.MockProvider{FirstCallDelay: tc.firstCallDelay, SecondCallDelay: tc.secondCallDelay}
			b := newBrokerForTests(t, *cfg, mockProvider)

			firstSession, firstKey := newSessionForTests(t, b, username1, "")
			firstToken := tokenOptions{username: username1, issuer: providerURL}
			generateAndStoreCachedInfo(t, firstToken, b.TokenPathForSession(firstSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(firstSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			secondSession, secondKey := newSessionForTests(t, b, username2, "")
			secondToken := tokenOptions{username: username2, issuer: providerURL}
			generateAndStoreCachedInfo(t, secondToken, b.TokenPathForSession(secondSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(secondSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			firstCallDone := make(chan struct{})
			go func() {
				t.Logf("%s: First auth starting", t.Name())
				defer close(firstCallDone)

				updateAuthModes(t, b, firstSession, authmodes.Password)

				authData := `{"challenge":"` + encryptChallenge(t, "password", firstKey) + `"}`

				access, data, err := b.IsAuthenticated(firstSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_auth"), out, 0600)
				require.NoError(t, err, "Failed to write first response")

				t.Logf("%s: First auth done", t.Name())
			}()

			time.Sleep(tc.timeBetween)

			secondCallDone := make(chan struct{})
			go func() {
				t.Logf("%s: Second auth starting", t.Name())
				defer close(secondCallDone)

				updateAuthModes(t, b, secondSession, authmodes.Password)

				authData := `{"challenge":"` + encryptChallenge(t, "password", secondKey) + `"}`

				access, data, err := b.IsAuthenticated(secondSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal second response")

				err = os.WriteFile(filepath.Join(outDir, "second_auth"), out, 0600)
				require.NoError(t, err, "Failed to write second response")

				t.Logf("%s: Second auth done", t.Name())
			}()

			<-firstCallDone
			<-secondCallDone

			for _, sessionID := range []string{firstSession, secondSession} {
				// Ensure that the token content is generic to avoid golden file conflicts
				if _, err := os.Stat(b.TokenPathForSession(sessionID)); err == nil {
					err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely an encrypted token"), 0600)
					require.NoError(t, err, "Teardown: Failed to write generic token file")
				}
				passwordPath := b.PasswordFilepathForSession(sessionID)
				if _, err := os.Stat(passwordPath); err == nil {
					err := os.WriteFile(passwordPath, []byte("Definitely a hashed password"), 0600)
					require.NoError(t, err, "Teardown: Failed to write generic password file")
				}
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			issuerDataDir := filepath.Dir(b.UserDataDirForSession(firstSession))
			if _, err := os.Stat(issuerDataDir); err == nil {
				toReplace := strings.ReplaceAll(strings.TrimPrefix(providerURL, "http://"), ":", "_")
				newIssuerDataDir := strings.ReplaceAll(issuerDataDir, toReplace, "provider_url")
				err := os.Rename(issuerDataDir, newIssuerDataDir)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename issuer data directory")
					t.Logf("Failed to rename issuer data directory: %v", err)
				}
			}
			testutils.CheckOrUpdateGoldenFileTree(t, outDir, testutils.GoldenPath(t))
		})
	}
}

func TestFetchUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username string
		token    tokenOptions

		emptyHomeDir bool
		emptyGroups  bool
		wantGroupErr bool
		wantErr      bool
	}{
		"Successfully_fetch_user_info_with_groups":                         {},
		"Successfully_fetch_user_info_without_groups":                      {emptyGroups: true},
		"Successfully_fetch_user_info_with_default_home_when_not_provided": {emptyHomeDir: true},

		"Error_when_token_can_not_be_validated":                   {token: tokenOptions{invalid: true}, wantErr: true},
		"Error_when_ID_token_claims_are_invalid":                  {token: tokenOptions{invalidClaims: true}, wantErr: true},
		"Error_when_username_is_not_configured":                   {token: tokenOptions{username: "-"}, wantErr: true},
		"Error_when_username_is_different_than_the_requested_one": {token: tokenOptions{username: "other-user@email.com"}, wantErr: true},
		"Error_when_getting_user_groups":                          {wantGroupErr: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			homeDirPath := "/home/userInfoTests/"
			if tc.emptyHomeDir {
				homeDirPath = ""
			}

			dataDir := t.TempDir()
			clientID := "test-client-id"
			brokerCfg := &broker.Config{DataDir: dataDir}
			brokerCfg.SetIssuerURL(defaultProviderURL)
			brokerCfg.SetHomeBaseDir(homeDirPath)
			brokerCfg.SetClientID(clientID)

			var getGroupsFunc func() ([]info.Group, error)
			if tc.wantGroupErr {
				getGroupsFunc = func() ([]info.Group, error) {
					return nil, errors.New("error getting groups")
				}
			} else if tc.emptyGroups {
				getGroupsFunc = func() ([]info.Group, error) {
					return []info.Group{}, nil
				}
			} else {
				getGroupsFunc = func() ([]info.Group, error) {
					return []info.Group{
						{Name: "test-fetch-user-info-remote-group", UGID: "12345"},
						{Name: "linux-test-fetch-user-info-local-group", UGID: ""},
					}, nil
				}
			}

			mockProvider := &testutils.MockProvider{GetGroupsFunc: getGroupsFunc}

			b, err := broker.New(*brokerCfg, broker.WithCustomProvider(mockProvider))
			require.NoError(t, err, "Setup: New should not have returned an error")

			if tc.username == "" {
				tc.username = "test-user@email.com"
			}
			tc.token.issuer = defaultProviderURL

			sessionID, _, err := b.NewSession(tc.username, "lang", "auth")
			require.NoError(t, err, "Setup: Failed to create session for the tests")

			cachedInfo := generateCachedInfo(t, tc.token)
			if cachedInfo == nil {
				cachedInfo = &token.AuthCachedInfo{}
			}

			got, err := b.FetchUserInfo(sessionID, cachedInfo)
			if tc.wantErr {
				require.Error(t, err, "FetchUserInfo should have returned an error")
				return
			}
			require.NoError(t, err, "FetchUserInfo should not have returned an error")

			testutils.CheckOrUpdateGoldenYAML(t, got)
		})
	}
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	providerURL, cleanup := testutils.StartMockProviderServer("", nil, testutils.WithHandler("/token", testutils.HangingHandler(3*time.Second)))
	t.Cleanup(cleanup)

	cfg := &broker.Config{}
	cfg.SetIssuerURL(providerURL)
	b := newBrokerForTests(t, *cfg, nil)
	sessionID, _ := newSessionForTests(t, b, "", "")

	updateAuthModes(t, b, sessionID, authmodes.DeviceQr)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _, err := b.IsAuthenticated(sessionID, `{}`)
		require.Error(t, err, "IsAuthenticated should have returned an error")
	}()

	// Wait for the call to hang
	time.Sleep(50 * time.Millisecond)

	b.CancelIsAuthenticated(sessionID)
	<-stopped
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	cfg := &broker.Config{}
	cfg.SetIssuerURL(defaultProviderURL)
	b := newBrokerForTests(t, *cfg, nil)

	sessionID, _ := newSessionForTests(t, b, "", "")

	// Try to end a session that does not exist
	err := b.EndSession("nonexistent")
	require.Error(t, err, "EndSession should have returned an error when ending a nonexistent session")

	// End a session that exists
	err = b.EndSession(sessionID)
	require.NoError(t, err, "EndSession should not have returned an error when ending an existent session")
}

func TestUserPreCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username        string
		allowedSuffixes []string
		homePrefix      string

		wantErr bool
	}{
		"Successfully_allow_username_with_matching_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"}},
		"Successfully_allow_username_that_matches_at_least_one_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
		},
		"Return_userinfo_with_correct_homedir_after_precheck": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"},
			homePrefix:      "/home/allowed/",
		},

		"Error_when_username_does_not_match_allowed_suffix": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@allowed"},
			wantErr:         true,
		},
		"Error_when_username_does_not_match_any_of_the_allowed_suffixes": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
			wantErr:         true,
		},
		"Error_when_no_allowed_suffixes_are_provided": {
			username: "user@allowed",
			wantErr:  true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := &broker.Config{}
			cfg.SetIssuerURL(defaultProviderURL)
			cfg.SetHomeBaseDir(tc.homePrefix)
			cfg.SetAllowedSSHSuffixes(tc.allowedSuffixes)
			b := newBrokerForTests(t, *cfg, nil)

			got, err := b.UserPreCheck(tc.username)
			if tc.wantErr {
				require.Error(t, err, "UserPreCheck should have returned an error")
				return
			}
			require.NoError(t, err, "UserPreCheck should not have returned an error")

			testutils.CheckOrUpdateGolden(t, got)
		})
	}
}

func TestMain(m *testing.M) {
	var cleanup func()
	defaultProviderURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	os.Exit(m.Run())
}
