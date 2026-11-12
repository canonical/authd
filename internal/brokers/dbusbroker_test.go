package brokers

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/log"
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

type ownerChangeBrokerMock struct{}

func (ownerChangeBrokerMock) NewSession(username, lang, mode, providerID string) (string, string, *dbus.Error) {
	return providerID, username, nil
}

func (ownerChangeBrokerMock) DeleteUser(username, providerID string) *dbus.Error {
	if username != "renamed-user" || providerID != "provider-id" {
		return dbus.MakeFailedError(fmt.Errorf("unexpected deletion identity: %q, %q", username, providerID))
	}
	return nil
}

func TestInterfaceRefreshesWhenBrokerOwnerChanges(t *testing.T) {
	t.Parallel()

	const busName = "com.ubuntu.authd.OwnerChangeTest"
	const objectPath = dbus.ObjectPath("/com/ubuntu/authd/OwnerChangeTest")
	connect := func() *dbus.Conn {
		t.Helper()
		conn, err := testutils.GetSystemBusConnection(t)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	exportInterface := func(conn *dbus.Conn, version string) {
		t.Helper()
		require.NoError(t, conn.Export(introspect.NewIntrospectable(&introspect.Node{
			Name: string(objectPath),
			Interfaces: []introspect.Interface{
				introspect.IntrospectData,
				{Name: DbusBaseInterface + version},
			},
		}), objectPath, introspect.IntrospectData.Name))
	}
	claimName := func(conn *dbus.Conn) {
		t.Helper()
		reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
		require.NoError(t, err)
		require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
	}

	original, upgraded, client := connect(), connect(), connect()
	exportInterface(original, "2")
	claimName(original)
	b := dbusBroker{bus: client, dbusObject: client.Object(busName, objectPath)}
	first, err := b.resolveInterface(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint(2), first.version)

	exportInterface(original, "3")
	cached, err := b.resolveInterface(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, cached, "the interface should stay cached while the owner is unchanged")

	exportInterface(upgraded, "3")
	require.NoError(t, upgraded.Export(ownerChangeBrokerMock{}, objectPath, DbusBaseInterface+"3"))
	_, err = original.ReleaseName(busName)
	require.NoError(t, err)
	claimName(upgraded)

	var originalCalls, upgradedCalls atomic.Uint32
	for conn, calls := range map[*dbus.Conn]*atomic.Uint32{original: &originalCalls, upgraded: &upgradedCalls} {
		require.NoError(t, conn.ExportMethodTable(map[string]interface{}{
			"NewSession": func(username, lang, mode string) (string, string, *dbus.Error) {
				calls.Add(1)
				return "session-id", "encryption-key", nil
			},
		}, objectPath, first.name))
	}

	_, err = b.callOnInterface(context.Background(), first, "NewSession", "renamed-user", "en", "login")
	require.NoError(t, err)
	require.Equal(t, uint32(1), originalCalls.Load(), "dispatch must use the owner resolved before the name transfer")
	require.Zero(t, upgradedCalls.Load(), "the replacement owner must not receive arguments selected for the old owner")

	sessionID, _, err := b.NewSession(context.Background(), "renamed-user", "en", "login", "provider-id")
	require.NoError(t, err, "login should use the upgraded API without restarting authd")
	require.Equal(t, "provider-id", sessionID, "the upgraded broker must receive the stable provider ID")
	require.Equal(t, uint(3), b.iface.version)
	require.NotEqual(t, first.owner, b.iface.owner)
	require.NoError(t, b.DeleteUser(context.Background(), "renamed-user", "provider-id"))

	_, err = upgraded.ReleaseName(busName)
	require.NoError(t, err)
	_, err = b.resolveInterface(context.Background())
	require.Error(t, err, "an unavailable broker must not reuse a cached interface")
	require.Empty(t, b.iface)

	exportInterface(original, "2")
	claimName(original)
	downgraded, err := b.resolveInterface(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint(2), downgraded.version, "a broker rollback should also refresh the API")
}

func TestResolveInterfaceHonorsCancellation(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"GetNameOwner", "StartServiceByName", "Introspect"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var serial atomic.Uint32
			blocked := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			blockReply := sync.OnceFunc(func() {
				close(blocked)
				<-release
			})

			client, err := dbus.ConnectSystemBus(
				dbus.WithOutgoingInterceptor(func(msg *dbus.Message) {
					if msg.Type == dbus.TypeMethodCall && msg.Headers[dbus.FieldMember].Value() == method {
						serial.Store(msg.Serial())
					}
				}),
				dbus.WithIncomingInterceptor(func(msg *dbus.Message) {
					if serial.Load() != 0 && msg.Headers[dbus.FieldReplySerial].Value() == serial.Load() {
						blockReply()
					}
				}),
			)
			require.NoError(t, err)
			t.Cleanup(func() {
				unblock()
				_ = client.Close()
			})

			name := "com.ubuntu.authd.CancellationTest"
			path := dbus.ObjectPath("/com/ubuntu/authd/CancellationTest")
			if method == "Introspect" {
				name = "org.freedesktop.DBus"
				path = "/org/freedesktop/DBus"
			}
			b := dbusBroker{
				name:       "mybroker",
				bus:        client,
				dbusObject: client.Object(name, path),
				iface:      dbusInterface{name: DbusBaseInterface + "2", version: 2, owner: ":stopped"},
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := b.UserPreCheck(ctx, "user@example.com")
				done <- err
			}()

			// Real D-Bus I/O cannot use synctest; bound failures without delaying successful tests.
			watchdog, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			select {
			case <-blocked:
			case <-watchdog.Done():
				t.Fatal("D-Bus call did not reach the blocked reply")
			}
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
				require.NotContains(t, err.Error(), "Couldn't connect to broker")
			case <-watchdog.Done():
				t.Fatal("interface resolution ignored request cancellation")
			}
			require.Empty(t, b.iface, "cancellation must not retain an old interface")
			unblock()

			_, err = b.resolveInterface(watchdog)
			require.Error(t, err)
			require.NotErrorIs(t, err, context.DeadlineExceeded, "cancellation must release the interface lock")
		})
	}
}

