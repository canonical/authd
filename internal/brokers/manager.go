// Package brokers contains the broker object definitions, implementations and manager that will be used by the daemon
// for authentication.
package brokers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
)

// Manager is the object that manages the available brokers and the session->broker and user->broker relationships.
type Manager struct {
	brokers      map[string]*Broker
	brokersOrder []string

	usersToBroker   map[string]*Broker
	usersToBrokerMu sync.RWMutex

	transactionsToBroker   map[string]*Broker
	transactionsToBrokerMu sync.RWMutex

	cleanup func()
}

// NewManager creates a new broker manager object.
func NewManager(ctx context.Context, brokersConfPath string, configuredBrokers []string) (m *Manager, err error) {
	defer decorate.OnError(&err /*i18n.G(*/, "can't create brokers detection object") //)

	log.Debug(ctx, "Building broker detection")

	brokersConfPathWithExample, cleanup, err := useExampleBrokers()
	if err != nil {
		return nil, err
	}
	if brokersConfPathWithExample != "" {
		brokersConfPath = brokersConfPathWithExample
	}

	defer func() {
		if err != nil && cleanup != nil {
			cleanup()
		}
	}()

	// Connect to the system bus
	// Don't call dbus.SystemBus which caches globally system dbus (issues in tests)
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		return m, err
	}

	// Select all brokers in ascii order if none is configured
	if len(configuredBrokers) == 0 {
		log.Debug(ctx, "Auto-detecting brokers")

		entries, err := os.ReadDir(brokersConfPath)
		if errors.Is(err, fs.ErrNotExist) {
			log.Noticef(ctx, "Broker configuration directory %q does not exist, so using only the local broker", brokersConfPath)
		} else if err != nil {
			return m, fmt.Errorf("could not read brokers directory to detect brokers: %v", err)
		}

		for _, e := range entries {
			if !e.Type().IsRegular() {
				log.Noticef(ctx, "Skipping non-regular file %q in brokers configuration directory", e.Name())
				continue
			}
			if !strings.HasSuffix(e.Name(), ".conf") {
				log.Noticef(ctx, "Skipping file %q in brokers configuration directory, only .conf files are supported", e.Name())
				continue
			}
			configuredBrokers = append(configuredBrokers, e.Name())
		}
	}

	if len(configuredBrokers) == 0 {
		log.Notice(ctx, "No broker configuration found, using only the local broker.")
	}

	brokers := make(map[string]*Broker)
	var brokersOrder []string

	// First broker is always the local one.
	b, err := newBroker(ctx, "", nil)
	brokersOrder = append(brokersOrder, b.ID)
	brokers[b.ID] = &b

	// Load brokers configuration
	for _, cfgFileName := range configuredBrokers {
		configFile := filepath.Join(brokersConfPath, cfgFileName)
		b, err := newBroker(ctx, configFile, bus)
		if err != nil {
			log.Warningf(ctx, "Skipping broker %q is not correctly configured: %v", cfgFileName, err)
			continue
		}
		brokersOrder = append(brokersOrder, b.ID)
		brokers[b.ID] = &b
	}

	return &Manager{
		brokers:      brokers,
		brokersOrder: brokersOrder,

		usersToBroker:        make(map[string]*Broker),
		transactionsToBroker: make(map[string]*Broker),

		cleanup: cleanup,
	}, nil
}

// AvailableBrokers returns currently loaded and available brokers in preference order.
func (m *Manager) AvailableBrokers() (r []*Broker) {
	for _, id := range m.brokersOrder {
		r = append(r, m.brokers[id])
	}
	return r
}

// SetDefaultBrokerForUser memorizes which broker was used for which user.
func (m *Manager) SetDefaultBrokerForUser(brokerID, username string) error {
	broker, err := m.brokerFromID(brokerID)
	if err != nil {
		return fmt.Errorf("invalid broker: %v", err)
	}

	m.usersToBrokerMu.Lock()
	defer m.usersToBrokerMu.Unlock()
	m.usersToBroker[username] = broker
	return nil
}

// BrokerForUser returns any previously selected broker for a given user, if any.
func (m *Manager) BrokerForUser(username string) (broker *Broker) {
	m.usersToBrokerMu.RLock()
	defer m.usersToBrokerMu.RUnlock()
	return m.usersToBroker[username]
}

// BrokerFromSessionID returns broker currently in use for a given transaction sessionID.
func (m *Manager) BrokerFromSessionID(id string) (broker *Broker, err error) {
	m.transactionsToBrokerMu.RLock()
	defer m.transactionsToBrokerMu.RUnlock()

	// no session ID means local broker
	if id == "" {
		return m.brokerFromID(LocalBrokerName)
	}

	broker, exists := m.transactionsToBroker[id]
	if !exists {
		return nil, fmt.Errorf("no broker found for session %q", id)
	}

	return broker, nil
}

// NewSession create a new session for the broker and store the sessionID on the manager.
func (m *Manager) NewSession(brokerID, username, lang, mode string) (sessionID string, encryptionKey string, err error) {
	broker, err := m.brokerFromID(brokerID)
	if err != nil {
		return "", "", fmt.Errorf("invalid broker: %v", err)
	}

	sessionID, encryptionKey, err = broker.newSession(context.Background(), username, lang, mode)
	if err != nil {
		return "", "", err
	}

	m.transactionsToBrokerMu.Lock()
	defer m.transactionsToBrokerMu.Unlock()
	log.Debugf(context.Background(), "%s: New %s session for %q",
		sessionID, mode, username)
	m.transactionsToBroker[sessionID] = broker
	return sessionID, encryptionKey, nil
}

// EndSession signals the end of the session to the broker associated with the sessionID and then removes the
// session -> broker mapping.
func (m *Manager) EndSession(sessionID string) error {
	b, err := m.BrokerFromSessionID(sessionID)
	if err != nil {
		return err
	}

	if err = b.endSession(context.Background(), sessionID); err != nil {
		return err
	}

	m.transactionsToBrokerMu.Lock()
	log.Debugf(context.Background(), "%s: End session %q",
		sessionID, m.transactionsToBroker[sessionID].Name)
	delete(m.transactionsToBroker, sessionID)
	m.transactionsToBrokerMu.Unlock()
	return nil
}

// BrokerExists returns true if the brokerID is known by the manager.
func (m *Manager) BrokerExists(brokerID string) bool {
	_, exists := m.brokers[brokerID]
	return exists
}

// brokerFromID returns the broker matching this brokerID.
func (m *Manager) brokerFromID(id string) (broker *Broker, err error) {
	broker, exists := m.brokers[id]
	if !exists {
		return nil, fmt.Errorf("no broker found matching %q", id)
	}

	return broker, nil
}
