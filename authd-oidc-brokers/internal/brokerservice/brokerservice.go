// Package brokerservice is the dbus service implementation delegating its functional call to brokers.
package brokerservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/ubuntu/decorate"
	"github.com/ubuntu/oidc-broker/internal/broker"
)

const intro = `
<node>
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
		<arg type="a{s}s" direction="in" name="supportedUILayouts"/>
		<arg type="a{s}s" direction="out" name="authenticationModes"/>
		</method>
		<method name="SelectAuthenticationMode">
			<arg type="s" direction="in" name="sessionID"/>
			<arg type="s" direction="in" name="authenticationModeName"/>
			<arg type="a{s}s" direction="out"  name="uiLayoutInfo"/>
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
	</interface>` + introspect.IntrospectDataString + `</node> `

// Service is the handler exposing our broker methods on the system bus.
type Service struct {
	name   string
	broker broker.Broker

	serve      chan struct{}
	disconnect func()
}

// New returns a new dbus service after exporting to the system bus our name.
func New(_ context.Context, serviceName string) (s *Service, err error) {
	serviceName = strings.ReplaceAll(serviceName, "-", "_")
	defer decorate.OnError(&err, "cannot create dbus service %q", serviceName)

	name := fmt.Sprintf("com.ubuntu.oidc.%s", serviceName)
	iface := "com.ubuntu.authd.Broker"
	object := dbus.ObjectPath(fmt.Sprintf("/com/ubuntu/oidc/%s", serviceName))

	s = &Service{
		name:   name,
		broker: broker.Broker{},
		serve:  make(chan struct{}),
	}

	conn, err := s.getBus()
	if err != nil {
		return nil, err
	}

	if err := conn.Export(s, object, iface); err != nil {
		return nil, err
	}
	if err := conn.Export(introspect.Introspectable(fmt.Sprintf(intro, iface)), object, "org.freedesktop.DBus.Introspectable"); err != nil {
		return nil, err
	}

	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		s.disconnect()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		s.disconnect()
		return nil, fmt.Errorf("%q is already taken in the bus", name)
	}

	return s, nil
}

// Addr returns the address of the service.
func (s Service) Addr() string {
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
