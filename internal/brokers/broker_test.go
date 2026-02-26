package brokers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
)

var supportedLayouts = map[string]map[string]string{
	"required-entry": {
		layouts.Type:  "required-entry",
		layouts.Entry: layouts.RequiredItems("entry_type", "other_entry_type"),
	},
	"optional-entry": {
		layouts.Type:  "optional-entry",
		layouts.Entry: layouts.OptionalItems("entry_type", "other_entry_type"),
	},
	"missing-type": {
		layouts.Entry: layouts.RequiredItems("missing_type"),
	},
	"misconfigured-layout": {
		layouts.Type:  "misconfigured-layout",
		layouts.Entry: "required-but-misformatted",
	},
	"layout-with-spaces": {
		layouts.Type:  "layout-with-spaces",
		layouts.Entry: layouts.RequiredItems(" entry_type ", "other_entry_type"),
	},
}

func TestNewBroker(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configFile string

		wantErr bool
	}{
		"No_config_means_local_broker":                        {configFile: "-"},
		"Successfully_create_broker_with_correct_config_file": {configFile: "valid.conf"},

		// General config errors
		"Error_when_config_file_is_invalid":     {configFile: "invalid.conf", wantErr: true},
		"Error_when_config_file_does_not_exist": {configFile: "do not exist.conf", wantErr: true},

		// Missing field errors
		"Error_when_config_does_not_have_name_field":        {configFile: "no_name.conf", wantErr: true},
		"Error_when_config_does_not_have_brand_icon_field":  {configFile: "no_brand_icon.conf", wantErr: true},
		"Error_when_config_does_not_have_dbus_name_field":   {configFile: "no_dbus_name.conf", wantErr: true},
		"Error_when_config_does_not_have_dbus_object_field": {configFile: "no_dbus_object.conf", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			conn, err := testutils.GetSystemBusConnection(t)
			require.NoError(t, err, "Setup: could not connect to system bus")

			configDir := filepath.Join(brokerConfFixtures, "valid_brokers")
			if tc.wantErr {
				configDir = filepath.Join(brokerConfFixtures, "invalid_brokers")
			}
			if tc.configFile == "-" {
				tc.configFile = ""
			} else if tc.configFile != "" {
				tc.configFile = filepath.Join(configDir, tc.configFile)
			}

			got, err := brokers.NewBroker(context.Background(), tc.configFile, conn)
			if tc.wantErr {
				require.Error(t, err, "NewBroker should return an error, but did not")
				return
			}
			require.NoError(t, err, "NewBroker should not return an error, but did")

			gotString := fmt.Sprintf("ID: %s\nName: %s\nBrand Icon: %s\n", got.ID, got.Name, got.BrandIconPath)

			golden.CheckOrUpdate(t, gotString)
		})
	}
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, "", "")

	tests := map[string]struct {
		sessionID          string
		supportedUILayouts []string

		wantErr bool
	}{
		"Get_authentication_modes_and_generate_validators":                                         {sessionID: "success", supportedUILayouts: []string{"required-entry", "optional-entry"}},
		"Get_authentication_modes_and_generate_validator_ignoring_whitespaces_in_supported_values": {sessionID: "success", supportedUILayouts: []string{"layout-with-spaces"}},
		"Get_authentication_modes_and_ignores_invalid_UI_layout":                                   {sessionID: "success", supportedUILayouts: []string{"required-entry", "missing-type"}},
		"Get_multiple_authentication_modes_and_generate_validators":                                {sessionID: "gam_multiple_modes", supportedUILayouts: []string{"required-entry", "optional-entry"}},

		"Does_not_error_out_when_no_authentication_modes_are_returned": {sessionID: "gam_empty"},

		// broker errors
		"Error_when_getting_authentication_modes": {sessionID: "gam_error", wantErr: true},
		"Error_when_broker_returns_invalid_modes": {sessionID: "gam_invalid", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.supportedUILayouts == nil {
				tc.supportedUILayouts = []string{"required-entry"}
			}

			var supportedUILayouts []map[string]string
			for _, layout := range tc.supportedUILayouts {
				supportedUILayouts = append(supportedUILayouts, supportedLayouts[layout])
			}

			gotModes, err := b.GetAuthenticationModes(context.Background(), prefixID(t, tc.sessionID), supportedUILayouts)
			if tc.wantErr {
				require.Error(t, err, "GetAuthenticationModes should return an error, but did not")
				return
			}
			require.NoError(t, err, "GetAuthenticationModes should not return an error, but did")

			modesStr, err := json.Marshal(gotModes)
			require.NoError(t, err, "Post: error when marshaling result")

			got := "MODES:\n" + string(modesStr) + "\n\nVALIDATORS:\n" + b.LayoutValidatorsString(prefixID(t, tc.sessionID))
			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, "", "")

	tests := map[string]struct {
		sessionID          string
		supportedUILayouts []string

		wantErr bool
	}{
		"Successfully_select_mode_with_required_value":         {sessionID: "sam_success_required_entry"},
		"Successfully_select_mode_with_optional_value":         {sessionID: "sam_success_optional_entry", supportedUILayouts: []string{"optional-entry"}},
		"Successfully_select_mode_with_missing_optional_value": {sessionID: "sam_missing_optional_entry", supportedUILayouts: []string{"optional-entry"}},

		// broker errors
		"Error_when_selecting_invalid_auth_mode":              {sessionID: "sam_error", wantErr: true},
		"Error_when_no_validators_were_generated_for_session": {sessionID: "no-validators", wantErr: true},

		/* Layout errors */
		"Error_when_returns_no_layout":                          {sessionID: "sam_no_layout", wantErr: true},
		"Error_when_returns_empty_layout":                       {sessionID: "sam_empty_layout", wantErr: true},
		"Error_when_returns_layout_with_no_type":                {sessionID: "sam_no_layout_type", wantErr: true},
		"Error_when_returns_layout_with_invalid_type":           {sessionID: "sam_invalid_layout_type", wantErr: true},
		"Error_when_returns_layout_without_required_value":      {sessionID: "sam_missing_required_entry", wantErr: true},
		"Error_when_returns_layout_with_unknown_field":          {sessionID: "sam_unknown_field", wantErr: true},
		"Error_when_returns_layout_with_invalid_required_value": {sessionID: "sam_invalid_required_entry", wantErr: true},
		"Error_when_returns_layout_with_invalid_optional_value": {sessionID: "sam_invalid_optional_entry", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.supportedUILayouts == nil {
				tc.supportedUILayouts = []string{"required-entry"}
			}

			var supportedUILayouts []map[string]string
			for _, layout := range tc.supportedUILayouts {
				supportedUILayouts = append(supportedUILayouts, supportedLayouts[layout])
			}

			if tc.sessionID != "no-validators" {
				// This is normally done in the broker's GetAuthenticationModes method, but we need to do it here to test the SelectAuthenticationMode method.
				brokers.GenerateLayoutValidators(&b, prefixID(t, tc.sessionID), supportedUILayouts)
			}

			gotUI, err := b.SelectAuthenticationMode(context.Background(), prefixID(t, tc.sessionID), "mode1")
			if tc.wantErr {
				require.Error(t, err, "SelectAuthenticationMode should return an error, but did not")
				return
			}
			require.NoError(t, err, "SelectAuthenticationMode should not return an error, but did")

			golden.CheckOrUpdateYAML(t, gotUI)
		})
	}
}

