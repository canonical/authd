package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

var defaultIssuerURL string

func newTrackedMFAFlowState(release func()) *himmelblau.MFAFlowState {
	flow := &himmelblau.MFAFlowState{}
	releaseField := reflect.ValueOf(flow).Elem().FieldByName("release")
	//nolint:gosec // G103: unsafe pointer required to set unexported field for testing purposes only.
	reflect.NewAt(releaseField.Type(), unsafe.Pointer(releaseField.UnsafeAddr())).Elem().Set(reflect.ValueOf(release))
	return flow
}

type mockEntraPasswordProvider struct {
	*testutils.MockProvider
	flowState             *himmelblau.MFAFlowState
	challengeInfo         *himmelblau.MFAChallengeInfo
	mfaTokenResult        *oauth2.Token
	initErr               error
	recordedPollAttempts  []int
	recordedChallengeData []string
	refreshResult         *oauth2.Token // returned by RefreshEntraPasswordToken (defaults to a rotated token)
	refreshErr            error         // when set, RefreshEntraPasswordToken returns it (e.g. AADSTS50057)
	userDisabledErrorCode string        // when set, IsUserDisabledError matches an *oauth2.RetrieveError with this code
}

type mockProviderWithEntraModes struct {
	*testutils.MockProvider
}

func (p *mockProviderWithEntraModes) SupportedOIDCAuthModes() []string {
	return []string{authmodes.Device, authmodes.DeviceQr, authmodes.EntraPassword}
}

type mockGrantRevokedProvider struct {
	*mockProviderWithEntraModes
}

func (p *mockGrantRevokedProvider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	return err != nil && err.ErrorCode == "invalid_grant" && strings.HasPrefix(err.ErrorDescription, "AADSTS50173:")
}

var mockDeviceRegistrationData = []byte(`{"device_id":"test-device-id","cert_key":"Y2VydA==","transport_key":"dHJhbnNwb3J0","auth_value":"test-auth-value","tpm_machine_key":"dHBtLW1hY2hpbmUta2V5"}`)

func (p *mockEntraPasswordProvider) InitiateEntraPasswordAuth(_ context.Context, _, _ string, _, _ string, _ []byte, _ bool) (*himmelblau.MFAFlowState, *himmelblau.MFAChallengeInfo, error) {
	if p.initErr != nil {
		return nil, nil, p.initErr
	}
	return p.flowState, p.challengeInfo, nil
}

func (p *mockEntraPasswordProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, authData string, pollAttempt int, _ []byte) (*oauth2.Token, error) {
	p.recordedPollAttempts = append(p.recordedPollAttempts, pollAttempt)
	p.recordedChallengeData = append(p.recordedChallengeData, authData)
	if p.mfaTokenResult == nil {
		return nil, fmt.Errorf("missing MFA token result")
	}
	return p.mfaTokenResult, nil
}

func (p *mockEntraPasswordProvider) RefreshEntraPasswordToken(_ context.Context, _, _ string) (*oauth2.Token, error) {
	if p.refreshErr != nil {
		return nil, p.refreshErr
	}
	if p.refreshResult != nil {
		return p.refreshResult, nil
	}
	// Default: an active user — a successful refresh that rotates the refresh token.
	return &oauth2.Token{AccessToken: "mock-access-token", RefreshToken: "mock-rotated-refresh-token"}, nil
}

// IsUserDisabledError lets the mock stand in as a providers.UserDisabledChecker so
// broker tests can exercise the refresh-rejection classification. It matches on a
// sentinel error code, mirroring testutils.MockUserDisabledCheckerProvider; the real
// AADSTS50057 detection is covered by the provider-level tests.
func (p *mockEntraPasswordProvider) IsUserDisabledError(err *oauth2.RetrieveError) bool {
	return p.userDisabledErrorCode != "" && err != nil && err.ErrorCode == p.userDisabledErrorCode
}

func (p *mockEntraPasswordProvider) IsTokenForDeviceRegistration(token *oauth2.Token) (bool, error) {
	if token == nil {
		return false, errors.New("token is nil")
	}
	isForDeviceRegistration, ok := token.Extra(testutils.IsForDeviceRegistrationClaim).(bool)
	if !ok {
		return false, fmt.Errorf("token does not contain %q claim", testutils.IsForDeviceRegistrationClaim)
	}
	return isForDeviceRegistration, nil
}

func (p *mockEntraPasswordProvider) GetExtraFields(token *oauth2.Token) map[string]interface{} {
	extraFields := map[string]interface{}{}
	if isForDeviceRegistration, ok := token.Extra(testutils.IsForDeviceRegistrationClaim).(bool); ok {
		extraFields[testutils.IsForDeviceRegistrationClaim] = isForDeviceRegistration
	}
	if len(extraFields) == 0 {
		return nil
	}
	return extraFields
}

func (p *mockEntraPasswordProvider) MaybeRegisterDevice(_ context.Context, _ *oauth2.Token, _ string, _ string, oldData []byte) ([]byte, func(), error) {
	if len(oldData) > 0 {
		return oldData, func() {}, nil
	}
	return mockDeviceRegistrationData, func() {}, nil
}

// mockMFADeniedProvider simulates MFA push notification being denied by the user.
type mockMFADeniedProvider struct {
	*mockEntraPasswordProvider
}

// mockDeviceRegistrationFailProvider simulates a first-time login where device
// registration fails at the network level (e.g. no connectivity to
// enterpriseregistration.windows.net).
type mockDeviceRegistrationFailProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockDeviceRegistrationFailProvider) MaybeRegisterDevice(_ context.Context, _ *oauth2.Token, _ string, _ string, oldData []byte) ([]byte, func(), error) {
	if len(oldData) > 0 {
		// Re-use existing registration — failure is only on first registration.
		return oldData, func() {}, nil
	}
	return nil, func() {}, fmt.Errorf("failed to enroll device: Request failed: error sending request for url (https://enterpriseregistration.windows.net/EnrollmentServer/device/?api-version=2.0)")
}

func (p *mockMFADeniedProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	// Simulate the user denying the push notification: ACQUIRE_TOKEN_FAILED without AADSTS.
	return nil, &himmelblau.MFAInitError{Category: himmelblau.MFAErrorDenied, Message: "MFA denied by user"}
}

// mockMFATimeoutProvider simulates MFA poll continuing until max attempts are exhausted.
type mockMFATimeoutProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockMFATimeoutProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	// Always return poll-continue so the loop exhausts max attempts.
	return nil, &himmelblau.MFAInitError{Category: himmelblau.MFAErrorPollContinue, Message: "MFA poll continue"}
}

// mockMFAWrongCodeThenSuccessProvider simulates an incorrect or expired
// one-time code on the first code submission followed by a correct code on the
// second. libhimmelblau reports a wrong code as a generic GeneralFailure with an
// "AuthResponse indicates failure: ..." message (the code-submission path drops
// the server's retry flag), while leaving the flow intact. newMFAInitError
// promotes that to MFAErrorRetryableCode, which is what production consumers see.
type mockMFAWrongCodeThenSuccessProvider struct {
	*mockEntraPasswordProvider
	codeAttempts int
}

func (p *mockMFAWrongCodeThenSuccessProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, authData string, _ int, _ []byte) (*oauth2.Token, error) {
	p.recordedChallengeData = append(p.recordedChallengeData, authData)
	p.codeAttempts++
	if p.codeAttempts == 1 {
		return nil, &himmelblau.MFAInitError{
			Category: himmelblau.MFAErrorRetryableCode,
			Message:  "AuthResponse indicates failure: Your sign-in was blocked by a One-Time Passcode mismatch.",
		}
	}
	return p.mfaTokenResult, nil
}

// mockMFANilTokenProvider violates the provider contract by returning (nil, nil)
// from AcquireTokenByMFAFlow, exercising the broker's defensive nil-token guard
// (a misbehaving provider must deny, not panic the broker).
type mockMFANilTokenProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockMFANilTokenProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	return nil, nil
}