func TestDbusBrokerInterfaceResolutionErrors(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"NewSession", "DeleteUser", "UserPreCheck"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			for _, cause := range []error{context.Canceled, context.DeadlineExceeded, fmt.Errorf("connection refused")} {
				t.Run(cause.Error(), func(t *testing.T) {
					t.Parallel()

					b := dbusBroker{
						name:       "mybroker",
						dbusObject: &mockBusObject{callErr: fmt.Errorf("introspection failed: %w", cause)},
					}
					var err error
					switch method {
					case "NewSession":
						_, _, err = b.NewSession(t.Context(), "user@example.com", "en", "login", "provider-id")
					case "DeleteUser":
						err = b.DeleteUser(t.Context(), "user@example.com", "provider-id")
					case "UserPreCheck":
						_, err = b.UserPreCheck(t.Context(), "user@example.com")
					}
					require.Error(t, err)
					if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
						require.ErrorIs(t, err, cause)
						require.NotContains(t, err.Error(), "Couldn't connect to broker")
						return
					}
					require.Contains(t, err.Error(), "Couldn't connect to broker \"mybroker\"")
					require.Contains(t, err.Error(), cause.Error())
					require.ErrorAs(t, err, &brokerUnavailableMessageError{})
				})
			}
		})
	}
}

func TestGetInterface(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		interfaces []string
		callErr    error

		wantInterface dbusInterface
		wantErr       bool
	}{
		"Single_base_interface": {
			interfaces:    []string{"com.ubuntu.authd.Broker"},
			wantInterface: dbusInterface{name: "com.ubuntu.authd.Broker", version: 1},
		},
		"Returns_highest_supported_version": {
			interfaces: []string{"com.ubuntu.authd.Broker1", "com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker3",
				"com.ubuntu.authd.Broker999"}, // This one should be ignored as it's above the latest supported API version.
			wantInterface: dbusInterface{name: "com.ubuntu.authd.Broker3", version: 3},
		},
		"Versioned_interfaces_with_unversioned": {
			interfaces:    []string{"com.ubuntu.authd.Broker2", "com.ubuntu.authd.Broker", "com.ubuntu.authd.Broker1"},
			wantInterface: dbusInterface{name: "com.ubuntu.authd.Broker2", version: 2},
		},
		"Unrelated_interfaces_are_excluded": {
			interfaces: []string{"com.ubuntu.authd.Broker2", "org.freedesktop.DBus.Introspectable",
				"com.ubuntu.authd.Broker1", "com.ubuntu.authd.BrokerUnrelated"},
			wantInterface: dbusInterface{name: "com.ubuntu.authd.Broker2", version: 2},
		},
		"Single_versioned_interface": {
			interfaces:    []string{"com.ubuntu.authd.Broker1"},
			wantInterface: dbusInterface{name: "com.ubuntu.authd.Broker1", version: 1},
		},

		"Error_when_no_supported_interfaces": {
			interfaces: []string{},
			wantErr:    true,
		},
		"Error_when_all_interfaces_above_latest_version": {
			interfaces: []string{"com.ubuntu.authd.Broker4", "com.ubuntu.authd.Broker5"},
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

			got, err := getInterface(context.Background(), mock)
			if tc.wantErr {
				require.Error(t, err, "getInterface should return an error, but did not")
				return
			}
			require.NoError(t, err, "getInterface should not return an error, but did")
			require.Equal(t, tc.wantInterface, got, "getInterface returned unexpected interface")
		})
	}
}

