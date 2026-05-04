// Package dbusservice is the dbus service implementation delegating its functional call to brokers.
package dbusservice

import (
	"context"
	"fmt"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	introspectableHeader    = `<node>`
	introspectableInterface = `
	<interface name="%s">
		<method name="NewSession">
			<arg type="s" direction="in" name="username"/>
			<arg type="s" direction="in" name="lang"/>
			<arg type="s" direction="in" name="mode"/>
			<arg type="s" direction="out" name="sessionID"/>
			<arg type="s" direction="out" name="encryptionKey"/>
		</method>
		<method name="GetAuthenticationModes">
			<arg type="s" direction="in" name="sessionID"/>
			<arg type="aa{ss}" direction="in" name="supportedUILayouts"/>
			<arg type="aa{ss}" direction="out" name="authenticationModes"/>
		</method>
		<method name="SelectAuthenticationMode">
			<arg type="s" direction="in" name="sessionID"/>
			<arg type="s" direction="in" name="authenticationModeName"/>
			<arg type="a{ss}" direction="out"  name="uiLayoutInfo"/>
		</method>
		<method name="IsAuthenticated">
			<arg type="s" direction="in" name="sessionID"/>
			<arg type="s" direction="in" name="authenticationData"/>
			<arg type="s" direction="out" name="access"/>
			<arg type="s" direction="out" name="data"/>
		</method>
		<method name="EndSession">
			<arg type="s" direction="in" name="sessionID"/>
		</method>
		<method name="CancelIsAuthenticated">
			<arg type="s" direction="in" name="sessionID"/>
		</method>
		<method name="UserPreCheck">
			<arg type="s" direction="in" name="username"/>
			<arg type="s" direction="out" name="userInfo"/>
		</method>
		<method name="DeleteUser">
			<arg type="s" direction="in" name="username"/>
		</method>
	</interface>`
	introspectableFooter = introspect.IntrospectDataString + `</node> `
)

// Service is the object representing the dbus service, which contains the exported interfaces and the necessary
// information to disconnect from the bus and stop the service.
type Service struct {
	name       string
	interfaces []*Interface
	disconnect func()
	serve      chan struct{}
}

// Interface is the object representing a dbus interface, which contains the broker to which delegate the calls and the
// name of the interface itself.
type Interface struct {
	iface  string
	broker *broker.Broker
}

var interfaceNames = []string{
	"com.ubuntu.authd.Broker",
	"com.ubuntu.authd.Broker2",
}

// New returns a new dbus service after exporting to the system bus our name.
func New(_ context.Context, brokerConfig broker.Config) (*Service, error) {
	name := consts.DbusName
	object := dbus.ObjectPath(consts.DbusObject)

	service := &Service{
		name:  name,
		serve: make(chan struct{}),
	}

	conn, err := service.getBus()
	if err != nil {
		return nil, err
	}

	var introspectableBody string
	for i, iface := range interfaceNames {
		b, err := broker.New(brokerConfig, uint(i)+1) // There's no 0 version, so we start from 1.
		if err != nil {
			service.disconnect()
			return nil, fmt.Errorf("error initializing broker for %q: %v", iface, err)
		}

		s := &Interface{
			iface:  iface,
			broker: b,
		}

		if err := conn.Export(s, object, iface); err != nil {
			service.disconnect()
			return nil, err
		}

		service.interfaces = append(service.interfaces, s)
		introspectableBody = introspectableBody + fmt.Sprintf(introspectableInterface, iface)
	}

	// Build combined introspection XML for all versioned interfaces and export once.
	introspectable := introspect.Introspectable(introspectableHeader + introspectableBody + introspectableFooter)
	if err := conn.Export(introspectable, object, "org.freedesktop.DBus.Introspectable"); err != nil {
		service.disconnect()
		return nil, err
	}

	reply, err := conn.RequestName(consts.DbusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		service.disconnect()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		service.disconnect()
		return nil, fmt.Errorf("%q is already taken in the bus", name)
	}

	return service, nil
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