// newMFATokenResult builds an oauth2.Token mirroring what
// himmelblau.AcquireTokenByMFAFlow returns in production: the user's
// preferred_username/sub/name carried as top-level token extras, recovered
// from the native MFA UserToken. finishEntraAuth relies on these extras since
// the MFA access token cannot be used against the OIDC UserInfo endpoint. The
// sub/name values match the claims set by generateCachedInfo.
func newMFATokenResult(t *oauth2.Token) *oauth2.Token {
	return t.WithExtra(map[string]any{
		"preferred_username": "test-user@email.com",
		"sub":                "saved-user-id",
		"name":               "test-user",
	})
}

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
				tc.issuer = defaultIssuerURL
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
			b, err := broker.New(*bCfg, broker.LatestAPIVersion)
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
		username                     string
		emptyUsername                bool
		issuerURL                    string
		customHandlers               map[string]testutils.EndpointHandler
		forceAccessCheckWithProvider bool

		wantOffline bool
		wantErr     bool
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
		"Creates_new_session_with_schemeless_issuer_URL": {
			issuerURL:   "example.com",
			wantOffline: true,
		},

		"Error_when_provider_authentication_is_forced_and_provider_is_not_available": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			forceAccessCheckWithProvider: true,
			wantErr:                      true,
		},
		"Error_when_username_is_empty": {
			emptyUsername: true,
			wantErr:       true,
		},
		"Error_when_user_directory_path_could_not_be_derived": {
			username: "invalid/../user",
			wantErr:  true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				customHandlers:               tc.customHandlers,
				forceAccessCheckWithProvider: tc.forceAccessCheckWithProvider,
				issuerURL:                    tc.issuerURL,
			})

			username := tc.username
			if tc.emptyUsername {
				username = ""
			} else if username == "" {
				username = "test-user"
			}

			id, _, err := b.NewSession(username, "lang", sessionmode.Login)
			t.Logf("NewSession returned id: %q, err: %v", id, err)
			if tc.wantErr {
				require.Error(t, err, "NewSession should have returned an error")
				return
			}
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

		providerAddress                    string
		token                              *tokenOptions
		noPasswordFile                     bool
		nextAuthMode                       string
		unavailableProvider                bool
		deviceAuthUnsupported              bool
		registerDevice                     bool
		providerSupportsDeviceRegistration bool

		wantErr   bool
		wantModes []string
	}{
		// === Authentication session ===
		"Get_only_device_auth_qr_if_there_is_no_token": {
			token:     nil,
			wantModes: []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_token_exists": {
			token:     &tokenOptions{},
			wantModes: []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_token_is_invalid": {
			token:     &tokenOptions{invalid: true},
			wantModes: []string{authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_there_is_no_password_file": {
			token:          &tokenOptions{},
			noPasswordFile: true,
			wantModes:      []string{authmodes.DeviceQr},
		},

		// --- Next auth mode ---
		"Get_only_newpassword_if_next_auth_mode_is_newpassword": {
			nextAuthMode: authmodes.NewPassword,
			wantModes:    []string{authmodes.NewPassword},
		},
		"Get_only_device_auth_qr_if_next_auth_mode_is_device_qr": {
			nextAuthMode: authmodes.DeviceQr,
			wantModes:    []string{authmodes.DeviceQr},
		},

		// --- Device registration ---
		"Get_password_and_device_auth_qr_if_device_should_be_registered_and_token_is_for_device_registration": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_device_should_be_registered_and_token_is_not_for_device_registration": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_device_should_be_registered_and_token_is_not_for_device_registration_and_provider_does_not_support_it": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: false,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_device_should_not_be_registered_and_token_is_for_device_registration": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			wantModes:                          []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_device_should_not_be_registered_and_token_is_not_for_device_registration": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_token_is_not_for_device_registration_but_provider_does_not_support_it": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: false,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		// Note: We don't care about the weird case that the token is for device registration but the provider doesn't
		//       support it, because that never happens (providers which don't support device registration always return
		//       false for IsTokenForDeviceRegistration).

		"Get_only_password_if_device_should_be_registered_and_token_is_not_for_device_registration_but_provider_is_not_available": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			unavailableProvider:                true,
			// TODO: Automatically set providerAddress if unavailableProvider or deviceAuthUnsupported is set
			providerAddress: "127.0.0.1:31308",
			wantModes:       []string{authmodes.Password},
		},
		"Get_only_password_if_device_should_not_be_registered_and_token_is_for_device_registration_but_provider_is_not_available": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			unavailableProvider:                true,
			providerAddress:                    "127.0.0.1:31309",
			wantModes:                          []string{authmodes.Password},
		},

		"Get_only_password_if_token_exists_and_provider_is_not_available": {
			token:               &tokenOptions{},
			providerAddress:     "127.0.0.1:31310",
			unavailableProvider: true,
			wantModes:           []string{authmodes.Password},
		},
		"Get_only_password_if_token_exists_and_provider_does_not_support_device_auth_qr": {
			token:                 &tokenOptions{},
			providerAddress:       "127.0.0.1:31311",
			deviceAuthUnsupported: true,
			wantModes:             []string{authmodes.Password},
		},
		"Get_only_device_auth_if_token_exists_but_checking_if_it_is_for_device_registration_fails": {
			token:                              &tokenOptions{noIsForDeviceRegistration: true},
			providerSupportsDeviceRegistration: true,
			wantModes:                          []string{authmodes.DeviceQr},
		},

		// === Change password session ===
		"Get_only_password_if_token_exists_and_session_is_for_changing_password": {
			sessionMode: sessionmode.ChangePassword,
			token:       &tokenOptions{},
			wantModes:   []string{authmodes.Password},
		},
		"Get_only_newpassword_if_session_is_for changing_password_and_next_auth_mode_is_newpassword": {
			sessionMode:  sessionmode.ChangePassword,
			token:        &tokenOptions{},
			nextAuthMode: authmodes.NewPassword,
			wantModes:    []string{authmodes.NewPassword},
		},
		"Get_only_password_if_token_exists_and_session_mode_is_the_old_passwd_value": {
			sessionMode: sessionmode.ChangePasswordOld,
			token:       &tokenOptions{},
			wantModes:   []string{authmodes.Password},
		},

		// === Errors ===
		// --- General errors ---
		"Error_if_there_is_no_session": {
			sessionID: "-",
			wantErr:   true,
		},
		"Error_if_no_authentication_mode_is_supported": {
			providerAddress:       "127.0.0.1:31312",
			deviceAuthUnsupported: true,
			wantErr:               true,
		},
		"Error_if_expecting_device_auth_qr_but_not_supported": {
			supportedLayouts: []string{"qrcode-without-wait"},
			wantErr:          true,
		},
		"Error_if_expecting_device_auth_but_not_supported": {
			supportedLayouts: []string{"qrcode-without-wait-and-qrcode"},
			wantErr:          true,
		},
		"Error_if_expecting_newpassword_but_not_supported": {
			supportedLayouts: []string{"newpassword-without-entry"},
			wantErr:          true,
		},
		"Error_if_expecting_password_but_not_supported": {
			supportedLayouts: []string{"form-without-entry"},
			wantErr:          true,
		},
		"Error_if_next_auth_mode_is_invalid": {
			nextAuthMode: "invalid",
			wantErr:      true,
		},

		// --- Change password session errors ---
		"Error_if_session_is_for_changing_password_but_password_file_does_not_exist": {
			sessionMode:    sessionmode.ChangePassword,
			noPasswordFile: true,
			wantErr:        true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = sessionmode.Login
			}

			cfg := &brokerForTestConfig{
				registerDevice:             tc.registerDevice,
				supportsDeviceRegistration: tc.providerSupportsDeviceRegistration,
			}
			if tc.providerAddress == "" {
				// Use the default provider URL if no address is provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.listenAddress = tc.providerAddress

				const wellKnown = "/.well-known/openid-configuration"
				if tc.deviceAuthUnsupported {
					cfg.customHandlers = map[string]testutils.EndpointHandler{
						wellKnown: testutils.OpenIDHandlerWithNoDeviceEndpoint("http://" + tc.providerAddress),
					}
				}
				if tc.unavailableProvider {
					cfg.customHandlers = map[string]testutils.EndpointHandler{
						wellKnown: testutils.UnavailableHandler(),
					}
				}
			}
			b := newBrokerForTests(t, cfg)

			sessionID, _ := newSessionForTests(t, b, "", tc.sessionMode)
			if tc.sessionID == "-" {
				sessionID = ""
			}
			if tc.token != nil {
				generateAndStoreCachedInfo(t, *tc.token, b.TokenPathForSession(sessionID))
			}
			if !tc.noPasswordFile && sessionID != "" {
				err := password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}
			if tc.nextAuthMode != "" {
				b.SetNextAuthModes(sessionID, []string{tc.nextAuthMode})
			}

			if tc.supportedLayouts == nil {
				tc.supportedLayouts = []string{"form", "qrcode", "newpassword"}
			}
			var layouts []map[string]string
			for _, layout := range tc.supportedLayouts {
				layouts = append(layouts, supportedUILayouts[layout])
			}

			modes, err := b.GetAuthenticationModes(sessionID, layouts)
			if tc.wantErr {
				require.Error(t, err, "GetAuthenticationModes should have returned an error")
				return
			}
			require.NoError(t, err, "GetAuthenticationModes should not have returned an error")

			var modeIDs []string
			for _, mode := range modes {
				id, exists := mode["id"]
				require.True(t, exists, "Each mode should have an 'id' field. Mode: %v", mode)
				modeIDs = append(modeIDs, id)
			}
			require.Equal(t, tc.wantModes, modeIDs, "GetAuthenticationModes should have returned the expected modes")

			golden.CheckOrUpdateYAML(t, modes)
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
		nextAuthMode     string
		passwdSession    bool
		customHandlers   map[string]testutils.EndpointHandler
		supportedLayouts []map[string]string

		wantErr bool
	}{
		"Successfully_select_password":       {modeName: authmodes.Password, tokenExists: true},
		"Successfully_select_device_auth_qr": {modeName: authmodes.DeviceQr},
		"Successfully_select_device_auth":    {supportedLayouts: supportedLayoutsWithoutQrCode, modeName: authmodes.Device},
		"Successfully_select_newpassword":    {modeName: authmodes.NewPassword, nextAuthMode: authmodes.NewPassword},

		"Selected_newpassword_shows_correct_label_in_passwd_session": {modeName: authmodes.NewPassword, passwdSession: true, tokenExists: true, nextAuthMode: authmodes.NewPassword},

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

			cfg := &brokerForTestConfig{}
			if tc.customHandlers == nil {
				// Use the default provider URL if no custom handlers are provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.customHandlers = tc.customHandlers
			}
			b := newBrokerForTests(t, cfg)

			sessionType := sessionmode.Login
			if tc.passwdSession {
				sessionType = sessionmode.ChangePassword
			}
			sessionID, _ := newSessionForTests(t, b, "", sessionType)

			if tc.tokenExists {
				generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
				err := password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}
			if tc.nextAuthMode != "" {
				b.SetNextAuthModes(sessionID, []string{tc.nextAuthMode})
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

			golden.CheckOrUpdateYAML(t, got)
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
		sessionMode                        string
		sessionOffline                     bool
		username                           string
		forceAccessCheckWithProvider       bool
		userDoesNotBecomeOwner             bool
		allUsersAllowed                    bool
		extraGroups                        []string
		ownerExtraGroups                   []string
		providerSupportsDeviceRegistration bool
		registerDevice                     bool
		requireNameClaimOnInitialAuth      bool
		providerSupportsMetadata           bool
		metadataGetErr                     error
		providerSupportsUserDisabledCheck  bool
		userDisabledErrorCode              string
		providerHasNoGroupFetcher          bool

		firstMode                string
		firstSecret              string
		badFirstKey              bool
		getGroupsFails           bool
		useOldNameForSecretField bool
		groupsReturnedByProvider []info.Group
		getGroupsFunc            func() ([]info.Group, error)

		customHandlers      map[string]testutils.EndpointHandler
		address             string
		tokenHandlerOptions *testutils.TokenHandlerOptions

		wantSecondCall bool
		secondMode     string
		secondSecret   string

		token                *tokenOptions
		invalidAuthData      bool
		dontWaitForFirstCall bool
		readOnlyDataDir      bool
		wantGroups           []info.Group
		wantGecos            string
		wantNextAuthModes    []string
		wantOffline          bool
	}{
		"Successfully_authenticate_user_with_device_auth_and_newpassword": {firstSecret: "-", wantSecondCall: true},
		"Successfully_authenticate_user_with_password":                    {firstMode: authmodes.Password, token: &tokenOptions{}},
		"Successfully_authenticate_with_device_auth_when_provider_uses_thin_id_token": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				// Remove "must-have-claim" from the ID token.
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				// Provide "must-have-claim" via the userinfo endpoint.
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{
					"must-have-claim": "present",
				}),
			},
		},

		"Authenticating_with_qrcode_reacquires_token":          {firstSecret: "-", wantSecondCall: true, token: &tokenOptions{}},
		"Authenticating_with_password_refreshes_expired_token": {firstMode: authmodes.Password, token: &tokenOptions{expired: true}},
		"Authenticating_with_password_keeps_old_gecos_if_name_claim_missing_on_refresh_for_name_claim_provider": {
			firstMode:                     authmodes.Password,
			token:                         &tokenOptions{expired: true, gecos: "Saved Name"},
			requireNameClaimOnInitialAuth: true,
			wantGecos:                     "Saved Name",
			customHandlers:                map[string]testutils.EndpointHandler{},
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"name"},
			},
		},
		"Successfully_authenticate_with_name_claim_provider_when_name_is_only_in_userinfo": {
			firstSecret:                   "-",
			wantSecondCall:                true,
			requireNameClaimOnInitialAuth: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"name"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{
					"must-have-claim": "present",
					"name":            "Full Name from UserInfo",
				}),
			},
		},
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
			firstSecret:    "-",
			wantSecondCall: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31313", nil),
			},
			address: "127.0.0.1:31313",
		},
		"Authenticating_with_password_refreshes_groups": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "refreshed-group"}},
			wantGroups:               []info.Group{{Name: "refreshed-group"}},
		},
		"Authenticating_with_password_keeps_old_groups_if_fetching_groups_fails": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{groups: []info.Group{{Name: "old-group"}}},
			getGroupsFails: true,
			wantGroups:     []info.Group{{Name: "old-group"}},
		},
		"Authenticating_with_password_keeps_old_groups_if_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{groups: []info.Group{{Name: "old-group"}}},
			sessionOffline: true,
			wantGroups:     []info.Group{{Name: "old-group"}},
		},
		"Authenticating_when_the_auth_data_secret_field_uses_the_old_name": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			useOldNameForSecretField: true,
		},
		"Authenticating_to_change_password_still_allowed_if_fetching_groups_fails": {
			sessionMode:       sessionmode.ChangePassword,
			firstMode:         authmodes.Password,
			wantNextAuthModes: []string{authmodes.NewPassword},
			token:             &tokenOptions{noUserInfo: true},
			getGroupsFails:    true,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenExpired: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_due_to_inactivity_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenInactiveExpired: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_due_to_ca_sign_in_frequency_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenStale: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_no_refresh_token_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{noRefreshToken: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_still_allowed_if_no_refresh_token_and_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{noRefreshToken: true, groups: []info.Group{{Name: "old-group"}}},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			wantGroups: []info.Group{{Name: "old-group"}},
		},
		"Authenticating_with_password_when_provider_authentication_is_forced": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{},
			forceAccessCheckWithProvider: true,
		},
		// With force_access_check_with_provider set and a cached Entra password +
		// MFA token, a group-fetch failure no longer denies. The live provider check
		// is the token refresh (the Broker App token is refreshed as a public client;
		// see the dedicated refresh tests TestIsAuthenticatedPasswordEntraTokenRefresh*),
		// so — as for every other cached token — the group fetch falls back to cached
		// groups, the same as the device-auth flow.
		"Forced_check_with_entra_password_token_uses_cached_groups_when_group_fetch_fails": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{obtainedViaEntraPasswordAuth: true, groups: []info.Group{{Name: "old-group"}}},
			forceAccessCheckWithProvider: true,
			getGroupsFails:               true,
			wantGroups:                   []info.Group{{Name: "old-group"}},
		},
		"Extra_groups_configured": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			extraGroups:              []string{"extra-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}, {Name: "extra-group"}},
		},
		"Owner_extra_groups_configured": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			ownerExtraGroups:         []string{"owner-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}, {Name: "owner-group"}},
		},
		"Extra_and_owner_extra_groups_configured_with_existing_extra_group_in_cached_user_info": {
			firstMode: authmodes.Password,
			token: &tokenOptions{groups: []info.Group{
				{Name: "remote-group"},
				{Name: "extra-group"},
			}},
			sessionOffline:   true,
			extraGroups:      []string{"extra-group", "other-extra-group"},
			ownerExtraGroups: []string{"owner-group"},
			wantGroups:       []info.Group{{Name: "remote-group"}, {Name: "extra-group"}, {Name: "other-extra-group"}, {Name: "owner-group"}},
		},
		"Extra_and_owner_extra_groups_configured_but_already_in_cached_user_info": {
			firstMode: authmodes.Password,
			token: &tokenOptions{groups: []info.Group{
				{Name: "remote-group"},
				{Name: "extra-group"},
				{Name: "owner-group"},
			}},
			sessionOffline:   true,
			extraGroups:      []string{"extra-group"},
			ownerExtraGroups: []string{"owner-group"},
			wantGroups:       []info.Group{{Name: "remote-group"}, {Name: "extra-group"}, {Name: "owner-group"}},
		},
		"Owner_extra_groups_configured_but_user_does_not_become_owner": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			userDoesNotBecomeOwner:   true,
			allUsersAllowed:          true,
			ownerExtraGroups:         []string{"owner-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}},
		},
		"Authenticating_with_device_auth_when_provider_supports_device_registration": {
			firstSecret:                        "-",
			wantSecondCall:                     true,
			providerSupportsDeviceRegistration: true,
			registerDevice:                     true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31314", &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"aud": consts.MicrosoftBrokerAppID},
					},
				}),
			},
			address: "127.0.0.1:31314",
		},
		"Authenticating_with_password_when_provider_supports_device_registration": {
			firstMode:                          authmodes.Password,
			token:                              &tokenOptions{},
			providerSupportsDeviceRegistration: true,
			registerDevice:                     true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31315", &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"aud": consts.MicrosoftBrokerAppID},
					},
				}),
			},
			address: "127.0.0.1:31315",
		},

		"Error_when_authentication_data_is_invalid":         {invalidAuthData: true},
		"Error_when_secret_can_not_be_decrypted":            {firstMode: authmodes.Password, badFirstKey: true},
		"Error_when_provided_wrong_secret":                  {firstMode: authmodes.Password, token: &tokenOptions{}, firstSecret: "wrongpassword"},
		"Error_when_can_not_cache_token":                    {firstSecret: "-", wantSecondCall: true, readOnlyDataDir: true},
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
		"Error_when_mode_is_password_and_no_refresh_token":       {firstMode: authmodes.Password, token: &tokenOptions{noRefreshToken: true}},
		"Error_when_token_is_expired_and_refreshing_token_fails": {firstMode: authmodes.Password, token: &tokenOptions{expired: true, noRefreshToken: true}},
		"Authenticating_with_password_skips_token_refresh_network_error": {firstMode: authmodes.Password, token: &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantOffline: true,
		},
		"Error_when_mode_is_password_and_token_refresh_times_out_with_forced_provider_auth": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{expired: true},
			forceAccessCheckWithProvider: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},

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
		"Error_when_empty_secret_is_provided_for_local_password":  {firstSecret: "-", wantSecondCall: true, secondSecret: "-"},
		"Error_when_mode_is_newpassword_and_session_has_no_token": {firstMode: authmodes.NewPassword},
		// This test case also tests that errors with double quotes are marshaled to JSON correctly.
		"Error_when_selected_username_does_not_match_the_provider_one": {username: "not-matching", firstSecret: "-"},
		"Error_when_user_is_disabled_and_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{userIsDisabled: true},
			sessionOffline: true,
		},
		"Error_when_device_is_disabled_and_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{deviceIsDisabled: true},
			sessionOffline: true,
		},
		// disabled user/device must be denied even when the session transitions from online to offline mid-auth (network error during token refresh).
		"Error_when_user_is_disabled_and_session_transitions_to_offline_due_to_network_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{userIsDisabled: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_device_is_disabled_and_session_transitions_to_offline_due_to_network_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{deviceIsDisabled: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_mode_is_invalid": {firstMode: "invalid"},
		"Error_when_thin_id_token_and_userinfo_endpoint_is_unavailable": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UnavailableHandler(),
			},
		},
		"Error_when_thin_id_token_and_userinfo_does_not_have_must_have_claim": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{}),
			},
		},

		// MetadataProvider: the broker should call GetExtraFields and GetMetadata when the
		// provider implements MetadataProvider.
		"Successfully_authenticate_with_device_auth_when_provider_supports_metadata": {
			firstSecret:              "-",
			wantSecondCall:           true,
			providerSupportsMetadata: true,
		},
		"Error_when_device_auth_metadata_provider_fails_to_get_metadata": {
			firstSecret:              "-",
			wantSecondCall:           true,
			providerSupportsMetadata: true,
			metadataGetErr:           errors.New("metadata unavailable"),
		},
		"Authenticating_with_password_when_provider_supports_metadata": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			providerSupportsMetadata: true,
		},

		// NoGroupFetcher: when the provider does not implement GroupFetcher, getGroups
		// returns nil and the user is authenticated without remote groups.
		"Authenticating_with_password_when_provider_has_no_group_fetcher": {
			firstMode:                 authmodes.Password,
			token:                     &tokenOptions{},
			providerHasNoGroupFetcher: true,
			wantGroups:                []info.Group{},
		},

		// UserDisabledChecker: when a token refresh fails with a provider-specific
		// "user disabled" error code, login is denied and the token is marked disabled.
		"Error_when_user_is_disabled_according_to_user_disabled_checker": {
			firstMode:                         authmodes.Password,
			token:                             &tokenOptions{},
			providerSupportsUserDisabledCheck: true,
			userDisabledErrorCode:             "user_disabled",
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.ErrorResponseHandler(http.StatusBadRequest,
					`{"error":"user_disabled","error_description":"User account is disabled"}`),
			},
		},

		// getGroups error cases: test that errors returned from the GroupFetcher are
		// handled correctly.
		"Error_when_getgroups_returns_device_disabled_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, providerErrors.ErrDeviceDisabled
			},
		},
		"Error_when_getgroups_returns_invalid_redirect_uri_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, providerErrors.ErrInvalidRedirectURI
			},
		},
		"Error_when_getgroups_returns_retry_with_device_auth_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, &providerErrors.RetryWithDeviceAuthError{Err: errors.New("token acquisition failed")}
			},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = sessionmode.Login
			}

			if tc.sessionOffline {
				tc.customHandlers = map[string]testutils.EndpointHandler{
					"/.well-known/openid-configuration": testutils.UnavailableHandler(),
				}
			}

			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")

			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			cfg := &brokerForTestConfig{
				Config:                        broker.Config{DataDir: dataDir},
				getGroupsFails:                tc.getGroupsFails,
				ownerAllowed:                  true,
				firstUserBecomesOwner:         !tc.userDoesNotBecomeOwner,
				allUsersAllowed:               tc.allUsersAllowed,
				forceAccessCheckWithProvider:  tc.forceAccessCheckWithProvider,
				extraGroups:                   tc.extraGroups,
				ownerExtraGroups:              tc.ownerExtraGroups,
				supportsDeviceRegistration:    tc.providerSupportsDeviceRegistration,
				requireNameClaimOnInitialAuth: tc.requireNameClaimOnInitialAuth,
				registerDevice:                tc.registerDevice,
				supportsMetadata:              tc.providerSupportsMetadata,
				metadataGetErr:                tc.metadataGetErr,
				supportsUserDisabledCheck:     tc.providerSupportsUserDisabledCheck,
				userDisabledErrorCode:         tc.userDisabledErrorCode,
				supportsFetchingGroups:        !tc.providerHasNoGroupFetcher,
				tokenHandlerOptions:           tc.tokenHandlerOptions,
			}
			if tc.customHandlers == nil {
				// Use the default provider URL if no custom handlers are provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.customHandlers = tc.customHandlers
				cfg.listenAddress = tc.address
			}
			if tc.groupsReturnedByProvider != nil {
				cfg.getGroupsFunc = func() ([]info.Group, error) {
					return tc.groupsReturnedByProvider, nil
				}
			}
			if tc.getGroupsFunc != nil {
				cfg.getGroupsFunc = tc.getGroupsFunc
			}
			b := newBrokerForTests(t, cfg)

			sessionID, key := newSessionForTests(t, b, tc.username, tc.sessionMode)

			if tc.token != nil {
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

			switch tc.firstSecret {
			case "":
				tc.firstSecret = correctPassword
			case "-":
				tc.firstSecret = ""
			}

			authData := "{}"
			if tc.firstSecret != "" {
				eKey := key
				if tc.badFirstKey {
					eKey = ""
				}
				secret := encryptSecret(t, tc.firstSecret, eKey)
				field := broker.AuthDataSecret
				if tc.useOldNameForSecretField {
					field = broker.AuthDataSecretOld
				}
				authData = fmt.Sprintf(`{"%s":"%s"}`, field, secret)
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

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_call"), out, 0600)
				require.NoError(t, err, "Failed to write first response")

				if tc.wantNextAuthModes != nil {
					nextAuthModes := b.GetNextAuthModes(sessionID)
					require.ElementsMatch(t, tc.wantNextAuthModes, nextAuthModes, "Next auth modes should match")
				}

				if tc.wantGroups != nil {
					type userInfoMsgType struct {
						UserInfo info.User `json:"userinfo"`
					}
					userInfoMsg := userInfoMsgType{}
					err = json.Unmarshal([]byte(data), &userInfoMsg)
					require.NoError(t, err, "Failed to unmarshal user info message")
					userInfo := userInfoMsg.UserInfo
					require.ElementsMatch(t, tc.wantGroups, userInfo.Groups, "Groups should match")
				}
				if tc.wantGecos != "" {
					type userInfoMsgType struct {
						UserInfo info.User `json:"userinfo"`
					}
					userInfoMsg := userInfoMsgType{}
					err = json.Unmarshal([]byte(data), &userInfoMsg)
					require.NoError(t, err, "Failed to unmarshal user info message")
					require.Equal(t, tc.wantGecos, userInfoMsg.UserInfo.Gecos, "GECOS should match")
				}
			}()

			if !tc.dontWaitForFirstCall {
				<-firstCallDone
			}

			if tc.wantOffline {
				gotOffline, err := b.IsOffline(sessionID)
				require.NoError(t, err, "IsOffline should not have returned an error")
				require.True(t, gotOffline, "Session should be offline after token refresh network error")
			}

			// When forceAccessCheckWithProvider is set, offline fallback must never happen,
			// even for transient network errors. Verify the session was not flipped to offline.
			// (Skip if the session was already offline at creation, which is a separate scenario.)
			if tc.forceAccessCheckWithProvider && !tc.sessionOffline {
				gotOffline, err := b.IsOffline(sessionID)
				require.NoError(t, err, "IsOffline should not have returned an error")
				require.False(t, gotOffline, "Session should not be offline when forceAccessCheckWithProvider is true")
			}

			if tc.wantSecondCall {
				// Give some time for the first call
				time.Sleep(10 * time.Millisecond)

				secret := "passwordpassword"
				if tc.secondSecret == "-" {
					secret = ""
				}

				secret = encryptSecret(t, secret, key)
				field := broker.AuthDataSecret
				if tc.useOldNameForSecretField {
					field = broker.AuthDataSecretOld
				}
				secondAuthData := fmt.Sprintf(`{"%s":"%s"}`, field, secret)
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
				err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely a token"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic token file")
			}
			passwordPath := b.PasswordFilepathForSession(sessionID)
			if _, err := os.Stat(passwordPath); err == nil {
				err := os.WriteFile(passwordPath, []byte("Definitely a hashed password"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic password file")
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			if _, err := os.Stat(filepath.Dir(b.TokenPathForSession(sessionID))); err == nil {
				issuerDir := filepath.Dir(filepath.Dir(b.TokenPathForSession(sessionID)))
				newIsserDir := filepath.Join(filepath.Dir(issuerDir), "provider_url")
				err := os.Rename(issuerDir, newIsserDir)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename token directory")
					t.Logf("Failed to rename token directory: %v", err)
				}
			}

			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

// Due to ordering restrictions, this test can not be run in parallel, otherwise the routines would not be ordered as expected.
func TestConcurrentIsAuthenticated(t *testing.T) {
	tests := map[string]struct {
		firstCallDelay        int
		secondCallDelay       int
		ownerAllowed          bool
		allUsersAllowed       bool
		firstUserBecomesOwner bool

		timeBetween time.Duration
	}{
		"First_auth_starts_and_finishes_before_second": {
			secondCallDelay: 1,
			timeBetween:     2 * time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_but_second_finishes_first": {
			firstCallDelay:  3,
			timeBetween:     time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_then_second_starts_and_first_finishes": {
			firstCallDelay:  2,
			secondCallDelay: 3,
			timeBetween:     time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_but_second_finishes_first_and_is_registered_as_the_owner": {
			firstCallDelay:        3,
			timeBetween:           time.Second,
			ownerAllowed:          true,
			firstUserBecomesOwner: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")
			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			username1 := "user1@example.com"
			username2 := "user2@example.com"

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                 broker.Config{DataDir: dataDir},
				allUsersAllowed:        tc.allUsersAllowed,
				ownerAllowed:           tc.ownerAllowed,
				firstUserBecomesOwner:  tc.firstUserBecomesOwner,
				firstCallDelay:         tc.firstCallDelay,
				secondCallDelay:        tc.secondCallDelay,
				supportsFetchingGroups: true,
				tokenHandlerOptions: &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"sub": "user1", "name": "user1", "email": username1},
						{"sub": "user2", "name": "user2", "email": username2},
					},
				},
			})

			firstSession, firstKey := newSessionForTests(t, b, username1, "")
			firstToken := tokenOptions{username: username1}
			generateAndStoreCachedInfo(t, firstToken, b.TokenPathForSession(firstSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(firstSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			secondSession, secondKey := newSessionForTests(t, b, username2, "")
			secondToken := tokenOptions{username: username2}
			generateAndStoreCachedInfo(t, secondToken, b.TokenPathForSession(secondSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(secondSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			firstCallDone := make(chan struct{})
			go func() {
				t.Logf("%s: First auth starting", t.Name())
				defer close(firstCallDone)

				updateAuthModes(t, b, firstSession, authmodes.Password)

				secret := encryptSecret(t, "password", firstKey)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

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

				secret := encryptSecret(t, "password", secondKey)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

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
					err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely a token"), 0600)
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
				err := os.Rename(issuerDataDir, filepath.Join(filepath.Dir(issuerDataDir), "provider_url"))
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename issuer data directory")
					t.Logf("Failed to rename issuer data directory: %v", err)
				}
			}
			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

func TestIsAuthenticatedAllowedUsersConfig(t *testing.T) {
	t.Parallel()

	u1 := "u1"
	u2 := "u2"
	u3 := "U3"
	allUsers := []string{u1, u2, u3}

	idTokenClaims := []map[string]interface{}{}
	for _, uname := range allUsers {
		idTokenClaims = append(idTokenClaims, map[string]interface{}{"sub": "user", "name": "user", "email": uname})
	}

	tests := map[string]struct {
		allowedUsers          map[string]struct{}
		owner                 string
		ownerAllowed          bool
		allUsersAllowed       bool
		firstUserBecomesOwner bool

		wantAllowedUsers   []string
		wantUnallowedUsers []string
	}{
		"No_users_allowed": {
			wantUnallowedUsers: allUsers,
		},
		"No_users_allowed_when_owner_is_allowed_but_not_set": {
			ownerAllowed:       true,
			wantUnallowedUsers: allUsers,
		},
		"No_users_allowed_when_owner_is_set_but_not_allowed": {
			owner:              u1,
			wantUnallowedUsers: allUsers,
		},

		"All_users_are_allowed": {
			allUsersAllowed:  true,
			wantAllowedUsers: allUsers,
		},
		"Only_owner_allowed": {
			ownerAllowed:       true,
			owner:              u1,
			wantAllowedUsers:   []string{u1},
			wantUnallowedUsers: []string{u2, u3},
		},
		"Only_first_user_allowed": {
			ownerAllowed:          true,
			firstUserBecomesOwner: true,
			wantAllowedUsers:      []string{u1},
			wantUnallowedUsers:    []string{u2, u3},
		},
		"Specific_users_allowed": {
			allowedUsers:       map[string]struct{}{u1: {}, u2: {}},
			wantAllowedUsers:   []string{u1, u2},
			wantUnallowedUsers: []string{u3},
		},
		"Specific_users_and_owner": {
			ownerAllowed:       true,
			allowedUsers:       map[string]struct{}{u1: {}},
			owner:              u2,
			wantAllowedUsers:   []string{u1, u2},
			wantUnallowedUsers: []string{u3},
		},
		"Usernames_are_normalized": {
			ownerAllowed:       true,
			allowedUsers:       map[string]struct{}{u3: {}},
			owner:              strings.ToLower(u3),
			wantAllowedUsers:   []string{u3},
			wantUnallowedUsers: []string{u1, u2},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")
			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                broker.Config{DataDir: dataDir},
				allowedUsers:          tc.allowedUsers,
				owner:                 tc.owner,
				ownerAllowed:          tc.ownerAllowed,
				allUsersAllowed:       tc.allUsersAllowed,
				firstUserBecomesOwner: tc.firstUserBecomesOwner,
				tokenHandlerOptions: &testutils.TokenHandlerOptions{
					IDTokenClaims: idTokenClaims,
				},
			})

			for _, u := range allUsers {
				sessionID, key := newSessionForTests(t, b, u, "")
				token := tokenOptions{username: u}
				generateAndStoreCachedInfo(t, token, b.TokenPathForSession(sessionID))
				err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

				updateAuthModes(t, b, sessionID, authmodes.Password)

				secret := encryptSecret(t, "password", key)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")
				require.NoError(t, err)
				if slices.Contains(tc.wantAllowedUsers, u) {
					require.Equal(t, broker.AuthGranted, access, "authentication failed")
					continue
				}
				if slices.Contains(tc.wantUnallowedUsers, u) {
					require.Equal(t, broker.AuthDenied, access, "authentication failed")
					continue
				}
				t.Fatalf("user %s is not in the allowed or unallowed users list", u)
			}
		})
	}
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": testutils.HangingHandler(3 * time.Second),
		},
	})
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

