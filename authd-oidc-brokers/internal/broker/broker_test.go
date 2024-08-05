package broker_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

var defaultProvider *httptest.Server

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuer    string
		clientID  string
		cachePath string

		wantErr bool
	}{
		"Successfully create new broker":                              {},
		"Successfully create new even if can not connect to provider": {issuer: "https://notavailable"},

		"Error if issuer is not provided":   {issuer: "-", wantErr: true},
		"Error if clientID is not provided": {clientID: "-", wantErr: true},
		"Error if cacheDir is not provided": {cachePath: "-", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			switch tc.issuer {
			case "":
				tc.issuer = defaultProvider.URL
			case "-":
				tc.issuer = ""
			}

			if tc.clientID == "-" {
				tc.clientID = ""
			} else {
				tc.clientID = "test-client-id"
			}

			if tc.cachePath == "-" {
				tc.cachePath = ""
			} else {
				tc.cachePath = t.TempDir()
			}

			bCfg := broker.Config{
				IssuerURL: tc.issuer,
				ClientID:  tc.clientID,
				CachePath: tc.cachePath,
			}
			b, err := broker.New(bCfg)
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
		customHandlers map[string]testutils.ProviderHandler

		wantOffline bool
	}{
		"Successfully create new session": {},
		"Creates new session in offline mode if provider is not available": {
			customHandlers: map[string]testutils.ProviderHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			wantOffline: true,
		},
		"Creates new session in offline mode if provider connection times out": {
			customHandlers: map[string]testutils.ProviderHandler{
				"/.well-known/openid-configuration": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantOffline: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []testutils.OptionProvider
			for endpoint, handler := range tc.customHandlers {
				opts = append(opts, testutils.WithHandler(endpoint, handler))
			}

			provider, stopServer := testutils.StartMockProvider("", opts...)
			t.Cleanup(stopServer)
			b := newBrokerForTests(t, broker.Config{IssuerURL: provider.URL})

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
		"Get device_auth if there is no token":                      {},
		"Get newpassword if already authenticated with device_auth": {secondAuthStep: true},
		"Get password and device_auth if token exists":              {tokenExists: true},

		"Get only password if token exists and provider is not available":             {tokenExists: true, providerAddress: "127.0.0.1:31310", unavailableProvider: true},
		"Get only password if token exists and provider does not support device_auth": {tokenExists: true, providerAddress: "127.0.0.1:31311", deviceAuthUnsupported: true},

		// Passwd Session
		"Get only password if token exists and session is passwd":                      {sessionMode: "passwd", tokenExists: true},
		"Get newpassword if already authenticated with password and session is passwd": {sessionMode: "passwd", tokenExists: true, secondAuthStep: true},

		"Error if there is no session": {sessionID: "-", wantErr: true},

		// General errors
		"Error if no authentication mode is supported":     {providerAddress: "127.0.0.1:31312", deviceAuthUnsupported: true, wantErr: true},
		"Error if expecting device_auth but not supported": {supportedLayouts: []string{"qrcode-without-wait"}, wantErr: true},
		"Error if expecting newpassword but not supported": {supportedLayouts: []string{"newpassword-without-entry"}, wantErr: true},
		"Error if expecting password but not supported":    {supportedLayouts: []string{"form-without-entry"}, wantErr: true},

		// Passwd session errors
		"Error if session is passwd but token does not exist": {sessionMode: "passwd", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = "auth"
			}

			provider := defaultProvider
			var stopServer func()
			if tc.providerAddress != "" {
				address := tc.providerAddress
				opts := []testutils.OptionProvider{}
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
				provider, stopServer = testutils.StartMockProvider(address, opts...)
				t.Cleanup(stopServer)
			}
			b := newBrokerForTests(t, broker.Config{IssuerURL: provider.URL})
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

			want := testutils.LoadWithUpdateFromGoldenYAML(t, got)
			require.Equal(t, want, got, "GetAuthenticationModes should have returned the expected value")
		})
	}
}