func TestIsAuthenticated(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, "", "")

	tests := map[string]struct {
		sessionID  string
		secondCall bool

		cancelFirstCall bool
	}{
		"Successfully_authenticate":                                        {sessionID: "success"},
		"Successfully_authenticate_after_cancelling_first_call":            {sessionID: "ia_second_call", secondCall: true},
		"Denies_authentication_when_broker_times_out":                      {sessionID: "ia_timeout"},
		"Adds_default_groups_even_if_broker_did_not_set_them":              {sessionID: "ia_info_empty_groups"},
		"No_error_when_auth.Next_and_no_data":                              {sessionID: "ia_next"},
		"No_error_when_auth.Next_and_message":                              {sessionID: "ia_next_with_data"},
		"No_error_when_broker_returns_userinfo_with_empty_gecos":           {sessionID: "ia_info_empty_gecos"},
		"No_error_when_broker_returns_userinfo_with_group_with_empty_UGID": {sessionID: "ia_info_empty_ugid"},
		"No_error_when_broker_returns_userinfo_with_mismatching_username":  {sessionID: "ia_info_mismatching_user_name"},

		// broker errors
		"Error_when_authenticating":                                           {sessionID: "ia_error"},
		"Error_on_empty_data_even_if_granted":                                 {sessionID: "ia_empty_data"},
		"Error_when_broker_returns_invalid_data":                              {sessionID: "ia_invalid_data"},
		"Error_when_broker_returns_invalid_access":                            {sessionID: "ia_invalid_access"},
		"Error_when_broker_returns_invalid_userinfo":                          {sessionID: "ia_invalid_userinfo"},
		"Error_when_broker_returns_userinfo_with_empty_username":              {sessionID: "ia_info_empty_user_name"},
		"Error_when_broker_returns_userinfo_with_empty_group_name":            {sessionID: "ia_info_empty_group_name"},
		"Error_when_broker_returns_userinfo_with_invalid_homedir":             {sessionID: "ia_info_invalid_home"},
		"Error_when_broker_returns_userinfo_with_invalid_shell":               {sessionID: "ia_info_invalid_shell"},
		"Error_when_broker_returns_invalid_data_on_auth.Next":                 {sessionID: "ia_next_with_invalid_data"},
		"Error_when_broker_returns_data_on_auth.Cancelled":                    {sessionID: "ia_cancelled_with_data"},
		"Error_when_broker_returns_no_data_on_auth.Denied":                    {sessionID: "ia_denied_without_data"},
		"Error_when_broker_returns_no_data_on_auth.Retry":                     {sessionID: "ia_retry_without_data"},
		"Error_when_calling_IsAuthenticated_a_second_time_without_cancelling": {sessionID: "ia_second_call", secondCall: true, cancelFirstCall: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Stores the combined output of both calls to IsAuthenticated
			var firstCallReturn, secondCallReturn string

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sessionID := prefixID(t, tc.sessionID)

			// Add username to the ongoing requests
			b.AddOngoingUserRequest(sessionID, t.Name()+testutils.IDSeparator+tc.sessionID)

			done := make(chan struct{})
			go func() {
				defer close(done)
				access, gotData, err := b.IsAuthenticated(ctx, sessionID, "password")
				firstCallReturn = fmt.Sprintf("FIRST CALL:\n\taccess: %s\n\tdata: %s\n\terr: %v\n", access, gotData, err)
			}()

			// Give some time for the first call to block
			time.Sleep(time.Second)

			if tc.secondCall {
				if !tc.cancelFirstCall {
					cancel()
					<-done
				}
				access, gotData, err := b.IsAuthenticated(context.Background(), sessionID, "password")
				secondCallReturn = fmt.Sprintf("SECOND CALL:\n\taccess: %s\n\tdata: %s\n\terr: %v\n", access, gotData, err)
			}

			<-done
			gotStr := firstCallReturn + secondCallReturn
			golden.CheckOrUpdate(t, gotStr)
		})
	}
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, "", "")

	tests := map[string]struct {
		sessionID string

		wantAnswer string
	}{
		"Successfully_cancels_IsAuthenticated": {sessionID: "ia_wait", wantAnswer: auth.Cancelled},
		"Call_returns_denied_if_not_cancelled": {sessionID: "ia_timeout", wantAnswer: auth.Denied},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var access string
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				access, _, _ = b.IsAuthenticated(ctx, prefixID(t, tc.sessionID), "password")
				close(done)
			}()
			defer cancel()

			if tc.sessionID == "ia_wait" {
				// Give some time for the IsAuthenticated routine to start.
				time.Sleep(time.Second)
				cancel()
			}
			<-done
			require.Equal(t, tc.wantAnswer, access, "IsAuthenticated should return the expected access, but did not")
		})
	}
}