func TestIsAuthenticatedMaxAttempts(t *testing.T) {
	t.Parallel()

	correctPassword := "password"

	tests := map[string]struct {
		apiVersion uint
		wantAccess string
	}{
		"Returns_denied-max-tries_when_api_version_is_2": {
			apiVersion: 2,
			wantAccess: broker.AuthDeniedMaxTries,
		},
		"Returns_denied-max-tries_when_api_version_is_greater_than_2": {
			apiVersion: 3,
			wantAccess: broker.AuthDeniedMaxTries,
		},
		"Returns_denied_when_api_version_is_less_than_2": {
			apiVersion: 1,
			wantAccess: broker.AuthDenied,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL:  defaultIssuerURL,
				apiVersion: tc.apiVersion,
			})

			sessionID, key := newSessionForTests(t, b, "", "")

			// Store a valid token and password so that password auth mode is available.
			generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
			err := password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			wrongSecret := encryptSecret(t, "wrongpassword", key)
			authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, wrongSecret)

			err = b.SetAttemptsPerMode(sessionID, authmodes.Password, broker.MaxAuthAttempts-1)
			require.NoError(t, err, "Setup: Failed to set auth attempts")
			updateAuthModes(t, b, sessionID, authmodes.Password)

			access, data, err := b.IsAuthenticated(sessionID, authData)
			require.NoError(t, err, "IsAuthenticated should not have returned an error")

			require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
			require.Equal(t, tc.wantAccess, access, "Final attempt should return %s", tc.wantAccess)
			golden.CheckOrUpdateYAML(t, isAuthenticatedResponse{Access: access, Data: data, Err: ""})
		})
	}
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		issuerURL: defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "", "")

	// Try to end a session that does not exist
	err := b.EndSession("nonexistent")
	require.Error(t, err, "EndSession should have returned an error when ending a nonexistent session")

	// End a session that exists
	err = b.EndSession(sessionID)
	require.NoError(t, err, "EndSession should not have returned an error when ending an existent session")
}

