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

// DbusBaseInterface is the expected interface that should be implemented by the brokers.
const DbusBaseInterface string = "com.ubuntu.authd.Broker"

type dbusBroker struct {
	name       string
	interfaces []string

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

	dBroker.interfaces, err = detectAvailableInterfaces(dBroker.dbusObject)
	if err != nil {
		return b, "", "", fmt.Errorf("could not detect broker interfaces: %v", err)
	}
	log.Debugf(ctx, "Broker %q supports interfaces: %v", nameVal.String(), dBroker.interfaces)

	return dBroker, nameVal.String(), brandIconVal.String(), nil
}

// detectAvailableInterfaces introspects the broker's D-Bus object and returns the supported
// interface versions.
func detectAvailableInterfaces(obj dbus.BusObject) ([]string, error) {
	node, err := introspect.Call(obj)
	if err != nil {
		return nil, fmt.Errorf("could not introspect broker: %v", err)
	}

	var supportedInterfaces []string
	for _, iface := range node.Interfaces {
		// Ignore interfaces that do not satisfy the expected format, as they are not relevant for selecting the broker
		// interface version.
		// The expected format is com.ubuntu.authd.BrokerX, where X is the version number (or empty for the first
		// version).
		suffix := strings.TrimPrefix(iface.Name, DbusBaseInterface)
		if suffix == iface.Name {
			// The interface name doesn't start with com.ubuntu.authd.Broker
			continue
		}

		// The suffix should be either empty (for the first version) or a number (for later versions).
		if suffix != "" {
			if _, err := strconv.Atoi(suffix); err != nil {
				continue
			}
		}

		supportedInterfaces = append(supportedInterfaces, iface.Name)
	}

	slices.SortFunc(supportedInterfaces, func(a, b string) int {
		suffixA := strings.TrimPrefix(a, DbusBaseInterface)
		suffixB := strings.TrimPrefix(b, DbusBaseInterface)

		// We know from the filtering above that the suffix is either empty (for the base
		// interface, which is version 1 per D-Bus API versioning) or a valid number.
		versionA := 1
		if suffixA != "" {
			versionA, _ = strconv.Atoi(suffixA)
		}
		versionB := 1
		if suffixB != "" {
			versionB, _ = strconv.Atoi(suffixB)
		}

		if versionA < versionB {
			return -1
		} else if versionA > versionB {
			return 1
		}
		return 0
	})

	return supportedInterfaces, nil
}

// NewSession calls the corresponding method on the broker bus and returns the session ID and encryption key.
func (b dbusBroker) NewSession(ctx context.Context, username, lang, mode string) (sessionID, encryptionKey string, err error) {
	call, err := b.call(ctx, "NewSession", username, lang, mode)
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

// call is an abstraction over dbus calls to ensure we wrap the returned error to an ErrorToDisplay.
// All wrapped errors will be logged, but not returned to the UI.
func (b dbusBroker) call(ctx context.Context, method string, args ...interface{}) (*dbus.Call, error) {
	// For now, we can safely use the latest interface available, as the methods we call are the same in all versions.
	// If in the future we need to call methods that are only available in specific versions,
	// we can add logic here to select the appropriate interface based on the method being called.
	if len(b.interfaces) == 0 {
		return nil, fmt.Errorf("no supported interfaces found for broker %q", b.name)
	}
	dbusInterface := b.interfaces[len(b.interfaces)-1]

	dbusMethod := dbusInterface + "." + method
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
