package brokers

import (
	"context"
	"fmt"
	"sort"

	"github.com/godbus/dbus/v5"
)

// NewBroker exports the private newBroker function for testing purposes.
func NewBroker(ctx context.Context, configFile string, bus *dbus.Conn) (Broker, error) {
	return newBroker(ctx, configFile, bus)
}

// SetBrokerForSession sets the broker for a given session.
//
// This is to be used only in tests.
func (m *Manager) SetBrokerForSession(b *Broker, sessionID string) {
	m.transactionsToBrokerMu.Lock()
	m.transactionsToBroker[sessionID] = b
	m.transactionsToBrokerMu.Unlock()
}

// GenerateLayoutValidators generates the layout validators and assign them to the specified broker.
func GenerateLayoutValidators(b *Broker, sessionID string, supportedUILayouts []map[string]string) {
	b.layoutValidatorsMu.Lock()
	defer b.layoutValidatorsMu.Unlock()
	b.layoutValidators[sessionID] = generateValidators(context.Background(), sessionID, supportedUILayouts)
}

// LayoutValidatorsString returns a string representation of the layout validators.
func (b *Broker) LayoutValidatorsString(sessionID string) string {
	// Gets the map keys and sort them
	b.layoutValidatorsMu.Lock()
	defer b.layoutValidatorsMu.Unlock()

	var keys []string
	for k := range b.layoutValidators[sessionID] {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var s string
	for _, k := range keys {
		layoutStr := fmt.Sprintf("\t%s:\n", k)

		validator := b.layoutValidators[sessionID][k]

		// Same thing for sorting the keys of the validator map
		var vKeys []string
		for v := range validator {
			vKeys = append(vKeys, v)
		}
		sort.Strings(vKeys)

		for _, v := range vKeys {
			layoutStr += fmt.Sprintf("\t\t%s: { required: %v, supportedValues: %v }\n", v, validator[v].required, validator[v].supportedValues)
		}

		s += layoutStr
	}
	return s
}

// AddOngoingUserRequest adds an ongoing user request to the broker for tests.
func (b *Broker) AddOngoingUserRequest(sessionID, username string) {
	b.ongoingUserRequestsMu.Lock()
	defer b.ongoingUserRequestsMu.Unlock()
	b.ongoingUserRequests[sessionID] = username
}