func TestEndSessionReleasesPendingMFAFlow(t *testing.T) {
	t.Parallel()

	released := 0
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    newTrackedMFAFlowState(func() { released++ }),
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Approve the sign-in request in Microsoft Authenticator",
			Method:          "PhoneAppNotification",
			PollingInterval: 5000,
			MaxPollAttempts: 10,
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, 0, released, "MFA flow should still be active before ending the session")

	err = b.EndSession(sessionID)
	require.NoError(t, err)
	require.Equal(t, 1, released, "EndSession should release any pending MFA flow state")
}

func TestIsAuthenticatedEntraMFAWaitStartsPollingAtOne(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Approve the sign-in request in Microsoft Authenticator",
			PollingInterval: 1,
			MaxPollAttempts: 1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, "{}", data, "AuthNext after password should carry no message (avoids PAM read-delay)")
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")
	require.Equal(t, "Approve the sign-in request in Microsoft Authenticator", layout["label"],
		"entra_mfa_wait layout label should reflect the MFA challenge message")

	access, data, err = b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	require.Equal(t, []int{1}, provider.recordedPollAttempts)
	require.Equal(t, []string{""}, provider.recordedChallengeData)

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the offline password")
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the refreshed token")
}

// advanceToEntraMFAWait submits the Entra password for the session and selects the
// entra_mfa_wait mode, leaving the session ready for the polling IsAuthenticated("{}").
func advanceToEntraMFAWait(t *testing.T, b *broker.Broker, sessionID, key string) {
	t.Helper()

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	require.NoError(t, b.SetAvailableMode(sessionID, authmodes.EntraMFAWait))
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
}

