package brokers

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
	"gopkg.in/ini.v1"
)

// DbusInterface is the expected interface that should be implemented by the brokers.
const DbusInterface string = "com.ubuntu.authd.Broker"

type dbusBroker struct {
	name string

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

	return dbusBroker{
		name:       nameVal.String(),
		dbusObject: bus.Object(dbusName.String(), dbus.ObjectPath(objectName.String())),
	}, nameVal.String(), brandIconVal.String(), nil
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
	dbusMethod := DbusInterface + "." + method
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
