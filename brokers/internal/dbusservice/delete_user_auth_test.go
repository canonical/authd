package dbusservice

import (
	"errors"
	"os"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeDeleteUser(t *testing.T) {
	lookupErr := errors.New("lookup failed")
	tests := []struct {
		name            string
		sender          dbus.Sender
		uid             uint32
		lookupErr       error
		missingLookup   bool
		wantErrorName   string
		wantLookupCalls int
	}{
		{
			name:            "root caller is allowed",
			sender:          ":1.10",
			uid:             0,
			wantLookupCalls: 1,
		},
		{
			name:            "non-root caller is denied",
			sender:          ":1.10",
			uid:             1000,
			wantErrorName:   dbusAccessDeniedErrorName,
			wantLookupCalls: 1,
		},
		{
			name:          "missing sender is denied",
			wantErrorName: dbusAccessDeniedErrorName,
		},
		{
			name:            "caller lookup failure is returned",
			sender:          ":1.10",
			lookupErr:       lookupErr,
			wantErrorName:   "org.freedesktop.DBus.Error.Failed",
			wantLookupCalls: 1,
		},
		{
			name:          "missing lookup fails closed",
			sender:        ":1.10",
			missingLookup: true,
			wantErrorName: "org.freedesktop.DBus.Error.Failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookupCalls := 0
			lookup := func(sender dbus.Sender) (uint32, error) {
				lookupCalls++
				require.Equal(t, tc.sender, sender)
				return tc.uid, tc.lookupErr
			}
			if tc.missingLookup {
				lookup = nil
			}

			dbusErr := authorizeRootCaller(tc.sender, lookup, "DeleteUser")
			if tc.wantErrorName == "" {
				require.Nil(t, dbusErr)
			} else {
				require.NotNil(t, dbusErr)
				require.Equal(t, tc.wantErrorName, dbusErr.Name)
			}

			require.Equal(t, tc.wantLookupCalls, lookupCalls)
		})
	}
}

func TestNewSessionRejectsUntrustedProviderID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root D-Bus caller")
	}
	conn, err := dbus.ConnectSessionBus()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	service := &Service{conn: conn}
	iface := Interface{service: service}
	_, _, dbusErr := iface.NewSession(dbus.Sender(conn.Names()[0]), "owner", "en", "login", "attacker-id")
	require.NotNil(t, dbusErr)
	require.Equal(t, dbusAccessDeniedErrorName, dbusErr.Name)
	ifaceV4 := InterfaceV4{Interface: &iface}
	_, _, dbusErr = ifaceV4.NewSession(dbus.Sender(conn.Names()[0]), "provider-name", "en", "login", "", "owner")
	require.NotNil(t, dbusErr)
	require.Equal(t, dbusAccessDeniedErrorName, dbusErr.Name, "even an ID-less Unix-name binding requires root")
}
