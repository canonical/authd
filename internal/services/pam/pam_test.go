package pam_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/services/pam"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/db"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	userslocking "github.com/canonical/authd/internal/users/locking"
	userstestutils "github.com/canonical/authd/internal/users/testutils"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	globalBrokerManager   *brokers.Manager
	mockBrokerGeneratedID string
)

// Used for TestGetAuthenticationModes and TestSelectAuthenticationMode.
var (
	requiredEntries = layouts.RequiredItems("entry_type", "other_entry_type")
	optionalEntries = layouts.OptionalItems("entry_type", "other_entry_type")
	optional        = layouts.Optional

	rendersQrCode = true

	requiredEntry = &authd.UILayout{
		Type:          "required-entry",
		Label:         &optional,
		Button:        &optional,
		Wait:          &optional,
		Entry:         &requiredEntries,
		Content:       &optional,
		Code:          &optional,
		RendersQrcode: &rendersQrCode,
	}
	optionalEntry = &authd.UILayout{
		Type:  "optional-entry",
		Entry: &optionalEntries,
	}
	emptyType = &authd.UILayout{
		Type:  "",
		Entry: &requiredEntries,
	}
)

func TestNewService(t *testing.T) {
	t.Parallel()

	m, err := users.NewManager(users.DefaultConfig, t.TempDir())
	require.NoError(t, err, "Setup: could not create user manager")

	pm := permissions.New()
	service := pam.NewService(context.Background(), m, globalBrokerManager, &pm)

	brokers, err := service.AvailableBrokers(context.Background(), &authd.Empty{})
	require.NoError(t, err, "can’t create the service directly")
	require.NotEmpty(t, brokers.BrokersInfos, "Service is created and can query the broker manager")
}