var supportedLayouts = []map[string]string{
	supportedUILayouts["form"],
	supportedUILayouts["qrcode"],
	supportedUILayouts["newpassword"],
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modeName string

		tokenExists    bool
		secondAuthStep bool
		passwdSession  bool
		customHandlers map[string]testutils.ProviderHandler

		wantErr bool
	}{
		"Successfully select password":    {modeName: "password", tokenExists: true},
		"Successfully select device_auth": {modeName: "device_auth"},
		"Successfully select newpassword": {modeName: "newpassword", secondAuthStep: true},

		"Selected newpassword shows correct label in passwd session": {modeName: "newpassword", passwdSession: true, tokenExists: true, secondAuthStep: true},

		"Error when selecting invalid mode": {modeName: "invalid", wantErr: true},
		"Error when selecting device_auth but provider is unavailable": {modeName: "device_auth", wantErr: true,
			customHandlers: map[string]testutils.ProviderHandler{
				"/device_auth": testutils.UnavailableHandler(),
			},
		},
		"Error when selecting device_auth but request times out": {modeName: "device_auth", wantErr: true,
			customHandlers: map[string]testutils.ProviderHandler{
				"/device_auth": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := defaultProvider
			if tc.customHandlers != nil {
				var opts []testutils.OptionProvider
				for path, handler := range tc.customHandlers {
					opts = append(opts, testutils.WithHandler(path, handler))
				}
				p, cleanup := testutils.StartMockProvider("", opts...)
				defer cleanup()
				provider = p
			}

			sessionType := "auth"
			if tc.passwdSession {
				sessionType = "passwd"
			}

			b := newBrokerForTests(t, broker.Config{IssuerURL: provider.URL})
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

			// We need to do a GAM call first to get all the modes.
			_, err := b.GetAuthenticationModes(sessionID, supportedLayouts)
			require.NoError(t, err, "Setup: GetAuthenticationModes should not have returned an error")

			got, err := b.SelectAuthenticationMode(sessionID, tc.modeName)
			if tc.wantErr {
				require.Error(t, err, "SelectAuthenticationMode should have returned an error")
				return
			}
			require.NoError(t, err, "SelectAuthenticationMode should not have returned an error")

			want := testutils.LoadWithUpdateFromGoldenYAML(t, got)
			require.Equal(t, want, got, "SelectAuthenticationMode should have returned the expected layout")
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

		firstMode      string
		firstChallenge string
		firstAuthInfo  map[string]any
		badFirstKey    bool

		customHandlers map[string]testutils.ProviderHandler

		wantSecondCall  bool
		secondChallenge string

		preexistentToken     string
		invalidAuthData      bool
		dontWaitForFirstCall bool
		readOnlyCacheDir     bool
	}{
		"Successfully authenticate user with QRCode+newpassword": {firstChallenge: "-", wantSecondCall: true},
		"Successfully authenticate user with password":           {firstMode: "password", preexistentToken: "valid"},

		"Authenticating with qrcode reacquires token":          {firstChallenge: "-", wantSecondCall: true, preexistentToken: "valid"},
		"Authenticating with password refreshes expired token": {firstMode: "password", preexistentToken: "expired"},
		"Authenticating with password still allowed if server is unreachable": {
			firstMode:        "password",
			preexistentToken: "valid",
			customHandlers: map[string]testutils.ProviderHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Authenticating with password still allowed if token is expired and server is unreachable": {
			firstMode:        "password",
			preexistentToken: "expired",
			customHandlers: map[string]testutils.ProviderHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
		},

		"Error when authentication data is invalid":         {invalidAuthData: true},
		"Error when challenge can not be decrypted":         {firstMode: "password", badFirstKey: true},
		"Error when provided wrong challenge":               {firstMode: "password", preexistentToken: "valid", firstChallenge: "wrongpassword"},
		"Error when can not cache token":                    {firstChallenge: "-", wantSecondCall: true, readOnlyCacheDir: true},
		"Error when IsAuthenticated is ongoing for session": {dontWaitForFirstCall: true, wantSecondCall: true},

		"Error when mode is password and token does not exist": {firstMode: "password"},
		"Error when mode is password but server returns error": {
			firstMode:        "password",
			preexistentToken: "expired",
			customHandlers: map[string]testutils.ProviderHandler{
				"/token": testutils.BadRequestHandler(),
			},
		},
		"Error when mode is password and token is invalid":                {firstMode: "password", preexistentToken: "invalid"},
		"Error when mode is password and cached token can't be refreshed": {firstMode: "password", preexistentToken: "no-refresh"},
		"Error when mode is password and token refresh times out": {firstMode: "password", preexistentToken: "expired",
			customHandlers: map[string]testutils.ProviderHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error when mode is password and can not fetch user info": {firstMode: "password", preexistentToken: "invalid-id"},

		"Error when mode is qrcode and response is invalid": {firstAuthInfo: map[string]any{"response": "not a valid response"}},
		"Error when mode is qrcode and link expires": {
			customHandlers: map[string]testutils.ProviderHandler{
				"/device_auth": testutils.ExpiryDeviceAuthHandler(),
			},
		},
		"Error when mode is qrcode and can not get token": {
			customHandlers: map[string]testutils.ProviderHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Error when mode is qrcode and can not get token due to timeout": {
			customHandlers: map[string]testutils.ProviderHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error when empty challenge is provided for local password": {firstChallenge: "-", wantSecondCall: true, secondChallenge: "-"},
		"Error when mode is newpassword and token is not set": {
			firstMode:     "newpassword",
			firstAuthInfo: map[string]any{"token": nil},
		},
		"Error when mode is newpassword and id token is not set": {
			firstMode:     "newpassword",
			firstAuthInfo: map[string]any{"token": &oauth2.Token{}},
		},
		"Error when mode is newpassword and can not fetch user info": {
			firstMode: "newpassword",
			firstAuthInfo: map[string]any{
				"token": (&oauth2.Token{}).WithExtra(map[string]interface{}{"id_token": "invalid"}),
			},
		},
		// This test case also tests that errors with double quotes are marshaled to JSON correctly.
		"Error when selected username does not match the provider one": {username: "not-matching", firstChallenge: "-"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = "auth"
			}

			outDir := t.TempDir()
			cacheDir := filepath.Join(outDir, "cache")

			err := os.Mkdir(cacheDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			provider := defaultProvider
			if tc.customHandlers != nil {
				var opts []testutils.OptionProvider
				for path, handler := range tc.customHandlers {
					opts = append(opts, testutils.WithHandler(path, handler))
				}
				p, cleanup := testutils.StartMockProvider("", opts...)
				t.Cleanup(cleanup)
				provider = p
			}

			b := newBrokerForTests(t, broker.Config{CachePath: cacheDir, IssuerURL: provider.URL})
			sessionID, key := newSessionForTests(t, b, tc.username, tc.sessionMode)

			if tc.preexistentToken != "" {
				tok := generateCachedInfo(t, tc.preexistentToken, provider.URL)
				err := b.CacheAuthInfo(sessionID, tok, correctPassword)
				require.NoError(t, err, "Setup: SaveToken should not have returned an error")
			}

			var readOnlyCacheCleanup, readOnlyTokenCleanup func()
			if tc.readOnlyCacheDir {
				if tc.preexistentToken != "" {
					readOnlyTokenCleanup = testutils.MakeReadOnly(t, b.TokenPathForSession(sessionID))
					t.Cleanup(readOnlyTokenCleanup)
				}
				readOnlyCacheCleanup = testutils.MakeReadOnly(t, b.CachePath())
				t.Cleanup(readOnlyCacheCleanup)
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
					tc.firstMode = "device_auth"
				}
				updateAuthModes(t, b, sessionID, tc.firstMode)

				if tc.firstAuthInfo != nil {
					for k, v := range tc.firstAuthInfo {
						require.NoError(t, b.SetAuthInfo(sessionID, k, v), "Setup: Failed to set AuthInfo for tests")
					}
				}

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				// Redact variant values from the response
				data = strings.ReplaceAll(data, sessionID, "SESSION_ID")
				data = strings.ReplaceAll(data, filepath.Dir(b.TokenPathForSession(sessionID)), "provider_cache_path")
				data = strings.ReplaceAll(data, provider.URL, "provider_url")
				errStr := strings.ReplaceAll(fmt.Sprintf("%v", err), sessionID, "SESSION_ID")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: errStr}

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

				secondCallDone := make(chan struct{})
				go func() {
					defer close(secondCallDone)

					updateAuthModes(t, b, sessionID, "newpassword")

					access, data, err := b.IsAuthenticated(sessionID, secondAuthData)
					require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

					// Redact variant values from the response
					data = strings.ReplaceAll(data, sessionID, "SESSION_ID")
					data = strings.ReplaceAll(data, filepath.Dir(b.TokenPathForSession(sessionID)), "provider_cache_path")
					data = strings.ReplaceAll(data, provider.URL, "provider_url")
					errStr := strings.ReplaceAll(fmt.Sprintf("%v", err), sessionID, "SESSION_ID")

					got := isAuthenticatedResponse{Access: access, Data: data, Err: errStr}
					out, err := yaml.Marshal(got)
					require.NoError(t, err, "Failed to marshal second response")

					err = os.WriteFile(filepath.Join(outDir, "second_call"), out, 0600)
					require.NoError(t, err, "Failed to write second response")
				}()
				<-secondCallDone
			}
			<-firstCallDone

			// We need to restore some permissions in order to save the golden files.
			if tc.readOnlyCacheDir {
				readOnlyCacheCleanup()
				if tc.preexistentToken != "" {
					readOnlyTokenCleanup()
				}
			}

			// Ensure that the token content is generic to avoid golden file conflicts
			if _, err := os.Stat(b.TokenPathForSession(sessionID)); err == nil {
				err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely an encrypted token"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic token file")
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			if _, err := os.Stat(filepath.Dir(b.TokenPathForSession(sessionID))); err == nil {
				toReplace := strings.ReplaceAll(strings.TrimPrefix(provider.URL, "http://"), ":", "_")
				providerCache := filepath.Dir(b.TokenPathForSession(sessionID))
				newProviderCache := strings.ReplaceAll(providerCache, toReplace, "provider_url")
				err := os.Rename(providerCache, newProviderCache)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename cache directory")
				}
			}

			testutils.CompareTreesWithFiltering(t, outDir, testutils.GoldenPath(t), testutils.Update())
		})
	}
}

