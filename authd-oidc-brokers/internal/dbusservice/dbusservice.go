// Package dbusservice is the dbus service implementation delegating its functional call to brokers.
package dbusservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"gopkg.in/ini.v1"
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
		<arg type="a{ss}" direction="in" name="supportedUILayouts"/>
		<arg type="a{ss}" direction="out" name="authenticationModes"/>
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
		</method>
	</interface>` + introspect.IntrospectDataString + `</node> `

// Service is the handler exposing our broker methods on the system bus.
type Service struct {
	name   string
	broker *broker.Broker

	serve      chan struct{}
	disconnect func()
}

// New returns a new dbus service after exporting to the system bus our name.
func New(_ context.Context, cfgPath, cachePath string) (s *Service, err error) {
	cfg, err := parseConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	var allowedSSHSuffixes []string
	if cfg[usersSection][sshSuffixesKey] != "" {
		allowedSSHSuffixes = strings.Split(cfg[usersSection][sshSuffixesKey], ",")
	}

	bCfg := broker.Config{
		IssuerURL:          cfg[oidcSection][issuerKey],
		ClientID:           cfg[oidcSection][clientIDKey],
		HomeBaseDir:        cfg[usersSection][homeDirKey],
		AllowedSSHSuffixes: allowedSSHSuffixes,
		CachePath:          cachePath,
	}
	b, err := broker.New(bCfg)
	if err != nil {
		return nil, err
	}

	name := consts.DbusName
	object := dbus.ObjectPath(consts.DbusObject)
	iface := "com.ubuntu.authd.Broker"
	s = &Service{
		name:   name,
		broker: b,
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

	reply, err := conn.RequestName(consts.DbusName, dbus.NameFlagDoNotQueue)
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

// parseConfig parses the config file and returns a map with the configuration keys and values.
func parseConfig(cfgPath string) (map[string]map[string]string, error) {
	iniCfg, err := ini.Load(cfgPath)
	if err != nil {
		return nil, err
	}

	cfg := make(map[string]map[string]string)
	for _, section := range iniCfg.Sections() {
		cfg[section.Name()] = make(map[string]string)
		for _, key := range section.Keys() {
			if strings.Contains(key.String(), "<") && strings.Contains(key.String(), ">") {
				err = errors.Join(err, fmt.Errorf("found invalid character in section %q, key %q", section.Name(), key.Name()))
				continue
			}
			cfg[section.Name()][key.Name()] = key.String()
		}
	}

	// This means we found at least one section that was potentially not edited.
	if err != nil {
		return nil, fmt.Errorf("config file has invalid values, did you edit the file %q?\n%w", cfgPath, err)
	}
	return cfg, nil
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