func TestAvailableBrokers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		currentUserNotRoot bool

		wantErr bool
	}{
		"Success_getting_available_brokers": {},

		"Error_when_not_root": {currentUserNotRoot: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pm := newPermissionManager(t, tc.currentUserNotRoot)
			client := newPamClient(t, nil, globalBrokerManager, &pm)

			abResp, err := client.AvailableBrokers(context.Background(), &authd.Empty{})

			if tc.wantErr {
				require.Error(t, err, "AvailableBrokers should return an error, but did not")
				return
			}
			require.NoError(t, err, "AvailableBrokers should not return an error, but did")

			got := abResp.GetBrokersInfos()
			for _, broker := range got {
				broker.Id = broker.Name + "_ID"
			}
			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestGetPreviousBroker(t *testing.T) {
	t.Parallel()

	// Get local user and get it set to local broker
	u, err := user.Current()
	require.NoError(t, err, "Setup: could not fetch current user")
	currentUsername := u.Username

	tests := map[string]struct {
		user string

		currentUserNotRoot bool
		onlyLocalBroker    bool

		wantBroker string
		wantErr    bool
	}{
		"Success_getting_previous_broker":                          {user: "userwithbroker", wantBroker: mockBrokerGeneratedID},
		"For_local_user,_get_local_broker":                         {user: currentUsername, wantBroker: brokers.LocalBrokerName},
		"For_unmanaged_user_and_only_one_broker,_get_local_broker": {user: "nonexistent", onlyLocalBroker: true, wantBroker: brokers.LocalBrokerName},
		"Username_is_case_insensitive":                             {user: "UserWithBroker", wantBroker: mockBrokerGeneratedID},

		"Returns_empty_when_user_does_not_exist":         {user: "nonexistent", wantBroker: ""},
		"Returns_empty_when_user_does_not_have_a_broker": {user: "userwithoutbroker", wantBroker: ""},
		"Returns_empty_when_broker_is_not_available":     {user: "userwithinactivebroker", wantBroker: ""},

		"Error_when_not_root": {user: "userwithbroker", currentUserNotRoot: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()

			// We have to replace MOCKBROKERID with our generated broker id.
			f, err := os.Open(filepath.Join(testutils.TestFamilyPath(t), "get-previous-broker.db"))
			require.NoError(t, err, "Setup: could not open fixture database file")
			defer f.Close()
			d, err := io.ReadAll(f)
			require.NoError(t, err, "Setup: could not read fixture database file")
			d = bytes.ReplaceAll(d, []byte("MOCKBROKERID"), []byte(mockBrokerGeneratedID))
			err = db.Z_ForTests_CreateDBFromYAMLReader(bytes.NewBuffer(d), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m, err := users.NewManager(users.DefaultConfig, dbDir)
			require.NoError(t, err, "Setup: could not create user manager")
			t.Cleanup(func() { _ = m.Stop() })
			pm := newPermissionManager(t, tc.currentUserNotRoot)

			brokerManager := globalBrokerManager
			if tc.onlyLocalBroker {
				brokerManager, err = brokers.NewManager(context.Background(), "", nil)
				require.NoError(t, err, "Setup: could not create broker manager with only local broker")
			}
			client := newPamClient(t, m, brokerManager, &pm)

			// Get existing entry
			gotResp, err := client.GetPreviousBroker(context.Background(), &authd.GPBRequest{Username: tc.user})

			if tc.wantErr {
				require.Error(t, err, "GetPreviousBroker should return an error, but did not")
				return
			}
			require.NoError(t, err, "GetPreviousBroker should not return an error, but did")

			require.Equal(t, tc.wantBroker, gotResp.GetPreviousBroker(), "GetPreviousBroker should return expected broker")
		})
	}
}

func TestSelectBroker(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		brokerID    string
		username    string
		sessionMode string
		existingDB  string

		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_select_a_broker_and_creates_auth_session":   {username: "success", sessionMode: auth.SessionModeLogin},
		"Successfully_select_a_broker_and_creates_passwd_session": {username: "success", sessionMode: auth.SessionModeChangePassword},

		"Error_when_not_root":                             {username: "success", currentUserNotRoot: true, wantErr: true},
		"Error_when_username_is_empty":                    {wantErr: true},
		"Error_when_mode_is_empty":                        {sessionMode: "-", wantErr: true},
		"Error_when_mode_does_not_exist":                  {sessionMode: "does not exist", wantErr: true},
		"Error_when_brokerID_is_empty":                    {username: "empty broker", brokerID: "-", wantErr: true},
		"Error_when_broker_does_not_exist":                {username: "no broker", brokerID: "does not exist", wantErr: true},
		"Error_when_broker_does_not_provide_a_session_ID": {username: "ns_no_id", wantErr: true},
		"Error_when_starting_the_session":                 {username: "ns_error", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			if tc.existingDB != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join(testutils.TestFamilyPath(t), tc.existingDB), cacheDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}

			m, err := users.NewManager(users.DefaultConfig, cacheDir)
			require.NoError(t, err, "Setup: could not create user manager")
			t.Cleanup(func() { _ = m.Stop() })

			pm := newPermissionManager(t, tc.currentUserNotRoot)
			client := newPamClient(t, m, globalBrokerManager, &pm)

			switch tc.brokerID {
			case "":
				tc.brokerID = mockBrokerGeneratedID
			case "-":
				tc.brokerID = ""
			}

			if tc.username != "" {
				tc.username = t.Name() + testutils.IDSeparator + tc.username
			}

			var sessionMode authd.SessionMode
			switch tc.sessionMode {
			case auth.SessionModeLogin, "":
				sessionMode = authd.SessionMode_LOGIN
			case auth.SessionModeChangePassword:
				sessionMode = authd.SessionMode_CHANGE_PASSWORD
			case "-":
				sessionMode = authd.SessionMode_UNDEFINED
			}

			sbRequest := &authd.SBRequest{
				BrokerId: tc.brokerID,
				Username: tc.username,
				Mode:     sessionMode,
			}
			sbResp, err := client.SelectBroker(context.Background(), sbRequest)
			if tc.wantErr {
				require.Error(t, err, "SelectBroker should return an error, but did not")
				return
			}
			require.NoError(t, err, "SelectBroker should not return an error, but did")

			got := fmt.Sprintf("ID: %s\nEncryption Key: %s\n",
				strings.ReplaceAll(sbResp.GetSessionId(), tc.brokerID, "BROKER_ID"),
				sbResp.GetEncryptionKey())
			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionID          string
		supportedUILayouts []*authd.UILayout

		username           string
		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_get_authentication_modes":          {},
		"Successfully_get_multiple_authentication_modes": {username: "gam_multiple_modes"},

		"Error_when_not_root":                     {currentUserNotRoot: true, wantErr: true},
		"Error_when_sessionID_is_empty":           {sessionID: "-", wantErr: true},
		"Error_when_passing_invalid_layout":       {supportedUILayouts: []*authd.UILayout{emptyType}, wantErr: true},
		"Error_when_sessionID_is_invalid":         {sessionID: "invalid-session", wantErr: true},
		"Error_when_getting_authentication_modes": {username: "gam_error", wantErr: true},
		"Error_when_broker_returns_invalid_modes": {username: "gam_invalid", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pm := newPermissionManager(t, false) // Allow starting the session (current user considered root)
			client := newPamClient(t, nil, globalBrokerManager, &pm)

			switch tc.sessionID {
			case "invalid-session":
			case "-":
				tc.sessionID = ""
			default:
				id := startSession(t, client, tc.username)
				if tc.sessionID == "" {
					tc.sessionID = id
				}
			}

			// Now, set tests permissions for this use case
			permissions.Z_ForTests_SetCurrentUserAsRoot(&pm, !tc.currentUserNotRoot)

			if tc.supportedUILayouts == nil {
				tc.supportedUILayouts = []*authd.UILayout{requiredEntry}
			}

			gamReq := &authd.GAMRequest{
				SessionId:          tc.sessionID,
				SupportedUiLayouts: tc.supportedUILayouts,
			}
			gamResp, err := client.GetAuthenticationModes(context.Background(), gamReq)
			if tc.wantErr {
				require.Error(t, err, "GetAuthenticationModes should return an error, but did not")
				return
			}
			require.NoError(t, err, "GetAuthenticationModes should not return an error, but did")

			got := gamResp.GetAuthenticationModes()
			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionID string
		authMode  string

		username           string
		supportedUILayouts []*authd.UILayout
		noValidators       bool
		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_select_mode_with_required_value":         {username: "sam_success_required_entry", supportedUILayouts: []*authd.UILayout{requiredEntry}},
		"Successfully_select_mode_with_missing_optional_value": {username: "sam_missing_optional_entry", supportedUILayouts: []*authd.UILayout{optionalEntry}},

		// service errors
		"Error_when_not_root":                {username: "sam_success_required_entry", currentUserNotRoot: true, wantErr: true},
		"Error_when_sessionID_is_empty":      {sessionID: "-", wantErr: true},
		"Error_when_session_ID_is_invalid":   {sessionID: "invalid-session", wantErr: true},
		"Error_when_no_authmode_is_selected": {sessionID: "no auth mode", authMode: "-", wantErr: true},

		// broker errors
		"Error_when_selecting_invalid_auth_mode":                     {username: "sam_error", supportedUILayouts: []*authd.UILayout{requiredEntry}, wantErr: true},
		"Error_when_broker_does_not_have_validators_for_the_session": {username: "does not matter", noValidators: true, wantErr: true},

		/* Layout errors */
		"Error_when_returns_no_layout":                     {username: "sam_no_layout", supportedUILayouts: []*authd.UILayout{requiredEntry}, wantErr: true},
		"Error_when_returns_layout_with_no_type":           {username: "sam_no_layout_type", supportedUILayouts: []*authd.UILayout{requiredEntry}, wantErr: true},
		"Error_when_returns_layout_without_required_value": {username: "sam_missing_required_entry", supportedUILayouts: []*authd.UILayout{requiredEntry}, wantErr: true},
		"Error_when_returns_layout_with_unknown_field":     {username: "sam_unknown_field", supportedUILayouts: []*authd.UILayout{requiredEntry}, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pm := newPermissionManager(t, false) // Allow starting the session (current user considered root)
			client := newPamClient(t, nil, globalBrokerManager, &pm)

			switch tc.sessionID {
			case "invalid-session":
			case "-":
				tc.sessionID = ""
			default:
				id := startSession(t, client, tc.username)
				if tc.sessionID == "" {
					tc.sessionID = id
				}
			}

			switch tc.authMode {
			case "":
				tc.authMode = "some mode"
			case "-":
				tc.authMode = ""
			}

			// If the username does not have a sam_something, it means we don't care about the broker answer and we don't need the validators.
			if !tc.noValidators && strings.HasPrefix(tc.username, "sam_") {
				// We need to call GetAuthenticationModes to generate the layout validators on the broker.
				gamReq := &authd.GAMRequest{
					SessionId:          tc.sessionID,
					SupportedUiLayouts: tc.supportedUILayouts,
				}
				_, err := client.GetAuthenticationModes(context.Background(), gamReq)
				require.NoError(t, err, "Setup: failed to get authentication modes for tests")
			}

			// Now, set tests permissions for this use case
			permissions.Z_ForTests_SetCurrentUserAsRoot(&pm, !tc.currentUserNotRoot)

			samReq := &authd.SAMRequest{
				SessionId:            tc.sessionID,
				AuthenticationModeId: tc.authMode,
			}
			samResp, err := client.SelectAuthenticationMode(context.Background(), samReq)
			if tc.wantErr {
				require.Error(t, err, "SelectAuthenticationMode should return an error, but did not")
				return
			}
			require.NoError(t, err, "SelectAuthenticationMode should not return an error, but did")

			got := samResp.GetUiLayoutInfo()
			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestIsAuthenticated(t *testing.T) {
	tests := map[string]struct {
		sessionID  string
		existingDB string

		username           string
		secondCall         bool
		cancelFirstCall    bool
		localGroupsFile    string
		currentUserNotRoot bool

		// There is no wantErr as it's stored in the golden file.
	}{
		"Successfully_authenticate":                            {username: "success"},
		"Successfully_authenticate_if_first_call_is_canceled":  {username: "ia_second_call", secondCall: true, cancelFirstCall: true},
		"Denies_authentication_when_broker_times_out":          {username: "ia_timeout"},
		"Update_existing_DB_on_success":                        {username: "success", existingDB: "cache-with-user.db"},
		"Update_local_groups":                                  {username: "success_with_local_groups", localGroupsFile: "valid.group"},
		"Successfully_authenticate_user_with_uppercase":        {username: "SUCCESS"},
		"Successfully_authenticate_with_groups_with_uppercase": {username: "success_with_uppercase_groups"},

		// service errors
		"Error_when_not_root":           {username: "success", currentUserNotRoot: true},
		"Error_when_sessionID_is_empty": {sessionID: "-"},
		"Error_when_there_is_no_broker": {sessionID: "invalid-session"},
		"Error_when_user_is_locked":     {username: "locked", existingDB: "cache-with-locked-user.db"},

		// broker errors
		"Error_when_authenticating":                         {username: "ia_error"},
		"Error_on_empty_data_even_if_granted":               {username: "ia_empty_data"},
		"Error_when_broker_returns_invalid_access":          {username: "ia_invalid_access"},
		"Error_when_broker_returns_invalid_data":            {username: "ia_invalid_data"},
		"Error_when_broker_returns_invalid_userinfo":        {username: "ia_invalid_userinfo"},
		"Error_when_calling_second_time_without_cancelling": {username: "ia_second_call", secondCall: true},

		// local group error
		"Error_on_updating_local_groups_with_unexisting_file": {username: "success_with_local_groups", localGroupsFile: "does_not_exists.group"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.localGroupsFile == "" {
				t.Parallel()
			}

			var destGroupFile string
			if tc.localGroupsFile != "" {
				destGroupFile = localgroupstestutils.SetupGroupMock(t,
					filepath.Join(testutils.TestFamilyPath(t), tc.localGroupsFile))
			}

			dbDir := t.TempDir()
			if tc.existingDB != "" {
				err := db.Z_ForTests_CreateDBFromYAML(filepath.Join(testutils.TestFamilyPath(t), tc.existingDB), dbDir)
				require.NoError(t, err, "Setup: could not create database from testdata")
			}

			managerOpts := []users.Option{
				users.WithIDGenerator(&users.IDGeneratorMock{
					UIDsToGenerate: []uint32{1111},
					GIDsToGenerate: []uint32{22222, 33333, 44444},
				}),
			}

			m, err := users.NewManager(users.DefaultConfig, dbDir, managerOpts...)
			require.NoError(t, err, "Setup: could not create user manager")
			t.Cleanup(func() { _ = m.Stop() })
			pm := newPermissionManager(t, false) // Allow starting the session (current user considered root)
			client := newPamClient(t, m, globalBrokerManager, &pm)

			switch tc.sessionID {
			case "invalid-session":
			case "-":
				tc.sessionID = ""
			default:
				id := startSession(t, client, tc.username)
				if tc.sessionID == "" {
					tc.sessionID = id
				}
			}

			// Now, set tests permissions for this use case
			permissions.Z_ForTests_SetCurrentUserAsRoot(&pm, !tc.currentUserNotRoot)

			var firstCall, secondCall string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				defer close(done)
				iaReq := &authd.IARequest{
					SessionId:          tc.sessionID,
					AuthenticationData: &authd.IARequest_AuthenticationData{},
				}
				iaResp, err := client.IsAuthenticated(ctx, iaReq)
				firstCall = fmt.Sprintf("FIRST CALL:\n\taccess: %s\n\tmsg: %s\n\terr: %v\n",
					iaResp.GetAccess(),
					iaResp.GetMsg(),
					err,
				)
			}()
			// Give some time for the first call to block
			time.Sleep(time.Second)
			if tc.cancelFirstCall {
				cancel()
				time.Sleep(500 * time.Millisecond)
				<-done
			}

			if tc.secondCall {
				iaReq := &authd.IARequest{
					SessionId:          tc.sessionID,
					AuthenticationData: &authd.IARequest_AuthenticationData{},
				}
				iaResp, err := client.IsAuthenticated(context.Background(), iaReq)
				secondCall = fmt.Sprintf("SECOND CALL:\n\taccess: %s\n\tmsg: %s\n\terr: %v\n",
					iaResp.GetAccess(),
					iaResp.GetMsg(),
					err,
				)
			}
			<-done

			got := firstCall + secondCall
			got = permissions.Z_ForTests_IdempotentPermissionError(got)
			golden.CheckOrUpdate(t, got, golden.WithPath("IsAuthenticated"))

			// Check that all usernames in the database are lowercase
			allUsers, err := m.AllUsers()
			require.NoError(t, err, "Setup: failed to get users from manager")
			for _, u := range allUsers {
				require.Equal(t, strings.ToLower(u.Name), u.Name, "all usernames in the database should be lowercase")
			}

			// Check that all groups in the database are lowercase
			groups, err := m.AllGroups()
			require.NoError(t, err, "Setup: failed to get groups from manager")
			for _, group := range groups {
				require.Equal(t, strings.ToLower(group.Name), group.Name, "all groups in the database should be lowercase")
			}

			// Check that database has been updated too.
			gotDB, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Setup: failed to dump database for comparing")
			golden.CheckOrUpdate(t, gotDB, golden.WithPath("cache.db"))

			localgroupstestutils.RequireGroupFile(t, destGroupFile, golden.Path(t))
		})
	}
}

func TestIDGeneration(t *testing.T) {
	t.Parallel()
	usernamePrefix := t.Name()

	tests := map[string]struct {
		username string
	}{
		"Generate_ID": {username: "success"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			managerOpts := []users.Option{
				users.WithIDGenerator(&users.IDGeneratorMock{
					UIDsToGenerate: []uint32{1111},
					GIDsToGenerate: []uint32{22222},
				}),
			}

			m, err := users.NewManager(users.DefaultConfig, t.TempDir(), managerOpts...)
			require.NoError(t, err, "Setup: could not create user manager")
			t.Cleanup(func() { _ = m.Stop() })
			pm := newPermissionManager(t, false) // Allow starting the session (current user considered root)
			client := newPamClient(t, m, globalBrokerManager, &pm)

			sbResp, err := client.SelectBroker(context.Background(), &authd.SBRequest{
				BrokerId: mockBrokerGeneratedID,
				Username: usernamePrefix + testutils.IDSeparator + tc.username,
				Mode:     authd.SessionMode_LOGIN,
			})
			require.NoError(t, err, "Setup: failed to create session for tests")

			resp, err := client.IsAuthenticated(context.Background(), &authd.IARequest{SessionId: sbResp.GetSessionId()})
			require.NoError(t, err, "Setup: could not authenticate user")
			require.Equal(t, "granted", resp.GetAccess(), "Setup: authentication should be granted")

			gotDB, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Setup: failed to dump database for comparing")
			golden.CheckOrUpdate(t, gotDB, golden.WithPath("cache.db"))
		})
	}
}

func TestSetDefaultBrokerForUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username           string
		brokerID           string
		currentUserNotRoot bool

		wantErr bool
	}{
		"Set_default_broker_for_existing_user_with_no_broker":   {username: "usersetbroker"},
		"Update_default_broker_for_existing_user_with_a_broker": {username: "userupdatebroker"},
		"Username_is_case_insensitive":                          {username: "UserSetBroker"},

		"Error_when_setting_default_broker_to_local_broker": {username: "userlocalbroker", brokerID: brokers.LocalBrokerName, wantErr: true},
		"Error_when_not_root":                               {username: "usersetbroker", currentUserNotRoot: true, wantErr: true},
		"Error_when_username_is_empty":                      {wantErr: true},
		"Error_when_user_does_not_exist_":                   {username: "doesnotexist", wantErr: true},
		"Error_when_broker_does_not_exist":                  {username: "userwithbroker", brokerID: "does not exist", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbDir := t.TempDir()
			err := db.Z_ForTests_CreateDBFromYAML(filepath.Join(testutils.TestFamilyPath(t), "set-default-broker.db"), dbDir)
			require.NoError(t, err, "Setup: could not create database from testdata")

			m, err := users.NewManager(users.DefaultConfig, dbDir)
			require.NoError(t, err, "Setup: could not create user manager")
			t.Cleanup(func() { _ = m.Stop() })
			pm := newPermissionManager(t, tc.currentUserNotRoot)
			client := newPamClient(t, m, globalBrokerManager, &pm)

			if tc.brokerID == "" {
				tc.brokerID = mockBrokerGeneratedID
			}

			sdbfuReq := &authd.SDBFURequest{
				BrokerId: tc.brokerID,
				Username: tc.username,
			}
			_, err = client.SetDefaultBrokerForUser(context.Background(), sdbfuReq)
			if tc.wantErr {
				require.Error(t, err, "SetDefaultBrokerForUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "SetDefaultBrokerForUser should not return an error, but did")

			gpbResp, err := client.GetPreviousBroker(context.Background(), &authd.GPBRequest{Username: tc.username})
			require.NoError(t, err, "GetPreviousBroker should not return an error")
			require.Equal(t, tc.brokerID, gpbResp.GetPreviousBroker(), "SetDefaultBrokerForUser should set the default broker as expected")

			// Check that database has been updated too.
			gotDB, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Setup: failed to dump database for comparing")
			golden.CheckOrUpdate(t, gotDB, golden.WithPath("cache.db"))
		})
	}
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionID string

		username           string
		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_end_session": {username: "success"},

		"Error_when_not_root":             {username: "success", currentUserNotRoot: true, wantErr: true},
		"Error_when_sessionID_is_empty":   {sessionID: "-", wantErr: true},
		"Error_when_sessionID_is_invalid": {sessionID: "invalid-session", wantErr: true},
		"Error_when_ending_session":       {username: "es_error", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pm := newPermissionManager(t, false) // Allow starting the session (current user considered root)
			client := newPamClient(t, nil, globalBrokerManager, &pm)

			switch tc.sessionID {
			case "invalid-session":
			case "-":
				tc.sessionID = ""
			default:
				id := startSession(t, client, tc.username)
				if tc.sessionID == "" {
					tc.sessionID = id
				}
			}

			// Now, set tests permissions for this use case
			permissions.Z_ForTests_SetCurrentUserAsRoot(&pm, !tc.currentUserNotRoot)

			esReq := &authd.ESRequest{
				SessionId: tc.sessionID,
			}
			_, err := client.EndSession(context.Background(), esReq)
			if tc.wantErr {
				require.Error(t, err, "EndSession should return an error, but did not")
				return
			}
			require.NoError(t, err, "EndSession should not return an error, but did")
		})
	}
}

