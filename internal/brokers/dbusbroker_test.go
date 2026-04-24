package brokers

import (
	"context"
	"encoding/xml"
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/stretchr/testify/require"
)

// mockBusObject implements dbus.BusObject for testing dbusBroker internals.
type mockBusObject struct {
	introspectXML string
	callErr       error

	lastCalledMethod string
}

func (m *mockBusObject) Call(method string, flags dbus.Flags, args ...interface{}) *dbus.Call {
	m.lastCalledMethod = method
	if m.callErr != nil {
		return &dbus.Call{Err: m.callErr}
	}
	return &dbus.Call{Body: []interface{}{m.introspectXML}}
}
func (m *mockBusObject) CallWithContext(_ context.Context, method string, flags dbus.Flags, args ...interface{}) *dbus.Call {
	return m.Call(method, flags, args...)
}

// The following methods are not used in tests but are required to satisfy the dbus.BusObject interface.
func (m *mockBusObject) Go(method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
	return nil
}
func (m *mockBusObject) GoWithContext(_ context.Context, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
	return nil
}
func (m *mockBusObject) AddMatchSignal(iface, member string, options ...dbus.MatchOption) *dbus.Call {
	return nil
}
func (m *mockBusObject) RemoveMatchSignal(iface, member string, options ...dbus.MatchOption) *dbus.Call {
	return nil
}
func (m *mockBusObject) GetProperty(p string) (dbus.Variant, error) {
	return dbus.Variant{}, nil
}
func (m *mockBusObject) StoreProperty(p string, value interface{}) error { return nil }
func (m *mockBusObject) SetProperty(p string, v interface{}) error       { return nil }
func (m *mockBusObject) Destination() string                             { return "" }
func (m *mockBusObject) Path() dbus.ObjectPath                           { return "/" }

// introspectionXML generates introspection XML for the given interface names.
func introspectionXML(interfaces ...string) string {
	node := introspect.Node{Name: "/test"}
	for _, iface := range interfaces {
		node.Interfaces = append(node.Interfaces, introspect.Interface{Name: iface})
	}
	data, _ := xml.Marshal(node)
	return string(data)
}

func TestGetInterface(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		interfaces []string
		callErr    error

		wantInterface string
		wantErr       bool
	}{
		"Single_base_interface": {
			interfaces:    []string{"com.ubuntu.authd.Broker"},
			wantInterface: "com.ubuntu.authd.Broker",
		},
		"Returns_highest_supported_version": {
			interfaces:    []string{"com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker3"},
			wantInterface: "com.ubuntu.authd.Broker2",
		},
		"Versioned_interfaces_with_unversioned": {
			interfaces:    []string{"com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker", "com.ubuntu.authd.Broker1"},
			wantInterface: "com.ubuntu.authd.Broker2",
		},
		"Unrelated_interfaces_are_excluded": {
			interfaces: []string{"com.ubuntu.authd.Broker2", "org.freedesktop.DBus.Introspectable",
				"com.ubuntu.authd.Broker1", "com.ubuntu.authd.BrokerUnrelated"},
			wantInterface: "com.ubuntu.authd.Broker2",
		},
		"Single_versioned_interface": {
			interfaces:    []string{"com.ubuntu.authd.Broker1"},
			wantInterface: "com.ubuntu.authd.Broker1",
		},

		"Error_when_no_supported_interfaces": {
			interfaces: []string{},
			wantErr:    true,
		},
		"Error_when_all_interfaces_above_latest_version": {
			interfaces: []string{"com.ubuntu.authd.Broker3", "com.ubuntu.authd.Broker4"},
			wantErr:    true,
		},
		"Error_when_introspect_fails": {
			callErr: fmt.Errorf("connection refused"),
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mock := &mockBusObject{callErr: tc.callErr}
			if tc.callErr == nil {
				mock.introspectXML = introspectionXML(tc.interfaces...)
			}

			got, err := getInterface(mock)
			if tc.wantErr {
				require.Error(t, err, "getInterface should return an error, but did not")
				return
			}
			require.NoError(t, err, "getInterface should not return an error, but did")
			require.Equal(t, tc.wantInterface, got, "getInterface returned unexpected interface")
		})
	}
}

func TestDbusBrokerCallUsesInterface(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		iface            string
		wantMethodCalled string
	}{
		"Uses_base_interface": {
			iface:            "com.ubuntu.authd.Broker",
			wantMethodCalled: "com.ubuntu.authd.Broker.TestMethod",
		},
		"Uses_versioned_interface": {
			iface:            "com.ubuntu.authd.Broker2",
			wantMethodCalled: "com.ubuntu.authd.Broker2.TestMethod",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mock := &mockBusObject{}
			b := dbusBroker{
				name:       "test",
				iface:      tc.iface,
				dbusObject: mock,
			}

			_, err := b.call(context.Background(), "TestMethod")
			require.NoError(t, err, "call should not return a D-Bus error")
			require.Equal(t, tc.wantMethodCalled, mock.lastCalledMethod)
		})
	}
}
