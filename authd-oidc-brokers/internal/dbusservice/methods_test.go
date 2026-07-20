package dbusservice_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/dbusservice"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

var defaultIssuerURL string

// newInterfaceForTests creates a v3 dbusservice Interface backed by a real broker
// connected to the mock provider server, so the wrappers exercise the full path.
func newInterfaceForTests(t *testing.T) *dbusservice.Interface {
	t.Helper()

	confPath := filepath.Join(t.TempDir(), "broker.conf")
	require.NoError(t, os.WriteFile(confPath, []byte(`[oidc]
issuer = `+defaultIssuerURL+`
client_id = test-client-id

[flows]
entra_auth = false
`), 0600), "Setup: writing broker config should not fail")

	cfg := broker.Config{ConfigFile: confPath, DataDir: t.TempDir()}
	b, err := broker.New(cfg, broker.LatestAPIVersion)
	require.NoError(t, err, "Setup: creating the broker should not fail")

	return dbusservice.NewInterfaceForTests(b)
}

var supportedUILayouts = []map[string]string{
	{"type": "form", "entry": "chars_password"},
	{"type": "qrcode", "wait": "true"},
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	id, key, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login, "provider-id")
	require.Nil(t, dbusErr, "NewSession should not return a D-Bus error")
	require.NotEmpty(t, id, "NewSession should return a session ID")
	require.NotEmpty(t, key, "NewSession should return an encryption key")

	_, _, dbusErr = iface.NewSession("", "lang", sessionmode.Login, "")
	require.NotNil(t, dbusErr, "NewSession with an empty username should return a D-Bus error")
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	id, _, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login, "")
	require.Nil(t, dbusErr, "NewSession should not return a D-Bus error")

	modes, dbusErr := iface.GetAuthenticationModes(id, supportedUILayouts)
	require.Nil(t, dbusErr, "GetAuthenticationModes should not return a D-Bus error")
	require.NotEmpty(t, modes, "GetAuthenticationModes should return modes")

	_, dbusErr = iface.GetAuthenticationModes("invalid-session", supportedUILayouts)
	require.NotNil(t, dbusErr, "GetAuthenticationModes with an invalid session should return a D-Bus error")
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	id, _, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login, "")
	require.Nil(t, dbusErr, "NewSession should not return a D-Bus error")

	modes, dbusErr := iface.GetAuthenticationModes(id, supportedUILayouts)
	require.Nil(t, dbusErr, "GetAuthenticationModes should not return a D-Bus error")

	_, dbusErr = iface.SelectAuthenticationMode(id, modes[0]["id"])
	require.Nil(t, dbusErr, "SelectAuthenticationMode should not return a D-Bus error")

	_, dbusErr = iface.SelectAuthenticationMode(id, "invalid-mode")
	require.NotNil(t, dbusErr, "SelectAuthenticationMode with an invalid mode should return a D-Bus error")
}

func TestIsAuthenticated(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	_, _, dbusErr := iface.IsAuthenticated("invalid-session", "{}")
	require.NotNil(t, dbusErr, "IsAuthenticated with an invalid session should return a D-Bus error")
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	id, _, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login, "")
	require.Nil(t, dbusErr, "NewSession should not return a D-Bus error")

	require.Nil(t, iface.EndSession(id), "EndSession should not return a D-Bus error")
	require.NotNil(t, iface.EndSession("invalid-session"), "EndSession with an invalid session should return a D-Bus error")
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	id, _, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login, "")
	require.Nil(t, dbusErr, "NewSession should not return a D-Bus error")

	require.Nil(t, iface.CancelIsAuthenticated(id), "CancelIsAuthenticated should not return a D-Bus error")
}

func TestUserPreCheck(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	_, dbusErr := iface.UserPreCheck("user@example.com")
	require.Nil(t, dbusErr, "UserPreCheck should not return a D-Bus error")
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	iface := newInterfaceForTests(t)

	require.Nil(t, iface.DeleteUser("user@example.com", "provider-id"), "DeleteUser should not return a D-Bus error")
	require.NotNil(t, iface.DeleteUser("invalid/../user", ""), "DeleteUser with an invalid username should return a D-Bus error")
}

func TestInterfaceV2(t *testing.T) {
	t.Parallel()

	iface := dbusservice.NewInterfaceV2ForTests(newInterfaceForTests(t).Broker())

	id, _, dbusErr := iface.NewSession("user@example.com", "lang", sessionmode.Login)
	require.Nil(t, dbusErr, "NewSession (v2) should not return a D-Bus error")
	require.NotEmpty(t, id, "NewSession (v2) should return a session ID")

	_, _, dbusErr = iface.NewSession("", "lang", sessionmode.Login)
	require.NotNil(t, dbusErr, "NewSession (v2) with an empty username should return a D-Bus error")

	require.Nil(t, iface.DeleteUser("user@example.com"), "DeleteUser (v2) should not return a D-Bus error")
	require.NotNil(t, iface.DeleteUser("invalid/../user"), "DeleteUser (v2) with an invalid username should return a D-Bus error")
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	var cleanup func()
	defaultIssuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
