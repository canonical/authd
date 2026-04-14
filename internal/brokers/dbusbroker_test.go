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

func TestDetectAvailableInterfaces(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		interfaces []string
		callErr    error

		wantInterfaces []string
		wantErr        bool
	}{
		"Single_base_interface": {
			interfaces:     []string{"com.ubuntu.authd.Broker"},
			wantInterfaces: []string{"com.ubuntu.authd.Broker"},
		},
		"Multiple_versioned_interfaces_sorted_ascending": {
			interfaces:     []string{"com.ubuntu.authd.Broker3", "com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2"},
			wantInterfaces: []string{"com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker3"},
		},
		"Versioned_interfaces_with_unversioned": {
			interfaces:     []string{"com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker", "com.ubuntu.authd.Broker1"},
			wantInterfaces: []string{"com.ubuntu.authd.Broker", "com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2"},
		},
		"Unrelated_interfaces_are_excluded": {
			interfaces: []string{"com.ubuntu.authd.Broker2", "org.freedesktop.DBus.Introspectable",
				"com.ubuntu.authd.Broker1", "com.ubuntu.authd.BrokerUnrelated"},
			wantInterfaces: []string{"com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2"},
		},
		"Single_versioned_interface": {
			interfaces:     []string{"com.ubuntu.authd.Broker1"},
			wantInterfaces: []string{"com.ubuntu.authd.Broker1"},
		},
		"No_interfaces": {
			interfaces:     []string{},
			wantInterfaces: nil,
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

			got, err := detectAvailableInterfaces(mock)
			if tc.wantErr {
				require.Error(t, err, "detectAvailableInterfaces should return an error, but did not")
				return
			}
			require.NoError(t, err, "detectAvailableInterfaces should not return an error, but did")
			require.Equal(t, tc.wantInterfaces, got, "detectAvailableInterfaces returned unexpected interfaces")
		})
	}
}

func TestDbusBrokerCallUsesLatestInterface(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		interfaces []string

		wantMethodCalled string
		wantErr          bool
	}{
		"Uses_only_interface": {
			interfaces:       []string{"com.ubuntu.authd.Broker"},
			wantMethodCalled: "com.ubuntu.authd.Broker.TestMethod",
		},
		"Uses_latest_interface": {
			interfaces:       []string{"com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker3"},
			wantMethodCalled: "com.ubuntu.authd.Broker3.TestMethod",
		},

		"Error_when_no_interfaces_available": {
			interfaces: []string{},
			wantErr:    true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mock := &mockBusObject{}
			b := dbusBroker{
				name:       "test",
				interfaces: tc.interfaces,
				dbusObject: mock,
			}

			_, err := b.call(context.Background(), "TestMethod")
			if tc.wantErr {
				require.Error(t, err, "call should return an error, but did not")
				return
			}
			require.NoError(t, err, "call should not return a D-Bus error")
			require.Equal(t, tc.wantMethodCalled, mock.lastCalledMethod)
		})
	}
}
