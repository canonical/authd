//go:build !withlocalbus

package dbusservice

import (
	"github.com/godbus/dbus/v5"
)

// getBus returns the system bus and attach a disconnect handler.
func (s *Service) getBus() (*dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	s.disconnect = func() { _ = conn.Close() }

	return conn, nil
}
