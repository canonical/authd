package dbusservice

import "github.com/canonical/authd/authd-oidc-brokers/internal/broker"

// NewInterfaceForTests returns an Interface wrapping the given broker, exposing
// the v4 D-Bus methods for tests.
func NewInterfaceForTests(b *broker.Broker) *Interface {
	return &Interface{iface: "com.ubuntu.authd.Broker4", broker: b}
}

// NewInterfaceV2ForTests returns an InterfaceV2 wrapping the given broker, exposing
// the legacy D-Bus methods for tests.
func NewInterfaceV2ForTests(b *broker.Broker) *InterfaceV2 {
	return &InterfaceV2{Interface: NewInterfaceForTests(b)}
}

// NewInterfaceV3ForTests returns an InterfaceV3 wrapping the given broker,
// exposing the v3 D-Bus methods for tests.
func NewInterfaceV3ForTests(b *broker.Broker) *InterfaceV3 {
	return &InterfaceV3{Interface: NewInterfaceForTests(b)}
}

// Broker returns the broker backing the interface, for tests.
func (s *Interface) Broker() *broker.Broker {
	return s.broker
}