func TestUserPreCheck(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, "", "")

	tests := map[string]struct {
		username string

		wantErr bool
	}{
		"Successfully_pre-check_user": {username: "user-pre-check"},

		"Error_if_user_is_not_available": {username: "unexistent", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := b.UserPreCheck(context.Background(), tc.username)
			if tc.wantErr {
				require.Error(t, err, "UserPreCheck should return an error, but did not")
				return
			}
			require.NoError(t, err, "UserPreCheck should not return an error, but did")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func newBrokerForTests(t *testing.T, cfgDir, brokerCfg string) (b brokers.Broker) {
	t.Helper()

	if cfgDir == "" {
		cfgDir = t.TempDir()
	}
	brokerName := strings.TrimSuffix(brokerCfg, ".conf")
	if brokerName == "" {
		brokerName = strings.ReplaceAll(t.Name(), "/", "_")
	}

	cfgPath, cleanup, err := testutils.StartBusBrokerMock(cfgDir, brokerName)
	require.NoError(t, err, "Setup: could not start bus broker mock")
	t.Cleanup(cleanup)

	conn, err := testutils.GetSystemBusConnection(t)
	require.NoError(t, err, "Setup: could not connect to system bus")
	t.Cleanup(func() { require.NoError(t, conn.Close(), "Teardown: Failed to close the connection") })

	b, err = brokers.NewBroker(context.Background(), cfgPath, conn)
	require.NoError(t, err, "Setup: could not create broker")

	return b
}

// prefixID is a helper function that prefixes the given ID with the test name to avoid conflicts.
func prefixID(t *testing.T, id string) string {
	t.Helper()
	return t.Name() + testutils.IDSeparator + id
}
