package dbusservice

import "github.com/canonical/authd/brokers/internal/broker"

// NewInterfaceForTests returns an Interface wrapping the given broker, exposing
// the v3 D-Bus methods for tests.
func NewInterfaceForTests(b *broker.Broker) *Interface {
	return &Interface{iface: "com.ubuntu.authd.Broker3", broker: b}
}

// NewInterfaceV2ForTests returns an InterfaceV2 wrapping the given broker, exposing
// the legacy D-Bus methods for tests.
func NewInterfaceV2ForTests(b *broker.Broker) *InterfaceV2 {
	return &InterfaceV2{Interface: NewInterfaceForTests(b)}
}

// Broker returns the broker backing the interface, for tests.
func (s *Interface) Broker() *broker.Broker {
	return s.broker
}

// BrokerV3ForTests returns the broker backing the v3 interface, for tests.
func (s *Service) BrokerV3ForTests() *broker.Broker {
	return s.interfaces[len(s.interfaces)-1].broker
}