// Due to ordering restrictions, this test can not be run in parallel, otherwise the routines would not be ordered as expected.
func TestConcurrentIsAuthenticated(t *testing.T) {
	tests := map[string]struct {
		firstUser  string
		secondUser string

		timeBetween time.Duration
	}{
		"First auth starts and finishes before second":                  {timeBetween: 2 * time.Second},
		"First auth starts first but second finishes first":             {firstUser: "user-timeout-3", timeBetween: time.Second},
		"First auth starts first then second starts and first finishes": {firstUser: "user-timeout-2", secondUser: "user-timeout-3", timeBetween: time.Second},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			outDir := t.TempDir()
			cacheDir := filepath.Join(outDir, "cache")
			err := os.Mkdir(cacheDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")
			b := newBrokerForTests(t, broker.Config{CachePath: cacheDir, IssuerURL: defaultProvider.URL})

			if tc.firstUser == "" {
				tc.firstUser = "user-timeout-0"
			}
			if tc.secondUser == "" {
				tc.secondUser = "user-timeout-1"
			}

			firstSession, firstKey := newSessionForTests(t, b, tc.firstUser, "")
			tok := generateCachedInfo(t, tc.firstUser, defaultProvider.URL)
			err = b.CacheAuthInfo(firstSession, tok, "password")
			require.NoError(t, err, "Setup: SaveToken should not have returned an error")

			secondSession, secondKey := newSessionForTests(t, b, tc.secondUser, "")
			tok = generateCachedInfo(t, tc.secondUser, defaultProvider.URL)
			err = b.CacheAuthInfo(secondSession, tok, "password")
			require.NoError(t, err, "Setup: SaveToken should not have returned an error")

			firstCallDone := make(chan struct{})
			//nolint:dupl // This is not a duplicate, just a very similar set of calls.
			go func() {
				t.Logf("%s: First auth starting", t.Name())
				defer close(firstCallDone)

				updateAuthModes(t, b, firstSession, "password")

				authData := `{"challenge":"` + encryptChallenge(t, "password", firstKey) + `"}`

				access, data, err := b.IsAuthenticated(firstSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				// Redact variant values from the response
				data = strings.ReplaceAll(data, firstSession, "SESSION_ID")
				data = strings.ReplaceAll(data, filepath.Dir(b.TokenPathForSession(firstSession)), "provider_cache_path")
				data = strings.ReplaceAll(data, defaultProvider.URL, "provider_url")
				errStr := strings.ReplaceAll(fmt.Sprintf("%v", err), firstSession, "SESSION_ID")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: errStr}

				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_auth"), out, 0600)
				require.NoError(t, err, "Failed to write first response")

				t.Logf("%s: First auth done", t.Name())
			}()

			time.Sleep(tc.timeBetween)

			secondCallDone := make(chan struct{})
			//nolint:dupl // This is not a duplicate, just a very similar set of calls.
			go func() {
				t.Logf("%s: Second auth starting", t.Name())
				defer close(secondCallDone)

				updateAuthModes(t, b, secondSession, "password")

				authData := `{"challenge":"` + encryptChallenge(t, "password", secondKey) + `"}`

				access, data, err := b.IsAuthenticated(secondSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				// Redact variant values from the response
				data = strings.ReplaceAll(data, secondSession, "SESSION_ID")
				data = strings.ReplaceAll(data, filepath.Dir(b.TokenPathForSession(secondSession)), "provider_cache_path")
				data = strings.ReplaceAll(data, defaultProvider.URL, "provider_url")
				errStr := strings.ReplaceAll(fmt.Sprintf("%v", err), secondSession, "SESSION_ID")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: errStr}

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
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			if _, err := os.Stat(filepath.Dir(b.TokenPathForSession(firstSession))); err == nil {
				toReplace := strings.ReplaceAll(strings.TrimPrefix(defaultProvider.URL, "http://"), ":", "_")
				providerCache := filepath.Dir(b.TokenPathForSession(firstSession))
				newProviderCache := strings.ReplaceAll(providerCache, toReplace, "provider_url")
				err := os.Rename(providerCache, newProviderCache)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename cache directory")
				}
			}
			testutils.CompareTreesWithFiltering(t, outDir, testutils.GoldenPath(t), testutils.Update())
		})
	}
}

func TestFetchUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username  string
		userToken string

		emptyHomeDir bool
		emptyGroups  bool
		wantGroupErr bool
		wantErr      bool
	}{
		"Successfully fetch user info with groups":                         {},
		"Successfully fetch user info without groups":                      {emptyGroups: true},
		"Successfully fetch user info with default home when not provided": {emptyHomeDir: true},
		"Successfully fetch user info ignoring different casing in name":   {userToken: "uppercased-name"},

		"Error when token can not be validated":                   {userToken: "invalid", wantErr: true},
		"Error when ID token claims are invalid":                  {userToken: "invalid-id", wantErr: true},
		"Error when username is not configured":                   {userToken: "no-name", wantErr: true},
		"Error when username is different than the requested one": {userToken: "other-name", wantErr: true},
		"Error when getting user groups":                          {wantGroupErr: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			homeDirPath := "/home/userInfoTests/"
			if tc.emptyHomeDir {
				homeDirPath = ""
			}

			brokerCfg := broker.Config{
				IssuerURL:   defaultProvider.URL,
				ClientID:    "test-client-id",
				CachePath:   t.TempDir(),
				HomeBaseDir: homeDirPath,
			}

			mockInfoer := &testutils.MockProviderInfoer{
				GroupsErr: tc.wantGroupErr,
				Groups: []info.Group{
					{Name: "remote-group", UGID: "12345"},
					{Name: "linux-local-group", UGID: ""},
				},
			}
			if tc.emptyGroups {
				mockInfoer.Groups = []info.Group{}
			}

			b, err := broker.New(brokerCfg, broker.WithSkipSignatureCheck(), broker.WithCustomProviderInfo(mockInfoer))
			require.NoError(t, err, "Setup: New should not have returned an error")

			if tc.username == "" {
				tc.username = "test-user@email.com"
			}
			if tc.userToken == "" {
				tc.userToken = "valid"
			}

			sessionID, _, err := b.NewSession(tc.username, "lang", "auth")
			require.NoError(t, err, "Setup: Failed to create session for the tests")

			cachedInfo := generateCachedInfo(t, tc.userToken, defaultProvider.URL)
			if cachedInfo == nil {
				cachedInfo = &broker.AuthCachedInfo{}
			}

			got, err := b.FetchUserInfo(sessionID, cachedInfo)
			if tc.wantErr {
				require.Error(t, err, "FetchUserInfo should have returned an error")
				return
			}
			require.NoError(t, err, "FetchUserInfo should not have returned an error")

			want := testutils.LoadWithUpdateFromGoldenYAML(t, got)
			require.Equal(t, want, got, "FetchUserInfo should have returned the expected value")
		})
	}
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	provider, cleanup := testutils.StartMockProvider("", testutils.WithHandler("/token", testutils.HangingHandler(3*time.Second)))
	t.Cleanup(cleanup)

	b := newBrokerForTests(t, broker.Config{IssuerURL: provider.URL})
	sessionID, _ := newSessionForTests(t, b, "", "")

	updateAuthModes(t, b, sessionID, "device_auth")

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

	b := newBrokerForTests(t, broker.Config{IssuerURL: defaultProvider.URL})

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
		"Successfully allow username with matching allowed suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"}},
		"Successfully allow username that matches at least one allowed suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
		},
		"Return userinfo with correct homedir after precheck": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"},
			homePrefix:      "/home/allowed/",
		},

		"Error when username does not match allowed suffix": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@allowed"},
			wantErr:         true,
		},
		"Error when username does not match any of the allowed suffixes": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
			wantErr:         true,
		},
		"Error when no allowed suffixes are provided": {
			username: "user@allowed",
			wantErr:  true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newBrokerForTests(t, broker.Config{IssuerURL: defaultProvider.URL, AllowedSSHSuffixes: tc.allowedSuffixes, HomeBaseDir: tc.homePrefix})

			got, err := b.UserPreCheck(tc.username)
			if tc.wantErr {
				require.Error(t, err, "UserPreCheck should have returned an error")
				return
			}
			require.NoError(t, err, "UserPreCheck should not have returned an error")

			want := testutils.LoadWithUpdateFromGolden(t, got)
			require.Equal(t, want, got, "UserPreCheck should have returned the expected value")
		})
	}
}

func TestMain(m *testing.M) {
	testutils.InstallUpdateFlag()
	flag.Parse()

	server, cleanup := testutils.StartMockProvider("")
	defer cleanup()

	defaultProvider = server

	m.Run()
}
