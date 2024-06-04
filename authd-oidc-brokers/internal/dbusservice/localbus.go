//go:build withlocalbus

package dbusservice

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/ubuntu/oidc-broker/internal/testutils"
)

// getBus creates the local bus and returns a connection to the bus.
// It attaches a disconnect handler to stop the local bus subprocess.
func (s *Service) getBus() (*dbus.Conn, error) {
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		return nil, err
	}
	slog.Info(fmt.Sprintf("Using local bus address: %s", os.Getenv("DBUS_SYSTEM_BUS_ADDRESS")))
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}

	s.disconnect = func() {
		conn.Close()
		cleanup()
	}
	return conn, err
}