// TestIsAuthenticatedEntraMFAWaitPollsWhenMaxPollAttemptsZero verifies that a
// MaxPollAttempts value of 0 (which libhimmelblau can produce from
// expires_in/polling_interval flooring to zero) still polls rather than returning
// an immediate, false "MFA timed out".
func TestIsAuthenticatedEntraMFAWaitPollsWhenMaxPollAttemptsZero(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider:   &testutils.MockProvider{},
		flowState:      &himmelblau.MFAFlowState{},
		challengeInfo:  &himmelblau.MFAChallengeInfo{Message: "Approve the sign-in request", PollingInterval: 1, MaxPollAttempts: 0},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "MaxPollAttempts==0 must still poll, not instant-timeout")
	require.Equal(t, []int{1}, provider.recordedPollAttempts, "the poll loop must run at least once when MaxPollAttempts==0")
}

// TestIsAuthenticatedEntraMFADeniesOnNilToken verifies the defensive nil-token
// guard: a provider returning (nil, nil) from AcquireTokenByMFAFlow must deny
// rather than panic the broker on the token dereference in finishEntraAuth.
func TestIsAuthenticatedEntraMFADeniesOnNilToken(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	provider := &mockMFANilTokenProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider:  &testutils.MockProvider{},
			flowState:     &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{Message: "Approve the sign-in request", PollingInterval: 1, MaxPollAttempts: 1},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a (nil, nil) MFA result must deny, not panic")
}