// initBrokers starts dbus mock brokers on the system bus. It returns its config path.
func initBrokers() (brokerConfigPath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "authd-internal-pam-tests-")
	if err != nil {
		return "", nil, err
	}

	brokersConfPath := filepath.Join(tmpDir, "etc", "authd", "broker.d")
	if err = os.MkdirAll(brokersConfPath, 0750); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, err
	}
	_, brokerCleanup, err := testutils.StartBusBrokerMock(brokersConfPath, "BrokerMock")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, err
	}

	return brokersConfPath, func() {
		brokerCleanup()
		_ = os.RemoveAll(tmpDir)
	}, nil
}

// newPAMClient returns a new GRPC PAM client for tests connected to brokerManager with the given database and
// permissionmanager.
// If the one passed is nil, this function will create the database and close it upon test teardown.
func newPamClient(t *testing.T, m *users.Manager, brokerManager *brokers.Manager, pm *permissions.Manager) (client authd.PAMClient) {
	t.Helper()

	// socket path is limited in length.
	tmpDir, err := os.MkdirTemp("", "authd-socket-dir")
	require.NoError(t, err, "Setup: could not setup temporary socket dir path")
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "authd.sock")

	lis, err := net.Listen("unix", socketPath)
	require.NoError(t, err, "Setup: could not create unix socket")

	if m == nil {
		m, err = users.NewManager(users.DefaultConfig, t.TempDir())
		require.NoError(t, err, "Setup: could not create user manager")
		t.Cleanup(func() { _ = m.Stop() })
	}

	service := pam.NewService(context.Background(), m, brokerManager, pm)

	grpcServer := grpc.NewServer(permissions.WithUnixPeerCreds(), grpc.ChainUnaryInterceptor(enableCheckGlobalAccess(service), errmessages.RedactErrorInterceptor))
	authd.RegisterPAMServer(grpcServer, service)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		<-done
	})

	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(errmessages.FormatErrorMessage))
	require.NoError(t, err, "Setup: Could not connect to gRPC server")

	t.Cleanup(func() { _ = conn.Close() }) // We don't care about the error on cleanup

	return authd.NewPAMClient(conn)
}

