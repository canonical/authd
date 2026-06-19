//go:build !withexamplebroker

package brokers

// useExampleBrokers is a no-op in production code.
func useExampleBrokers() (string, func(), error) {
	return "", nil, nil
}

// Stop is a no-op in production code.
func (m *Manager) Stop() {}