// TestIsAuthenticatedEntraMFAWaitNumberMatchingLabelShown verifies that when the MFA
// challenge message from libhimmelblau includes a number-matching code (e.g.
// PhoneAppNotification with entropy), that message is used as the entra_mfa_wait
// layout label so the user can see the number to match in the Authenticator app.
func TestIsAuthenticatedEntraMFAWaitNumberMatchingLabelShown(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	// Simulate the message libhimmelblau returns for PhoneAppNotification with number
	// matching: "Open your Authenticator app, and enter the number '60' to sign in."
	numberMatchingMsg := "Open your Authenticator app, and enter the number '60' to sign in."
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         numberMatchingMsg,
			Method:          "PhoneAppNotification",
			PollingInterval: 5000,
			MaxPollAttempts: 10,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Submit password – broker should offer entra_mfa_wait for PhoneAppNotification.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	require.Equal(t, numberMatchingMsg, layout["label"],
		"entra_mfa_wait label must show the number-matching message so the user can approve in the Authenticator app")
}

// TestIsAuthenticatedEntraMFAWaitDeniedWhenDeviceRegistrationFails verifies
// that authentication is denied when device registration fails, even after
// successful MFA. Without device registration the token exchange cannot be
// completed and group membership cannot be resolved, so granting access would
// leave the user in a broken state.
func TestIsAuthenticatedEntraMFAWaitDeniedWhenDeviceRegistrationFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockDeviceRegistrationFailProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:         "Approve the sign-in request in Microsoft Authenticator",
				Method:          "PhoneAppNotification",
				PollingInterval: 5000,
				MaxPollAttempts: 10,
			},
			mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		registerDevice:        true,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — should initiate MFA.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	// Step 2: MFA poll — device registration fails and auth should be denied.
	updateAuthModes(t, b, sessionID, authmodes.EntraMFAWait)
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"device registration failure must deny auth: without device registration the token exchange and group resolution cannot succeed")
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
}

func TestIsAuthenticatedEntraMFADeniedWhenInitialGroupFetchFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFails: true},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Approve the sign-in request in Microsoft Authenticator",
			Method:          "PhoneAppNotification",
			PollingInterval: 1,
			MaxPollAttempts: 1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "initial Entra MFA logins must be denied when groups cannot be resolved")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "Failed to retrieve groups")
}

func TestIsAuthenticatedEntraMFAUsesCachedGroupsWhenRefreshFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	oldAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	oldAuthInfo.UserInfo.Groups = []info.Group{{Name: "cached-group", UGID: "cached-id"}}
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFails: true},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Approve the sign-in request in Microsoft Authenticator",
			Method:          "PhoneAppNotification",
			PollingInterval: 1,
			MaxPollAttempts: 1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), oldAuthInfo))

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "cached groups should permit re-authentication when Graph refresh fails")

	var payload struct {
		UserInfo struct {
			Groups []info.Group `json:"groups"`
		} `json:"userinfo"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, []info.Group{{Name: "cached-group", UGID: "cached-id"}}, payload.UserInfo.Groups)
}

// TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin verifies that on a
// first Entra MFA login (no cached groups to fall back to) a group fetch that fails
// with a user-displayable ForDisplayError (e.g. a missing GroupMember.Read.All
// permission — a configuration problem) is surfaced verbatim by finishEntraAuth
// instead of being replaced by a misleading generic network hint. This is
// independent of force_access_check_with_provider (left unset here on purpose): the
// surfacing is driven by there being no cached groups, not by the forced check.
func TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	const graphPermMsg = "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{
			GetGroupsFunc: func() ([]info.Group, error) {
				return nil, &providerErrors.ForDisplayError{Message: graphPermMsg}
			},
		},
		flowState: &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Approve the sign-in request in Microsoft Authenticator",
			Method:          "PhoneAppNotification",
			PollingInterval: 1,
			MaxPollAttempts: 1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, graphPermMsg, payload.Message,
		"a ForDisplayError from the group fetch must be surfaced verbatim, not replaced by the generic network message")
	require.NotContains(t, payload.Message, "network connection")
}

func TestGetAuthenticationModesFiltersNextAuthModesByFlows(t *testing.T) {
	t.Parallel()

	// Use a provider that implements EntraPasswordProvider so that
	// authModeIsAvailable can confirm the capability before offering the mode.
	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{},
		flowState:     &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{},
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                 broker.Config{DataDir: t.TempDir()},
		provider:               provider,
		issuerURL:              defaultIssuerURL,
		ownerAllowed:           true,
		firstUserBecomesOwner:  true,
		deviceAuthFlowDisabled: true,
	})

	sessionID, _ := newSessionForTests(t, b, "", sessionmode.Login)
	b.SetNextAuthModes(sessionID, []string{authmodes.EntraPassword, authmodes.DeviceQr})

	modes, err := b.GetAuthenticationModes(sessionID, []map[string]string{
		supportedUILayouts["form"],
		supportedUILayouts["qrcode"],
	})
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{
		"id":    authmodes.EntraPassword,
		"label": authmodes.Label[authmodes.EntraPassword],
	}}, modes)
}

func TestIsAuthenticatedPasswordGrantRevokedInvalidatesCachedCredentials(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config: broker.Config{DataDir: t.TempDir()},
		provider: &mockGrantRevokedProvider{mockProviderWithEntraModes: &mockProviderWithEntraModes{
			MockProvider: &testutils.MockProvider{},
		}},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS50173: The provided grant has been revoked due to a password reset."}`))
			},
		},
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
	err := password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err)

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	// reauthModes includes EntraPassword, but the provider does not implement
	// EntraPasswordProvider, so authModeIsAvailable filters it out — only
	// Device/DeviceQr survive into the actual offer.
	require.Equal(t, []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr}, b.GetNextAuthModes(sessionID))

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.ErrorIs(t, err, os.ErrNotExist)

	nextSessionID, _ := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	modes, err := b.GetAuthenticationModes(nextSessionID, []map[string]string{
		supportedUILayouts["form"],
		supportedUILayouts["qrcode"],
	})
	require.NoError(t, err)

	var modeIDs []string
	for _, mode := range modes {
		modeIDs = append(modeIDs, mode["id"])
	}
	// entra_password is in reauthModes but filtered by the capability check; only device modes offered.
	require.ElementsMatch(t, []string{authmodes.DeviceQr}, modeIDs)
}

