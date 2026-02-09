// Package testutils provides utility functions and behaviors for testing.
package testutils

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// StartBusMock starts a mock dbus daemon and returns its address and a cancel function to stop it.
func StartBusMock() (string, func(), error) {
	if isRunning() {
		return "", nil, errors.New("system bus mock is already running")
	}

	tmp, err := os.MkdirTemp(os.TempDir(), "authd-system-bus-mock")
	if err != nil {
		return "", nil, err
	}

	cfgPath := filepath.Join(tmp, "bus.conf")
	listenPath := filepath.Join(tmp, "bus.sock")

	err = os.WriteFile(cfgPath, []byte(fmt.Sprintf(systemBusMockCfg, listenPath)), 0600)
	if err != nil {
		err = errors.Join(err, os.RemoveAll(tmp))
		return "", nil, err
	}

	busCtx, busCancel := context.WithCancel(context.Background())
	//#nosec:G204 // This is a test helper and we are in control of the arguments.
	cmd := exec.CommandContext(busCtx, "dbus-daemon", "--config-file="+cfgPath, "--print-address=1")

	// dbus-daemon can perform some NSS lookups when a client establishes a new connection, usually to determine the
	// group membership of the caller -- if examplebroker is running via systemd activation and our NSS module is
	// enabled the operation would time out, as the NSS module will send a request to authd that will never be answered,
	// due to the fact that the socket is present but our gRPC server is not yet listening on it.
	//
	// Setting this environment variable causes the authd NSS module to return early and skip the lookup.
	// For brevity, we reuse the same variable systemd uses when running the dbus service, which is already configured
	// in our module.
	cmd.Env = []string{"SYSTEMD_NSS_DYNAMIC_BYPASS=1"}

	dbusStdout, err := cmd.StdoutPipe()
	if err != nil {
		busCancel()
		return "", nil, errors.Join(err, os.RemoveAll(tmp))
	}
	if err := cmd.Start(); err != nil {
		busCancel()
		err = errors.Join(err, os.RemoveAll(tmp))
		return "", nil, err
	}

	waitDone := make(chan struct{})
	var busAddress string

	go func() {
		scanner := bufio.NewScanner(dbusStdout)
		for scanner.Scan() {
			busAddress = scanner.Text()
			close(waitDone)
			break
		}
	}()

	select {
	case <-time.After(10 * time.Second):
		busCancel()
		err = errors.New("dbus-daemon failed to start in 10 seconds")
		return "", nil, errors.Join(err, os.RemoveAll(tmp))
	case <-waitDone:
	}

	if !strings.HasPrefix(busAddress, "unix:path=") {
		busCancel()
		err = fmt.Errorf("invalid bus path: %s", busAddress)
		return "", nil, errors.Join(err, os.RemoveAll(tmp))
	}

	busAddress, _, _ = strings.Cut(busAddress, ",")
	return busAddress, func() {
		busCancel()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmp)
	}, nil
}

// StartSystemBusMock starts a mock dbus daemon and returns a cancel function to stop it.
//
// This function uses t.Setenv to set the DBUS_SYSTEM_BUS_ADDRESS environment, so it shouldn't be used in parallel tests
// that rely on the mentioned variable.
func StartSystemBusMock() (func(), error) {
	busAddress, busCancel, err := StartBusMock()
	if err != nil {
		return nil, err
	}
	prev, set := os.LookupEnv("DBUS_SYSTEM_BUS_ADDRESS")
	err = os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", busAddress)
	if err != nil {
		busCancel()
		return nil, err
	}

	return func() {
		busCancel()
		if !set {
			err = os.Unsetenv("DBUS_SYSTEM_BUS_ADDRESS")
		} else {
			err = os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", prev)
		}
		if err != nil {
			panic(err)
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
