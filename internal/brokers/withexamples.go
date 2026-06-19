//go:build withexamplebroker

package brokers

import (
	"context"
	"os"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/log"
)

// useExampleBrokers starts a mock system bus and exports the examplebroker in it.
func useExampleBrokers() (string, func(), error) {
	busCleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		return "", nil, err
	}
	log.Debugf(context.Background(), "Mock system bus started on %s\n", os.Getenv("DBUS_SYSTEM_BUS_ADDRESS"))

	// Create the directory for the broker configuration files.
	cfgPath, err := os.MkdirTemp(os.TempDir(), "examplebroker.d")
	if err != nil {
		busCleanup()
		return "", nil, err
	}

	conn, err := examplebroker.StartBus(cfgPath)
	if err != nil {
		if err := os.RemoveAll(cfgPath); err != nil {
			log.Warningf(context.Background(), "Failed to remove the broker configuration directory: %v", err)
		}
		busCleanup()
		return "", nil, err
	}

	return cfgPath, func() {
		conn.Close()
		if err := os.RemoveAll(cfgPath); err != nil {
			log.Warningf(context.Background(), "Failed to remove the broker configuration directory: %v", err)
		}
		busCleanup()
	}, nil
}

// Stop calls the function responsible for cleaning up the examplebrokers.
func (m *Manager) Stop() {
	m.cleanup()
}
