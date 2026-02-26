package broker_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils/golden"
	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd/log"
	"gopkg.in/yaml.v3"
)

var defaultIssuerURL string

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

			b := newBrokerForTests(t, &brokerForTestConfig{
				customHandlers: tc.customHandlers,
			})

			id, _, err := b.NewSession("test-user", "lang", sessionmode.Login)
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
		forceProviderAuthentication        bool
		userDoesNotBecomeOwner             bool
		allUsersAllowed                    bool
		extraGroups                        []string
		ownerExtraGroups                   []string
		providerSupportsDeviceRegistration bool
		registerDevice                     bool

		firstMode                string
		firstSecret              string
		badFirstKey              bool
		getGroupsFails           bool
		useOldNameForSecretField bool
		groupsReturnedByProvider []info.Group

		customHandlers map[string]testutils.EndpointHandler
		address        string

		wantSecondCall bool
		secondMode     string
		secondSecret   string

		token                *tokenOptions
		invalidAuthData      bool
		dontWaitForFirstCall bool
		readOnlyDataDir      bool
		wantGroups           []info.Group
		wantNextAuthModes    []string
	}{
		"Successfully_authenticate_user_with_device_auth_and_newpassword": {firstSecret: "-", wantSecondCall: true},
		"Successfully_authenticate_user_with_password":                    {firstMode: authmodes.Password, token: &tokenOptions{}},

		"Authenticating_with_qrcode_reacquires_token":          {firstSecret: "-", wantSecondCall: true, token: &tokenOptions{}},
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
			wantNextAuthModes: []string{authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_no_refresh_token_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{noRefreshToken: true},
			wantNextAuthModes: []string{authmodes.Device, authmodes.DeviceQr},
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
			firstMode:                   authmodes.Password,
			token:                       &tokenOptions{},
			forceProviderAuthentication: true,
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
		"Error_when_mode_is_password_and_token_refresh_times_out": {firstMode: authmodes.Password, token: &tokenOptions{expired: true},
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
		"Error_when_provider_authentication_is_forced_and_session_is_offline": {
			firstMode:                   authmodes.Password,
			token:                       &tokenOptions{},
			forceProviderAuthentication: true,
			sessionOffline:              true,
		},
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
		"Error_when_mode_is_invalid": {firstMode: "invalid"},
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
				Config:                      broker.Config{DataDir: dataDir},
				getGroupsFails:              tc.getGroupsFails,
				ownerAllowed:                true,
				firstUserBecomesOwner:       !tc.userDoesNotBecomeOwner,
				allUsersAllowed:             tc.allUsersAllowed,
				forceProviderAuthentication: tc.forceProviderAuthentication,
				extraGroups:                 tc.extraGroups,
				ownerExtraGroups:            tc.ownerExtraGroups,
				supportsDeviceRegistration:  tc.providerSupportsDeviceRegistration,
				registerDevice:              tc.registerDevice,
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
			}()

			if !tc.dontWaitForFirstCall {
				<-firstCallDone
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
				Config:                broker.Config{DataDir: dataDir},
				allUsersAllowed:       tc.allUsersAllowed,
				ownerAllowed:          tc.ownerAllowed,
				firstUserBecomesOwner: tc.firstUserBecomesOwner,
				firstCallDelay:        tc.firstCallDelay,
				secondCallDelay:       tc.secondCallDelay,
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

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	var cleanup func()
	defaultIssuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