func TestGetInterfaceIgnoresUnrelatedInterfacesWithoutWarning(t *testing.T) {
	var warnings []string
	log.SetLevelHandler(log.WarnLevel, func(_ context.Context, _ log.Level, format string, args ...interface{}) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() { log.SetLevelHandler(log.WarnLevel, nil) })

	mock := &mockBusObject{
		introspectXML: introspectionXML(
			"com.ubuntu.authd.Broker2",
			"org.freedesktop.DBus.Introspectable",
		),
	}

	got, err := getInterface(context.Background(), mock)
	require.NoError(t, err)
	require.Equal(t, dbusInterface{name: "com.ubuntu.authd.Broker2", version: 2}, got)
	require.Empty(t, warnings, "unrelated interfaces should not produce warnings")
}

func TestDbusBrokerCallUsesInterface(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		iface            dbusInterface
		wantMethodCalled string
	}{
		"Uses_base_interface": {
			iface:            dbusInterface{name: "com.ubuntu.authd.Broker", version: 1},
			wantMethodCalled: "com.ubuntu.authd.Broker.TestMethod",
		},
		"Uses_versioned_interface": {
			iface:            dbusInterface{name: "com.ubuntu.authd.Broker2", version: 2},
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

func TestDbusBrokerCallTranslatesErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		callErr error

		wantCanceled    bool
		wantUnavailable bool
		wantLogDetail   string
		// wantPassthrough asserts the original error message is preserved
		// (i.e. not replaced by the broker unavailable message).
		wantPassthrough string
	}{
		// A broker that exits before exporting its objects (e.g. it failed to
		// start) has already claimed its bus name, so D-Bus reports the call
		// as hitting an unknown interface rather than an unknown service.
		"Unknown_interface_is_reported_as_broker_unavailable": {
			callErr:         dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface"},
			wantUnavailable: true,
		},
		"Initialization_failure_is_reported_as_broker_unavailable": {
			callErr:         dbus.Error{Name: "com.ubuntu.authd.BrokerUnavailable", Body: []interface{}{"invalid broker configuration"}},
			wantUnavailable: true,
			wantLogDetail:   "invalid broker configuration",
		},
		"Unknown_service_is_reported_as_broker_unavailable": {
			callErr:         dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"},
			wantUnavailable: true,
		},
		"Activation_timeout_is_reported_as_broker_unavailable": {
			callErr:         dbus.Error{Name: "org.freedesktop.DBus.Error.TimedOut"},
			wantUnavailable: true,
		},
		"Canceled_is_translated_to_context_canceled": {
			callErr:      dbus.Error{Name: "com.ubuntu.authd.Canceled"},
			wantCanceled: true,
		},
		"Unrelated_error_is_passed_through": {
			callErr:         dbus.Error{Name: "com.ubuntu.authd.SomeBrokerError", Body: []interface{}{"boom"}},
			wantPassthrough: "boom",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mock := &mockBusObject{callErr: tc.callErr}
			b := dbusBroker{
				name:       "mybroker",
				iface:      dbusInterface{name: "com.ubuntu.authd.Broker2", version: 2},
				dbusObject: mock,
			}

			_, err := b.call(context.Background(), "TestMethod")
			require.Error(t, err, "call should return an error")

			if tc.wantCanceled {
				require.ErrorIs(t, err, context.Canceled, "Canceled should map to context.Canceled")
				return
			}
			if tc.wantUnavailable {
				require.Contains(t, err.Error(), "mybroker", "message should name the broker")
				require.Contains(t, err.Error(), "Is it running?",
					"message should help the administrator diagnose the failure")
				require.Contains(t, err.Error(), brokerStatusCommand,
					"message should show how to check the broker status")
				require.NotContains(t, err.Error(), fmt.Sprintf("%q", brokerStatusCommand),
					"message should not quote the complete command")
				if tc.wantLogDetail != "" {
					require.Contains(t, err.Error(), tc.wantLogDetail,
						"message should include the broker's diagnostic")
				}

				_, displayErr := errmessages.RedactErrorInterceptor(
					context.Background(),
					nil,
					nil,
					func(context.Context, any) (any, error) {
						return nil, err
					},
				)
				require.Error(t, displayErr)
				require.Contains(t, displayErr.Error(), "Please contact your administrator.",
					"message should tell the user to contact their administrator")
				require.NotContains(t, displayErr.Error(), brokerStatusCommand,
					"status guidance should remain in the administrator log")
				if tc.wantLogDetail != "" {
					require.NotContains(t, displayErr.Error(), tc.wantLogDetail,
						"broker diagnostic should not be shown to the user")
				}
				return
			}
			require.Contains(t, err.Error(), tc.wantPassthrough,
				"unrelated errors should be passed through unchanged")
		})
	}
}
