package brokers_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

var (
	brokerConfFixtures = filepath.Join("testdata", "broker.d")
)

func TestNewManager(t *testing.T) {
	tests := map[string]struct {
		brokerConfigDir   string
		configuredBrokers []string
		noBus             bool

		wantErr bool
	}{
		"Creates_all_brokers_when_config_dir_has_only_valid_brokers":                 {brokerConfigDir: "valid_brokers"},
		"Creates_without_autodiscovery_when_configuredBrokers_is_set":                {brokerConfigDir: "valid_brokers", configuredBrokers: []string{"valid_2.conf"}},
		"Creates_only_correct_brokers_when_config_dir_has_valid_and_invalid_brokers": {brokerConfigDir: "mixed_brokers"},
		"Creates_only_local_broker_when_config_dir_has_only_invalid_ones":            {brokerConfigDir: "invalid_brokers"},
		"Creates_only_local_broker_when_config_dir_does_not_exist":                   {brokerConfigDir: "does/not/exist"},
		"Creates_manager_even_if_broker_is_not_exported_on_dbus":                     {brokerConfigDir: "not_on_bus"},

		"Ignores_broker_configuration_file_not_ending_with_.conf": {brokerConfigDir: "some_ignored_brokers"},
		"Ignores_any_unknown_sections_and_fields":                 {brokerConfigDir: "extra_fields"},

		"Error_when_can't_connect_to_system_bus": {brokerConfigDir: "valid_brokers", noBus: true, wantErr: true},
		"Error_when_broker_config_dir_is_a_file": {brokerConfigDir: "file_config_dir", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.noBus {
				t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "/dev/null")
			}

			got, err := brokers.NewManager(context.Background(), filepath.Join(brokerConfFixtures, tc.brokerConfigDir), tc.configuredBrokers)
			if tc.wantErr {
				require.Error(t, err, "NewManager should return an error, but did not")
				return
			}
			require.NoError(t, err, "NewManager should not return an error, but did")

			// Grab the list of broker names from the manager to use as golden file.
			var brokers []string
			for _, broker := range got.AvailableBrokers() {
				brokers = append(brokers, broker.Name)
			}

			golden.CheckOrUpdateYAML(t, brokers)
		})
	}
}

func TestSetDefaultBrokerForUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		exists bool

		wantErr bool
	}{
		"Successfully_assigns_existent_broker_to_user": {exists: true},

		"Error_when_broker_does_not_exist": {wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, err := brokers.NewManager(context.Background(), filepath.Join(brokerConfFixtures, "mixed_brokers"), nil)
			require.NoError(t, err, "Setup: could not create manager")

			want := m.AvailableBrokers()[0]
			if !tc.exists {
				want.ID = "does not exist"
			}

			err = m.SetDefaultBrokerForUser(want.ID, "user")
			if tc.wantErr {
				require.Error(t, err, "SetDefaultBrokerForUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "SetDefaultBrokerForUser should not return an error, but did")

			got := m.BrokerForUser("user")
			require.Equal(t, want.ID, got.ID, "SetDefaultBrokerForUser should have assiged the expected broker, but did not")
		})
	}
}

func TestBrokerForUser(t *testing.T) {
	t.Parallel()

	m, err := brokers.NewManager(context.Background(), filepath.Join(brokerConfFixtures, "valid_brokers"), nil)
	require.NoError(t, err, "Setup: could not create manager")

	err = m.SetDefaultBrokerForUser(brokers.LocalBrokerName, "user")
	require.NoError(t, err, "Setup: could not set default broker")

	// Broker for user should return the assigned broker
	got := m.BrokerForUser("user")
	require.Equal(t, brokers.LocalBrokerName, got.ID, "BrokerForUser should return the assigned broker, but did not")

	// Broker for user should return nil if no broker is assigned
	got = m.BrokerForUser("no_broker")
	require.Nil(t, got, "BrokerForUser should return nil if no broker is assigned, but did not")
}

