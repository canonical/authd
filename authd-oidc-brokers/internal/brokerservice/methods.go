package brokerservice

import (
	"github.com/godbus/dbus/v5"
)

// IsAuthenticated is the method through which the broker and the daemon will communicate once dbusInterface.IsAuthenticated is called.
func (s *Service) IsAuthenticated(sessionID, authenticationData string) (access, data string, dbusErr *dbus.Error) {
	access, data, err := s.broker.IsAuthenticated(sessionID, authenticationData)
	if err != nil {
		return "", "", dbus.MakeFailedError(err)
	}
	return access, data, nil
}
