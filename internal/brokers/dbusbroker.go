package brokers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"golang.org/x/exp/slices"
	"gopkg.in/ini.v1"
)

const (
	// DbusBaseInterface is the expected interface that should be implemented by the brokers.
	DbusBaseInterface string = "com.ubuntu.authd.Broker"

	// LatestAPIVersion is the latest API version supported by authd.
	LatestAPIVersion = 3
)

type dbusInterface struct {
	name    string
	version uint
}

type dbusBroker struct {
	name  string
	iface dbusInterface

	dbusObject dbus.BusObject
}

// newDbusBroker returns a dbus broker and broker attributes from its configuration file.
func newDbusBroker(ctx context.Context, bus *dbus.Conn, configFile string) (b dbusBroker, name, brandIcon string, err error) {
	defer decorate.OnError(&err, "D-Bus broker from configuration file: %q", configFile)

	log.Debugf(ctx, "D-Bus broker configuration at %q", configFile)

	cfg, err := ini.Load(configFile)
	if err != nil {
		return b, "", "", fmt.Errorf("could not read ini configuration for broker %v", err)
	}

	nameVal, err := cfg.Section("authd").GetKey("name")
	if err != nil {
		return b, "", "", fmt.Errorf("missing field for broker: %v", err)
	}

	brandIconVal, err := cfg.Section("authd").GetKey("brand_icon")
	if err != nil {
		return b, "", "", fmt.Errorf("missing field for broker: %v", err)
	}

	dbusName, err := cfg.Section("authd").GetKey("dbus_name")
	if err != nil {
		return b, "", "", fmt.Errorf("missing field for broker: %v", err)
	}

	objectName, err := cfg.Section("authd").GetKey("dbus_object")
	if err != nil {
		return b, "", "", fmt.Errorf("missing field for broker: %v", err)
	}

	dBroker := dbusBroker{
		name:       nameVal.String(),
		dbusObject: bus.Object(dbusName.String(), dbus.ObjectPath(objectName.String())),
	}

	dBroker.iface, err = getInterface(dBroker.dbusObject)
	if err != nil {
		return b, "", "", fmt.Errorf("could not detect broker interfaces: %v", err)
	}

	return dBroker, nameVal.String(), brandIconVal.String(), nil
}

// getInterface introspects the broker's D-Bus object and returns the interface with the highest version supported both
// by the broker and authd.
func getInterface(obj dbus.BusObject) (dbusInterface, error) {
	node, err := introspect.Call(obj)
	if err != nil {
		return dbusInterface{}, fmt.Errorf("could not introspect broker: %v", err)
	}

	var supportedInterfaces []dbusInterface
	for _, iface := range node.Interfaces {
		// Ignore interfaces that do not satisfy the expected format, as they are not relevant for selecting the broker
		// interface version.
		// The expected format is com.ubuntu.authd.BrokerX, where X is the version number (or empty for the first
		// version).
		version, err := interfaceVersion(iface.Name)
		if err != nil {
			log.Warningf(context.Background(), "Could not parse interface version from %q", iface.Name)
			continue
		}

		if version > LatestAPIVersion {
			log.Warningf(context.Background(), "Ignoring interface %q with version %d higher than latest supported version %d", iface.Name, version, LatestAPIVersion)
			continue
		}

		supportedInterfaces = append(supportedInterfaces, dbusInterface{name: iface.Name, version: uint(version)})
	}

	slices.SortFunc(supportedInterfaces, func(a, b dbusInterface) int {
		if a.version < b.version {
			return -1
		} else if a.version > b.version {
			return 1
		}
		return 0
	})

	if len(supportedInterfaces) == 0 {
		return dbusInterface{}, errors.New("no supported interfaces found")
	}

	return supportedInterfaces[len(supportedInterfaces)-1], nil
}

// interfaceVersion extracts the version number from the broker interface name.
//
// If it is the base interface without a version suffix, it returns 1.
func interfaceVersion(iface string) (int, error) {
	suffix := strings.TrimPrefix(iface, DbusBaseInterface)
	if suffix == iface {
		return 0, fmt.Errorf("interface name %q does not start with expected prefix %q", iface, DbusBaseInterface)
	}

	if suffix == "" {
		return 1, nil
	}

	version, err := strconv.Atoi(suffix)
	if err != nil {
		return 0, fmt.Errorf("invalid interface version suffix %q: %v", suffix, err)
	}

	return version, nil
}