// TestIsAuthenticatedPasswordEntraTokenFallsBackToCachedGroupsOnGroupFetchError
// verifies that on a returning login with a cached Entra password + MFA token, a
// group-fetch failure — even a user-displayable ForDisplayError such as a missing
// GroupMember.Read.All permission — falls back to the cached groups instead of
// denying, exactly like the device-auth flow. The live provider check now happens
// at the token refresh (see refreshEntraPasswordToken), so the group fetch is no
// longer a liveness signal. The ForDisplayError is still surfaced on a *first*
// login that has no cached groups (see
// TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin).
func TestIsAuthenticatedPasswordEntraTokenFallsBackToCachedGroupsOnGroupFetchError(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const graphPermMsg = "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"
	cachedGroups := []info.Group{{Name: "cached-group", UGID: "cached-id"}}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                       broker.Config{DataDir: t.TempDir()},
		ownerAllowed:                 true,
		firstUserBecomesOwner:        true,
		issuerURL:                    defaultIssuerURL,
		forceAccessCheckWithProvider: true,
		supportsFetchingGroups:       true,
		getGroupsFunc: func() ([]info.Group, error) {
			return nil, &providerErrors.ForDisplayError{Message: graphPermMsg}
		},
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true, groups: cachedGroups}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access,
		"a returning Entra login must fall back to cached groups when the group fetch fails, not deny")

	var payload struct {
		UserInfo struct {
			Groups []info.Group `json:"groups"`
		} `json:"userinfo"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, cachedGroups, payload.UserInfo.Groups, "cached groups must be used when the group fetch fails")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshDetectsDisabledUser verifies that on a
// returning login the Entra password token refresh (refreshEntraPasswordToken) is the
// live disabled-user check: an AADSTS50057-class rejection is classified exactly like
// the device-auth flow — login is denied and UserIsDisabled is cached so later offline
// attempts are denied too.
func TestIsAuthenticatedPasswordEntraTokenRefreshDetectsDisabledUser(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	provider := &mockEntraPasswordProvider{
		MockProvider:          &testutils.MockProvider{},
		userDisabledErrorCode: "user_disabled",
		refreshErr: &oauth2.RetrieveError{
			ErrorCode:        "user_disabled",
			ErrorDescription: "AADSTS50057: The user account is disabled.",
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a disabled user must be denied at the refresh step")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "disabled")

	// The disabled state must be cached so subsequent offline logins are denied too.
	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.True(t, cached.UserIsDisabled, "UserIsDisabled must be cached after an AADSTS50057 refresh rejection")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshRotatesRefreshToken verifies that a
// successful Entra password token refresh on a returning login rotates the cached
// refresh token (kept fresh on each login, like the device-auth flow) and that the
// rotated token is persisted for the next login.
func TestIsAuthenticatedPasswordEntraTokenRefreshRotatesRefreshToken(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const rotatedRefreshToken = "rotated-refresh-token"
	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) { return []info.Group{{Name: "remote-group"}}, nil }},
		refreshResult: &oauth2.Token{AccessToken: "new-access-token", RefreshToken: rotatedRefreshToken},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true, groups: []info.Group{{Name: "remote-group"}}}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, rotatedRefreshToken, cached.Token.RefreshToken,
		"the rotated refresh token from refreshEntraPasswordToken must be persisted")
}

// TestIsAuthenticatedPhoneAppOTPRoutesToMFACode verifies that PhoneAppOTP
// (Authenticator TOTP) is routed to entra_mfa_code even when pollingInterval > 0,
// and that AcquireTokenByMFAFlow is called with poll_attempt=0 and the user's code.
func TestIsAuthenticatedPhoneAppOTPRoutesToMFACode(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:         "Please type in the code displayed on your authenticator app from your device:",
			Method:          "PhoneAppOTP",
			PollingInterval: 5000, // positive — must NOT cause poll routing
			MaxPollAttempts: 10,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — broker should recognise PhoneAppOTP and offer entra_mfa_code.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, "{}", data, "AuthNext after password should carry no message (avoids PAM read-delay)")
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID),
		"PhoneAppOTP should route to entra_mfa_code, not entra_mfa_wait")

	// Step 2: Submit the OTP code — should call AcquireTokenByMFAFlow with poll_attempt=0.
	err = b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")
	require.Equal(t, "Enter your MFA code", layout["label"],
		"The input label should remain generic")

	otpCode := "123456"
	otpAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, otpCode, key))

	access, data, err = b.IsAuthenticated(sessionID, otpAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	require.Equal(t, []int{0}, provider.recordedPollAttempts,
		"PhoneAppOTP must call AcquireTokenByMFAFlow with poll_attempt=0")
	require.Equal(t, []string{otpCode}, provider.recordedChallengeData,
		"PhoneAppOTP must pass the user-entered code as auth_data")

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the offline password")
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the refreshed token")
}

// TestIsAuthenticatedEntraMFACodeWrongCodeRetries verifies that an incorrect or
// expired one-time code keeps the MFA flow alive and re-prompts for the code
// (AuthRetry) rather than discarding the flow and forcing password re-entry. A
// subsequent correct code then completes authentication.
func TestIsAuthenticatedEntraMFACodeWrongCodeRetries(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	released := 0
	provider := &mockMFAWrongCodeThenSuccessProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    newTrackedMFAFlowState(func() { released++ }),
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:         "Please type in the code displayed on your authenticator app:",
				Method:          "PhoneAppOTP",
				PollingInterval: 5000,
				MaxPollAttempts: 10,
			},
			mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — routed to entra_mfa_code (PhoneAppOTP).
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")

	// Step 2: Submit a wrong code — should retry (stay on the code prompt), keep
	// the flow alive, and not yet cache the offline password.
	wrongAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "000000", key))
	access, data, err := b.IsAuthenticated(sessionID, wrongAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthRetry, access, "a wrong MFA code must return AuthRetry, not bounce back to the password step")
	require.Contains(t, data, "Incorrect or expired code", "the retry message should ask for the code again")
	require.Equal(t, 0, released, "the MFA flow must NOT be released on a retryable wrong code")
	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.Error(t, err, "no offline password should be cached after a wrong code")

	// Step 3: Submit the correct code — completes auth using the same flow.
	rightAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, _, err = b.IsAuthenticated(sessionID, rightAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "a correct code after a wrong one should grant access")
	require.Equal(t, []string{"000000", "123456"}, provider.recordedChallengeData,
		"both code submissions should reuse the same MFA flow")
	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "successful MFA completion should cache the offline password")
}

func TestIsAuthenticatedEntraMFAWaitDenialReturnsAuthDenied(t *testing.T) {
	t.Parallel()

	provider := &mockMFADeniedProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:         "Approve the sign-in request in Microsoft Authenticator",
				PollingInterval: 1,
				MaxPollAttempts: 5,
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access, "password submission should transition to MFA")

	// Select the MFA wait mode.
	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	// Poll - the mock will return a denial on first poll.
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "MFA denial should return AuthDenied, not AuthRetry")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "denied")
}

func TestIsAuthenticatedEntraMFAWaitTimeoutReturnsAuthNext(t *testing.T) {
	t.Parallel()

	provider := &mockMFATimeoutProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:         "Approve the sign-in request in Microsoft Authenticator",
				PollingInterval: 1,
				MaxPollAttempts: 2,
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	// Poll - the mock always returns MFA_POLL_CONTINUE, so max attempts will be exhausted.
	// After timeout the broker should redirect back to entra_password rather than
	// asking the client to retry a dead MFA wait mode.
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access, "MFA timeout should return AuthNext to restart from entra_password")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "timed out")
}

// TestEntraPasswordRoutesAADSTSErrors verifies that an AADSTS error raised while
// initiating the password+MFA flow is mapped to the right broker outcome.
func TestEntraPasswordRoutesAADSTSErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		aadsts             int
		category           himmelblau.MFAErrorCategory
		deviceAuthDisabled bool

		wantAccess    string
		wantNextModes []string
		wantMsg       string
	}{
		"Account_locked":                             {aadsts: 50053, wantAccess: broker.AuthDenied, wantMsg: "locked"},
		"Password_expired":                           {aadsts: 50055, wantAccess: broker.AuthDenied, wantMsg: "expired"},
		"Invalid_credentials_retry":                  {aadsts: 50126, wantAccess: broker.AuthRetry, wantMsg: "Incorrect password"},
		"Conditional_access_blocked":                 {aadsts: 53003, wantAccess: broker.AuthDenied, wantMsg: "Conditional Access"},
		"MFA_enrollment_to_device":                   {aadsts: 50072, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"MFA_enrollment_alt_to_device":               {aadsts: 50079, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"MFA_enrollment_denied_when_device_disabled": {aadsts: 50072, deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsg: "disabled"},
		"MFA_required_to_device":                     {category: himmelblau.MFAErrorRequired, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA is required"},
		"MFA_required_denied_when_device_disabled":   {category: himmelblau.MFAErrorRequired, deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsg: "disabled"},
		"Unhandled_AADSTS_denied":                    {aadsts: 99999, wantAccess: broker.AuthDenied, wantMsg: "Authentication failed"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &mockEntraPasswordProvider{
				MockProvider: &testutils.MockProvider{},
				initErr: &himmelblau.MFAInitError{
					AADSTS:   tc.aadsts,
					Category: tc.category,
					Message:  "simulated error",
				},
			}

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                 broker.Config{DataDir: t.TempDir()},
				ownerAllowed:           true,
				firstUserBecomesOwner:  true,
				provider:               provider,
				issuerURL:              defaultIssuerURL,
				deviceAuthFlowDisabled: tc.deviceAuthDisabled,
			})

			sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
			updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

			passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
			access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
			require.NoError(t, err)
			require.Equal(t, tc.wantAccess, access)

			if tc.wantNextModes != nil {
				require.Equal(t, tc.wantNextModes, b.GetNextAuthModes(sessionID))
			}

			var payload struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal([]byte(data), &payload))
			require.Contains(t, payload.Message, tc.wantMsg)
		})
	}
}

// TestEntraPasswordInvalidatesCachedCredentialsOnRemotePasswordChange verifies
// that an AADSTS50173 (grant revoked by a remote password change) wipes the
// cached token and password files and offers re-authentication.
func TestEntraPasswordInvalidatesCachedCredentialsOnRemotePasswordChange(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		initErr:      &himmelblau.MFAInitError{AADSTS: 50173, Message: "grant revoked"},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Seed cached credentials that the revocation must invalidate.
	cached := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), cached))
	require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "changed remotely")

	require.NoFileExists(t, b.TokenPathForSession(sessionID), "cached token should be removed on remote password change")
	require.NoFileExists(t, b.PasswordFilepathForSession(sessionID), "cached password should be removed on remote password change")
}

// TestIsAuthenticatedFIDOMethodRoutesToDevice verifies that a FIDO/security-key
// MFA method (unsupported in terminal auth) is rejected with guidance to use
// device authentication, and no credentials are cached.
func TestIsAuthenticatedFIDOMethodRoutesToDevice(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message: "Use your security key",
			Method:  "FidoKey",
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "FIDO")

	require.NoFileExists(t, b.PasswordFilepathForSession(sessionID))
}

// TestIsAuthenticatedEntraMFACodeDenied verifies that a denied code submission
// returns AuthDenied.
func TestIsAuthenticatedEntraMFACodeDenied(t *testing.T) {
	t.Parallel()

	provider := &mockMFADeniedProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message: "Enter the code from your authenticator app",
				Method:  "PhoneAppOTP",
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID))

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, data, err := b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "denied")
}

// TestIsAuthenticatedEntraMFACodeFailureRoutesBack verifies that a non-denial
// failure during code verification clears the dead MFA state and routes the
// client back to entra_password rather than the now-dead code mode.
func TestIsAuthenticatedEntraMFACodeFailureRoutesBack(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message: "Enter the code from your authenticator app",
			Method:  "PhoneAppOTP",
		},
		mfaTokenResult: nil, // AcquireTokenByMFAFlow returns a generic (non-denial) error.
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, data, err := b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraPassword}, b.GetNextAuthModes(sessionID),
		"a failed code submission should route back to entra_password")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "failed")
}

// TestIsAuthenticatedEntraMFAFallsBackToEmailClaim verifies that when the MFA
// token carries no preferred_username, the user identity is recovered from the
// email extra instead.
func TestIsAuthenticatedEntraMFAFallsBackToEmailClaim(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message: "Enter the code from your authenticator app",
			Method:  "PhoneAppOTP",
		},
		mfaTokenResult: mfaAuthInfo.Token.WithExtra(map[string]any{
			"email": username,
			"sub":   "saved-user-id",
			"name":  "test-user",
		}),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, _, err = b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "email claim should satisfy the user identity when preferred_username is absent")
}

func TestUserPreCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username        string
		allowedSuffixes []string
		homePrefix      string
	}{
		"Successfully_allow_username_with_matching_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"}},
		"Successfully_allow_username_that_matches_at_least_one_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
		},
		"Successfully_allow_username_if_suffix_is_allow_all": {
			username:        "user@doesnotmatter",
			allowedSuffixes: []string{"*"},
		},
		"Successfully_allow_username_if_suffix_has_asterisk": {
			username:        "user@allowed",
			allowedSuffixes: []string{"*@allowed"},
		},
		"Successfully_allow_username_ignoring_empty_string_in_config": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@anothersuffix", "", "@allowed"},
		},
		"Return_userinfo_with_correct_homedir_after_precheck": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"},
			homePrefix:      "/home/allowed/",
		},

		"Empty_userinfo_if_username_does_not_match_allowed_suffix": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@allowed"},
		},
		"Empty_userinfo_if_username_does_not_match_any_of_the_allowed_suffixes": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed", ""},
		},
		"Empty_userinfo_if_no_allowed_suffixes_are_provided": {
			username: "user@allowed",
		},
		"Empty_userinfo_if_allowed_suffixes_has_only_empty_string": {
			username:        "user@allowed",
			allowedSuffixes: []string{""},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL:          defaultIssuerURL,
				homeBaseDir:        tc.homePrefix,
				allowedSSHSuffixes: tc.allowedSuffixes,
			})

			got, err := b.UserPreCheck(tc.username)
			require.NoError(t, err, "UserPreCheck should not have returned an error")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestNormalizedIssuer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuerURL string
		want      string
	}{
		"HTTP_issuerURL":                     {issuerURL: "http://example.com", want: "example.com"},
		"HTTPS_issuerURL":                    {issuerURL: "https://example.com", want: "example.com"},
		"IssuerURL_with_path":                {issuerURL: "https://example.com/tenant/v2.0", want: "example.com_tenant_v2.0"},
		"IssuerURL_with_port":                {issuerURL: "https://example.com:8080", want: "example.com_8080"},
		"IssuerURL_with_port_and_path":       {issuerURL: "https://example.com:8080/path", want: "example.com_8080_path"},
		"IssuerURL_with_IP_address":          {issuerURL: "https://127.0.0.1", want: "127.0.0.1"},
		"IssuerURL_with_IP_address_and_port": {issuerURL: "https://127.0.0.1:8080", want: "127.0.0.1_8080"},
		"IssuerURL_without_scheme":           {issuerURL: "example.com", want: "example.com"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: tc.issuerURL,
			})

			got := b.NormalizedIssuer(tc.issuerURL)
			require.Equal(t, tc.want, got, "NormalizedIssuer returned unexpected result")
		})
	}
}

func TestUserDataDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuerURL string
		username  string
		want      string
		wantErr   bool
	}{
		"Successfully_return_user_data_dir_for_simple_username_and_issuer": {
			issuerURL: "https://example.com",
			username:  "user@example.com",
			want:      "example.com/user@example.com",
		},
		"Successfully_return_user_data_dir_for_issuer_url_without_scheme": {
			issuerURL: "example.com",
			username:  "user@example.com",
			want:      "example.com/user@example.com",
		},
		"Error_when_username_is_empty": {
			issuerURL: "https://example.com",
			username:  "",
			wantErr:   true,
		},
		"Error_when_username_contains_path_traversal": {
			issuerURL: "https://example.com",
			username:  "../test",
			wantErr:   true,
		},
		"Error_when_username_contains_path_traversal_but_does_not_leave_the_parent_directory": {
			issuerURL: "https://example.com",
			username:  "test/../other-user",
			wantErr:   true,
		},
		"Error_when_issuer_contains_path_traversal": {
			issuerURL: "https://..",
			username:  "validuser",
			wantErr:   true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: tc.issuerURL,
			})

			got, err := b.UserDataDir(tc.username)
			if tc.wantErr {
				require.Error(t, err, "UserDataDir should return an error, but did not")
				return
			}
			require.NoError(t, err, "UserDataDir should not return an error")
			require.Equal(t, filepath.Join(b.DataDir(), tc.want), got, "UserDataDir returned unexpected result")
		})
	}
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username        string
		createUserDir   bool
		readOnlyDataDir bool

		wantErr bool
	}{
		"Successfully_delete_existing_user":        {username: "user@example.com", createUserDir: true},
		"Successfully_delete_unknown_user_is_noop": {username: "unknown@example.com"},

		"Error_when_user_data_dir_cannot_be_removed":     {username: "user@example.com", createUserDir: true, readOnlyDataDir: true, wantErr: true},
		"Error_when_userDataDir_could_not_be_determined": {username: "", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: defaultIssuerURL,
			})

			// Derive the path where DeleteUser will look for the user's data
			userDataDir, err := b.UserDataDir(tc.username)
			if tc.username == "" {
				require.Error(t, err, "Setup: UserDataDir should have returned an error for empty username")
				return
			}
			require.NoError(t, err, "Setup: UserDataDir should not have returned an error for valid username")

			if tc.createUserDir {
				err := os.MkdirAll(userDataDir, 0700)
				require.NoError(t, err, "Setup: could not create user data directory")

				// Write a dummy token file so the directory is non-empty
				err = os.WriteFile(filepath.Join(userDataDir, "token.json"), []byte(`{}`), 0600)
				require.NoError(t, err, "Setup: could not write dummy token file")
			}

			if tc.readOnlyDataDir {
				// Make the issuer directory read-only so RemoveAll fails on the user subdir
				issuerDir := filepath.Dir(userDataDir)
				err := os.Chmod(issuerDir, 0500) //nolint:gosec // Intentional read-only permission for testing
				require.NoError(t, err, "Setup: could not make issuer directory read-only")
				t.Cleanup(func() { _ = os.Chmod(issuerDir, 0700) }) //nolint:gosec // Restore full permissions after test
			}

			err = b.DeleteUser(tc.username)
			if tc.wantErr {
				require.Error(t, err, "DeleteUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "DeleteUser should not return an error, but did")

			// Verify the user data directory no longer exists
			require.NoDirExists(t, userDataDir, "User data directory should have been removed")
		})
	}
}

func TestIsFIDOMethod(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
		want   bool
	}{
		"Empty":          {method: "", want: false},
		"Fido_lower":     {method: "fido", want: true},
		"Fido_upper":     {method: "FIDO", want: true},
		"FidoKey":        {method: "FidoKey", want: true},
		"Fido2_token":    {method: "FIDO2_token", want: true},
		"Webauthn_lower": {method: "webauthn", want: true},
		"WebAuthn_camel": {method: "WebAuthn", want: true},
		"Security_key":   {method: "security_key", want: true},
		"PhoneAppOTP":    {method: "PhoneAppOTP", want: false},
		"PhoneAppPush":   {method: "PhoneAppNotification", want: false},
		"OneWaySMS":      {method: "OneWaySMS", want: false},
		"Random":         {method: "AnythingElse", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, broker.IsFIDOMethod(tc.method))
		})
	}
}

func TestIsPromptMethod(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
		want   bool
	}{
		"AccessPass":            {method: "AccessPass", want: true},
		"PhoneAppOTP":           {method: "PhoneAppOTP", want: true},
		"OneWaySMS":             {method: "OneWaySMS", want: true},
		"ConsolidatedTelephony": {method: "ConsolidatedTelephony", want: true},
		"PhoneAppNotification":  {method: "PhoneAppNotification", want: false},
		"CompanionApps":         {method: "CompanionAppsNotification", want: false},
		"FidoKey":               {method: "FidoKey", want: false},
		"Empty":                 {method: "", want: false},
		"Lowercase_no_match":    {method: "phoneappotp", want: false},
		"Unknown":               {method: "SomeFutureMethod", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, broker.IsPromptMethod(tc.method))
		})
	}
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	var cleanup func()
	defaultIssuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
