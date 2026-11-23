package systemd_test

import (
	"path/filepath"
	"testing"

	"github.com/canonical/authd/internal/systemd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

const testUnitPath dbus.ObjectPath = "/org/freedesktop/systemd1/unit/authd_2eservice"

type systemdManager struct{}

func (systemdManager) GetUnitByPID(uint32) (dbus.ObjectPath, *dbus.Error) {
	return testUnitPath, nil
}

type systemdProperties struct{}

func (systemdProperties) Get(string, string) (dbus.Variant, *dbus.Error) {
	return dbus.MakeVariant(uint32(0)), nil
}

func TestDescriptorStoreConnectionOutlivesConstructor(t *testing.T) {
	address, stop, err := testutils.StartBusMock()
	require.NoError(t, err)
	t.Cleanup(stop)
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "notify.socket"))
	t.Setenv("FDSTORE", "8193")
	conn, err := testutils.GetSystemBusConnection(t)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	reply, err := conn.RequestName("org.freedesktop.systemd1", dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
	require.NoError(t, conn.Export(systemdManager{}, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"))
	require.NoError(t, conn.Export(systemdProperties{}, testUnitPath, "org.freedesktop.DBus.Properties"))

	store, err := systemd.NewDescriptorStore()
	require.NoError(t, err)
	require.NotNil(t, store)
	t.Cleanup(func() { _ = store.Close() })
	// Put checks the descriptor count before sending a notification. The
	// nonexistent socket must be the failure, not a closed D-Bus connection.
	for range 2 {
		err := store.Put(systemd.StorePrefix + "owner")
		require.ErrorContains(t, err, "no such file or directory")
	}
}
