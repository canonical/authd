// Package testutils provides utility functions and behaviors for testing.
package testutils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const defaultSystemBusAddress = "unix:path=/var/run/dbus/system_bus_socket"

var systemBusMockCfg = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>system</type>
  <keep_umask/>
  <listen>unix:path=%s</listen>
  <policy context="default">
    <allow user="*"/>
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`

// StartSystemBusMock starts a mock dbus daemon and returns a cancel function to stop it.
//
// This function uses os.Setenv to set the DBUS_SYSTEM_BUS_ADDRESS environment, so it shouldn't be used in parallel tests
// that rely on the mentioned variable.
func StartSystemBusMock() (func(), error) {
	if isRunning() {
		return nil, errors.New("system bus mock is already running")
	}

	tmp, err := os.MkdirTemp(os.TempDir(), "authd-system-bus-mock")
	if err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(tmp, "bus.conf")
	listenPath := filepath.Join(tmp, "bus.sock")

	err = os.WriteFile(cfgPath, []byte(fmt.Sprintf(systemBusMockCfg, listenPath)), 0600)
	if err != nil {
		err = errors.Join(err, os.RemoveAll(tmp))
		return nil, err
	}

	busCtx, busCancel := context.WithCancel(context.Background())
	//#nosec:G204 // This is a test helper and we are in control of the arguments.
	cmd := exec.CommandContext(busCtx, "dbus-daemon", "--config-file="+cfgPath)
	if err := cmd.Start(); err != nil {
		busCancel()
		err = errors.Join(err, os.RemoveAll(tmp))
		return nil, err
	}
	// Give some time for the daemon to start.
	time.Sleep(500 * time.Millisecond)

	prev, set := os.LookupEnv("DBUS_SYSTEM_BUS_ADDRESS")
	os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+listenPath)

	return func() {
		busCancel()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmp)

		if !set {
			_ = os.Unsetenv("DBUS_SYSTEM_BUS_ADDRESS")
		} else {
			_ = os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", prev)
		}
	}, nil
}

// GetSystemBusConnection returns a connection to the system bus with a safety check to avoid mistakenly connecting to the
// actual system bus.
func GetSystemBusConnection(t *testing.T) (*dbus.Conn, error) {
	t.Helper()
	if !isRunning() {
		return nil, errors.New("system bus mock is not running. If that's intended, manually connect to the system bus instead of using this function")
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// isRunning checks if the system bus mock is running.
func isRunning() bool {
	busAddr := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS")
	return busAddr != "" && busAddr != defaultSystemBusAddress
}