func TestBrokerFromSessionID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionID string

		wantBrokerID string
		wantErr      bool
	}{
		"Successfully_returns_expected_broker":       {sessionID: "success"},
		"Returns_local_broker_if_sessionID_is_empty": {wantBrokerID: brokers.LocalBrokerName},

		"Error_if_broker_does_not_exist": {sessionID: "does not exist", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			brokersConfPath := t.TempDir()
			b := newBrokerForTests(t, brokersConfPath, "")
			m, err := brokers.NewManager(context.Background(), brokersConfPath, nil)
			require.NoError(t, err, "Setup: could not create manager")

			if tc.sessionID == "success" {
				// We need to use the ID generated by the mananger.
				for _, broker := range m.AvailableBrokers() {
					if broker.Name != b.Name {
						continue
					}
					b.ID = broker.ID
					break
				}
				tc.wantBrokerID = b.ID
				m.SetBrokerForSession(&b, tc.sessionID)
			}

			got, err := m.BrokerFromSessionID(tc.sessionID)
			if tc.wantErr {
				require.Error(t, err, "BrokerFromSessionID should return an error, but did not")
				return
			}
			require.NoError(t, err, "BrokerFromSessionID should not return an error, but did")
			require.Equal(t, tc.wantBrokerID, got.ID, "BrokerFromSessionID should return the expected broker, but did not")
		})
	}
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		brokerID    string
		username    string
		sessionMode string

		configuredBrokers []string
		unavailableBroker bool

		wantErr bool
	}{
		"Successfully_start_a_new_auth_session":                    {username: "success"},
		"Successfully_start_a_new_passwd_session":                  {username: "success", sessionMode: auth.SessionModeChangePassword},
		"Successfully_start_a_new_session_with_the_correct_broker": {username: "success", configuredBrokers: []string{t.Name() + "_Broker1.conf", t.Name() + "_Broker2.conf"}},

		"Error_when_broker_does_not_exist":           {brokerID: "does_not_exist", wantErr: true},
		"Error_when_broker_does_not_provide_an_ID":   {username: "ns_no_id", wantErr: true},
		"Error_when_starting_a_new_session":          {username: "ns_error", wantErr: true},
		"Error_when_broker_is_not_available_on_dbus": {unavailableBroker: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			brokersConfPath := t.TempDir()
			if tc.configuredBrokers == nil {
				tc.configuredBrokers = []string{strings.ReplaceAll(t.Name(), "/", "_") + ".conf"}
			}

			wantBroker := newBrokerForTests(t, brokersConfPath, tc.configuredBrokers[0])
			if len(tc.configuredBrokers) > 1 {
				for _, name := range tc.configuredBrokers[1:] {
					newBrokerForTests(t, brokersConfPath, name)
				}
			}

			if tc.unavailableBroker {
				// We need to manually configure the broker without exporting it on the bus.
				content, err := os.ReadFile(filepath.Join(brokerConfFixtures, "not_on_bus", "not_on_bus.conf"))
				require.NoError(t, err, "Setup: could not read broker configuration file")
				err = os.WriteFile(filepath.Join(brokersConfPath, "not_on_bus.conf"), content, 0600)
				require.NoError(t, err, "Setup: could not write broker configuration file")
				wantBroker = brokers.Broker{Name: "OfflineBroker"}
				tc.configuredBrokers = nil
			}

			m, err := brokers.NewManager(context.Background(), brokersConfPath, tc.configuredBrokers)
			require.NoError(t, err, "Setup: could not create manager")

			if tc.brokerID == "" {
				// We need to use the ID generated by the mananger.
				var brokerFound bool
				for _, broker := range m.AvailableBrokers() {
					if broker.Name != wantBroker.Name {
						continue
					}
					wantBroker.ID = broker.ID
					brokerFound = true
				}
				require.True(t, brokerFound, "Setup: could not find the test broker in the manager")
				tc.brokerID = wantBroker.ID
			}

			if tc.sessionMode == "" {
				tc.sessionMode = "auth"
			}

			gotID, gotEKey, err := m.NewSession(tc.brokerID, tc.username, "some_lang", tc.sessionMode)
			if tc.wantErr {
				require.Error(t, err, "NewSession should return an error, but did not")
				return
			}
			require.NoError(t, err, "NewSession should not return an error, but did")

			// Replaces the autogenerated part of the ID with a placeholder before saving the file.
			gotStr := fmt.Sprintf("ID: %s\nEncryption Key: %s\n", strings.ReplaceAll(gotID, wantBroker.ID, "BROKER_ID"), gotEKey)
			golden.CheckOrUpdate(t, gotStr)

			gotBroker, err := m.BrokerFromSessionID(gotID)
			require.NoError(t, err, "NewSession should have assigned a broker for the session, but did not")
			require.Equal(t, wantBroker.ID, gotBroker.ID, "BrokerFromSessionID should have assigned the expected broker for the session, but did not")
		})
	}
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		brokerID  string
		sessionID string

		configuredBrokers []string

		wantErr bool
	}{
		"Successfully_end_session":                       {sessionID: "success"},
		"Successfully_end_session_on_the_correct_broker": {sessionID: "success", configuredBrokers: []string{t.Name() + "_Broker1", t.Name() + "_Broker2"}},

		"Error_when_broker_does_not_exist": {brokerID: "does not exist", sessionID: "dont matter", wantErr: true},
		"Error_when_ending_session":        {sessionID: "es_error", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			brokersConfPath := t.TempDir()
			if tc.configuredBrokers == nil {
				tc.configuredBrokers = []string{strings.ReplaceAll(t.Name(), "/", "_")}
			}

			wantBroker := newBrokerForTests(t, brokersConfPath, tc.configuredBrokers[0])
			if len(tc.configuredBrokers) > 1 {
				for _, name := range tc.configuredBrokers[1:] {
					newBrokerForTests(t, brokersConfPath, name)
				}
			}

			m, err := brokers.NewManager(context.Background(), brokersConfPath, tc.configuredBrokers)
			require.NoError(t, err, "Setup: could not create manager")

			if tc.brokerID != "does not exist" {
				m.SetBrokerForSession(&wantBroker, tc.sessionID)
			}

			err = m.EndSession(tc.sessionID)
			if tc.wantErr {
				require.Error(t, err, "EndSession should return an error, but did not")
				return
			}
			require.NoError(t, err, "EndSession should not return an error, but did")
			_, err = m.BrokerFromSessionID(tc.sessionID)
			require.Error(t, err, "EndSession should have removed the broker from the active transactions, but did not")
		})
	}
}

