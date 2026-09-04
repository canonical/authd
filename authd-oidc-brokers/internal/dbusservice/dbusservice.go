// Package dbusservice is the dbus service implementation delegating its functional call to brokers.
package dbusservice

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	introspectableHeader           = `<node>`
	introspectableFooter           = introspect.IntrospectDataString + `</node> `
	brokerUnavailableDBusErrorName = "com.ubuntu.authd.BrokerUnavailable"
)

var (
	//go:embed interfaces/com.ubuntu.authd.BrokerV2.xml
	interfaceV2 string

	// interfaceV3 extends the base interface with a provider_id argument in NewSession and DeleteUser.
	//go:embed interfaces/com.ubuntu.authd.BrokerV3.xml
	interfaceV3 string
)

// Service is the object representing the dbus service, which contains the exported interfaces and the necessary
// information to disconnect from the bus and stop the service.
type Service struct {
	name              string
	interfaces        []*Interface
	disconnect        func()
	serve             chan struct{}
	initializing      chan struct{}
	initializationErr error
	initOnce          sync.Once
}

// Interface is the object representing a dbus interface, which contains the broker to which delegate the calls and the
// name of the interface itself.
type Interface struct {
	iface   string
	broker  *broker.Broker
	service *Service
}

var interfaceNames = []string{
	"com.ubuntu.authd.Broker",
	"com.ubuntu.authd.Broker2",
	"com.ubuntu.authd.Broker3",
}

type brokerUnavailableError struct {
	err error
}

func (e *brokerUnavailableError) Error() string {
	return e.err.Error()
}

func (e *brokerUnavailableError) Unwrap() error {
	return e.err
}

// New returns a new dbus service after exporting to the system bus our name.
func New(_ context.Context, brokerConfig broker.Config) (*Service, error) {
	name := consts.DbusName
	object := dbus.ObjectPath(consts.DbusObject)

	service := &Service{
		name:         name,
		serve:        make(chan struct{}),
		initializing: make(chan struct{}),
	}

	conn, err := service.getBus()
	if err != nil {
		return nil, err
	}

	var introspectableBody string
	for i, iface := range interfaceNames {
		version := uint(i) + 1 // There's no 0 version, so we start from 1.
		s := &Interface{
			iface:   iface,
			service: service,
		}

		var objectToExport any
		// We declare the interfaces in order, so we can use the index to determine which version we are exporting and
		// adjust the introspection XML accordingly
		// (v1 and v2 share the same method signatures, while v3 introduces provider_id arguments in some methods).
		objectToExport = s
		introspectableInterface := interfaceV3
		if version < 3 {
			introspectableInterface = interfaceV2
			objectToExport = &InterfaceV2{Interface: s}
		}

		if err := conn.Export(objectToExport, object, iface); err != nil {
			service.disconnect()
			return nil, err
		}
		introspectableBody = introspectableBody + fmt.Sprintf(introspectableInterface, iface)

		service.interfaces = append(service.interfaces, s)
	}

	// Build combined introspection XML for all versioned interfaces and export once.
	introspectable := introspect.Introspectable(introspectableHeader + introspectableBody + introspectableFooter)
	if err := conn.Export(introspectable, object, "org.freedesktop.DBus.Introspectable"); err != nil {
		service.disconnect()
		return nil, err
	}

	// Claim the bus name before doing any fallible broker initialization. The
	// activating client is then unblocked, while exported methods wait for
	// initialization in their own goroutines. This keeps the godbus read loop
	// available to process the RequestName reply. If initialization fails,
	// disconnecting removes the name and fails the activating client's pending
	// call immediately instead of waiting for D-Bus's 25s activation timeout.
	reply, err := conn.RequestName(consts.DbusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		service.initializationFailed(err)
		service.disconnect()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		err := fmt.Errorf("%q is already taken in the bus", name)
		service.initializationFailed(err)
		service.disconnect()
		return nil, err
	}

	for i, s := range service.interfaces {
		log.Debugf(context.Background(), "Initializing broker for interface %s", s.iface)
		b, err := broker.New(brokerConfig, uint(i)+1)
		if err != nil {
			service.initializationFailed(err)
			service.disconnect()
			return nil, err
		}
		s.broker = b
	}

	service.initializationDone()
	return service, nil
}

func (s *Interface) brokerForCall() (*broker.Broker, error) {
	if s.service != nil {
		<-s.service.initializing
		if s.service.initializationErr != nil {
			return nil, &brokerUnavailableError{err: s.service.initializationErr}
		}
	}
	if s.broker == nil {
		return nil, errors.New("broker is not initialized")
	}
	return s.broker, nil
}

func (s *Service) initializationDone() {
	s.finishInitialization(nil)
}

func (s *Service) initializationFailed(err error) {
	s.finishInitialization(err)
}

func (s *Service) finishInitialization(err error) {
	s.initOnce.Do(func() {
		s.initializationErr = err
		close(s.initializing)
	})
}

// Addr returns the address of the service.
func (s *Service) Addr() string {
	return s.name
}

// Serve wait for the service.
func (s *Service) Serve() error {
	<-s.serve
	return nil
}

// Stop stop the service and do all the necessary cleanup operation.
func (s *Service) Stop() error {
	// Check if already stopped.
	select {
	case <-s.serve:
	default:
		close(s.serve)
		s.disconnect()
	}

	return nil
}