// NewSession calls the corresponding method on the broker bus and returns the session ID and encryption key.
// On API v3, the providerID (stable provider identifier) is passed so the broker can locate the
// provider ID-keyed cache directory directly. v2 brokers receive only username, lang, and mode.
func (b dbusBroker) NewSession(ctx context.Context, username, lang, mode, providerID string) (sessionID, encryptionKey string, err error) {
	var call *dbus.Call
	if b.iface.version < 3 {
		call, err = b.call(ctx, "NewSession", username, lang, mode)
	} else {
		call, err = b.call(ctx, "NewSession", username, lang, mode, providerID)
	}
	if err != nil {
		return "", "", err
	}

	if err = call.Store(&sessionID, &encryptionKey); err != nil {
		return "", "", err
	}

	return sessionID, encryptionKey, nil
}

// GetAuthenticationModes calls the corresponding method on the broker bus and returns the authentication modes supported by it.
func (b dbusBroker) GetAuthenticationModes(ctx context.Context, sessionID string, supportedUILayouts []map[string]string) (authenticationModes []map[string]string, err error) {
	call, err := b.call(ctx, "GetAuthenticationModes", sessionID, supportedUILayouts)
	if err != nil {
		return nil, err
	}
	if err = call.Store(&authenticationModes); err != nil {
		return nil, err
	}

	return authenticationModes, nil
}

// SelectAuthenticationMode calls the corresponding method on the broker bus and returns the UI layout for the selected mode.
func (b dbusBroker) SelectAuthenticationMode(ctx context.Context, sessionID, authenticationModeName string) (uiLayoutInfo map[string]string, err error) {
	call, err := b.call(ctx, "SelectAuthenticationMode", sessionID, authenticationModeName)
	if err != nil {
		return nil, err
	}
	if err = call.Store(&uiLayoutInfo); err != nil {
		return nil, err
	}

	return uiLayoutInfo, nil
}

// IsAuthenticated calls the corresponding method on the broker bus and returns the user information and access.
func (b dbusBroker) IsAuthenticated(_ context.Context, sessionID, authenticationData string) (access, data string, err error) {
	// We don’t want to cancel the context when the parent call is cancelled.
	call, err := b.call(context.Background(), "IsAuthenticated", sessionID, authenticationData)
	if err != nil {
		return "", "", err
	}
	if err = call.Store(&access, &data); err != nil {
		return "", "", err
	}

	return access, data, nil
}

// EndSession calls the corresponding method on the broker bus.
func (b dbusBroker) EndSession(ctx context.Context, sessionID string) (err error) {
	if _, err := b.call(ctx, "EndSession", sessionID); err != nil {
		return err
	}
	return nil
}

// CancelIsAuthenticated calls the corresponding method on the broker bus.
func (b dbusBroker) CancelIsAuthenticated(ctx context.Context, sessionID string) {
	// We don’t want to cancel the context when the parent call is cancelled.
	if _, err := b.call(context.Background(), "CancelIsAuthenticated", sessionID); err != nil {
		log.Errorf(ctx, "could not cancel IsAuthenticated call for session %q: %v", sessionID, err)
	}
}

// UserPreCheck calls the corresponding method on the broker bus.
func (b dbusBroker) UserPreCheck(ctx context.Context, username string) (userinfo string, err error) {
	call, err := b.call(ctx, "UserPreCheck", username)
	if err != nil {
		return "", err
	}
	if err = call.Store(&userinfo); err != nil {
		return "", err
	}

	return userinfo, nil
}

// DeleteUser calls the corresponding method on the broker bus to delete broker side user data.
// On API v3, the providerID (stable provider identifier) is passed so the broker can locate the
// provider ID-keyed cache directory even after an email change. v2 brokers receive only the username.
func (b dbusBroker) DeleteUser(ctx context.Context, username, providerID string) error {
	var err error
	if b.iface.version < 3 {
		_, err = b.call(ctx, "DeleteUser", username)
	} else {
		_, err = b.call(ctx, "DeleteUser", username, providerID)
	}
	if err != nil {
		return err
	}

	return nil
}

// call is an abstraction over dbus calls to ensure we wrap the returned error to an ErrorToDisplay.
// All wrapped errors will be logged, but not returned to the UI.
func (b dbusBroker) call(ctx context.Context, method string, args ...interface{}) (*dbus.Call, error) {
	dbusMethod := b.iface.name + "." + method
	call := b.dbusObject.CallWithContext(ctx, dbusMethod, 0, args...)
	if err := call.Err; err != nil {
		var dbusError dbus.Error
		// If the broker is not available ib dbus, the original "method was not provided by any .service files" isn't
		// user-friendly, so we replace it with a better message.
		if errors.As(err, &dbusError) && dbusError.Name == "org.freedesktop.DBus.Error.ServiceUnknown" {
			err = fmt.Errorf("couldn't connect to broker %q. Is it running?", b.name)
		}
		if errors.As(err, &dbusError) && dbusError.Name == "com.ubuntu.authd.Canceled" {
			return nil, context.Canceled
		}
		return nil, errmessages.NewToDisplayError(err)
	}

	return call, nil
}