// newPermissionManager factors out permission manager creation for tests.
// this permission manager can then be tweaked for mimicking currentUser considered as root not.
func newPermissionManager(t *testing.T, currentUserNotRoot bool) permissions.Manager {
	t.Helper()

	var opts = []permissions.Option{}
	if !currentUserNotRoot {
		opts = append(opts, permissions.Z_ForTests_WithCurrentUserAsRoot())
	}
	return permissions.New(opts...)
}

// enableCheckGlobalAccess returns the middleware hooking up in CheckGlobalAccess for the given service.
func enableCheckGlobalAccess(s pam.Service) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := s.CheckGlobalAccess(ctx, info.FullMethod); err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// getMockBrokerGeneratedID returns the generated ID for the mock broker.
func getMockBrokerGeneratedID(brokerManager *brokers.Manager) (string, error) {
	for _, b := range brokerManager.AvailableBrokers() {
		if b.Name != "BrokerMock" {
			continue
		}
		return b.ID, nil
	}
	return "", errors.New("Setup: could not find generated broker mock ID in the broker manager list")
}

// startSession is a helper that starts a session on the mock broker.
func startSession(t *testing.T, client authd.PAMClient, username string) string {
	t.Helper()

	// Prefixes the username to avoid concurrency issues.
	username = t.Name() + testutils.IDSeparator + username

	sbResp, err := client.SelectBroker(context.Background(), &authd.SBRequest{
		BrokerId: mockBrokerGeneratedID,
		Username: username,
		Mode:     authd.SessionMode_LOGIN,
	})
	require.NoError(t, err, "Setup: failed to create session for tests")
	return sbResp.GetSessionId()
}

// setupGlobalBrokerMock creates and points to a test-wide system bus, registering the mock broker on it.
func setupGlobalBrokerMock() (cleanup func(), err error) {
	cleanup = func() {}

	// Start system bus mock.
	busCleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		return cleanup, err
	}
	cleanup = busCleanup

	// Start brokers mock over dbus.
	brokersConfPath, brokerCleanup, err := initBrokers()
	if err != nil {
		return cleanup, err
	}

	cleanup = func() {
		brokerCleanup()
		busCleanup()
	}

	// Get manager shared across grpc services.
	globalBrokerManager, err = brokers.NewManager(context.Background(), brokersConfPath, nil)
	if err != nil {
		return cleanup, err
	}
	mockBrokerGeneratedID, err = getMockBrokerGeneratedID(globalBrokerManager)
	if err != nil {
		return cleanup, err
	}

	return cleanup, nil
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	userslocking.Z_ForTests_OverrideLocking()
	defer userslocking.Z_ForTests_RestoreLocking()

	cleanup, err := setupGlobalBrokerMock()
	if err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