func TestStartAndEndSession(t *testing.T) {
	t.Parallel()

	brokersConfPath := t.TempDir()
	b1 := newBrokerForTests(t, brokersConfPath, t.Name()+"_Broker1.conf")
	b2 := newBrokerForTests(t, brokersConfPath, t.Name()+"_Broker2.conf")

	m, err := brokers.NewManager(context.Background(), brokersConfPath, []string{b1.Name + ".conf", b2.Name + ".conf"})
	require.NoError(t, err, "Setup: could not create manager")

	// Fetches the broker IDs
	for _, broker := range m.AvailableBrokers() {
		switch broker.Name {
		case b1.Name:
			b1.ID = broker.ID
		case b2.Name:
			b2.ID = broker.ID
		}
	}

	/* Starting the sessions */
	var firstID, firstKey, secondID, secondKey *string
	var firstErr, secondErr *error
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		id, key, err := m.NewSession(b1.ID, "user1", "some_lang", "auth")
		firstID, firstKey, firstErr = &id, &key, &err
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		id, key, err := m.NewSession(b2.ID, "user2", "some_lang", "auth")
		secondID, secondKey, secondErr = &id, &key, &err
	}()
	wg.Wait()

	require.NoError(t, *firstErr, "First NewSession should not return an error, but did")
	require.NoError(t, *secondErr, "Second NewSession should not return an error, but did")

	require.Equal(t, b1.ID+"-"+testutils.GenerateSessionID("user1"),
		*firstID, "First NewSession should return the expected session ID, but did not")
	require.Equal(t, testutils.GenerateEncryptionKey(b1.Name),
		*firstKey, "First NewSession should return the expected encryption key, but did not")
	require.Equal(t, b2.ID+"-"+testutils.GenerateSessionID("user2"),
		*secondID, "Second NewSession should return the expected session ID, but did not")
	require.Equal(t, testutils.GenerateEncryptionKey(b2.Name),
		*secondKey, "Second NewSession should return the expected encryption key, but did not")

	assignedBroker, err := m.BrokerFromSessionID(*firstID)
	require.NoError(t, err, "First NewSession should have assigned a broker for the session, but did not")
	require.Equal(t, b1.Name, assignedBroker.Name, "First NewSession should have assigned the expected broker for the session, but did not")
	assignedBroker, err = m.BrokerFromSessionID(*secondID)
	require.NoError(t, err, "Second NewSession should have assigned a broker for the session, but did not")
	require.Equal(t, b2.Name, assignedBroker.Name, "Second NewSession should have assigned the expected broker for the session, but did not")

	/* Ending the sessions */
	wg.Add(1)
	go func() {
		defer wg.Done()
		*firstErr = m.EndSession(*firstID)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		*secondErr = m.EndSession(*secondID)
	}()
	wg.Wait()

	require.NoError(t, *firstErr, "First EndSession should not return an error, but did")
	require.NoError(t, *secondErr, "Second EndSession should not return an error, but did")

	_, err = m.BrokerFromSessionID(*firstID)
	require.Error(t, err, "First EndSession should have removed the broker for the session, but did not")

	_, err = m.BrokerFromSessionID(*secondID)
	require.Error(t, err, "Second EndSession should have removed the broker for the session, but did not")
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	// Start system bus mock.
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
