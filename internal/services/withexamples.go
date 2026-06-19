// TiCS: disabled // Should only be built when building authd with the examplebroker enabled for tests.

//go:build withexamplebroker

package services

// Stop calls the brokerManager function that stops and cleans the examplebroker files.
func (m *Manager) Stop() error {
	m.brokerManager.Stop()
	return m.stop()
}
