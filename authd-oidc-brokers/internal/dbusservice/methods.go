package dbusservice

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd/log"
)

// NewSession is the method through which the broker and the daemon will communicate once dbusInterface.NewSession is called.
func (s *Service) NewSession(username, lang, mode string) (sessionID, encryptionKey string, dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "Creating new session (username=%s, lang=%s, mode=%s)", username, lang, mode)
	sessionID, encryptionKey, err := s.broker.NewSession(username, lang, mode)
	if err != nil {
		return "", "", dbus.MakeFailedError(err)
	}
	log.Debugf(context.Background(), "Created new session %s", sessionID)
	return sessionID, encryptionKey, nil
}

// GetAuthenticationModes is the method through which the broker and the daemon will communicate once dbusInterface.GetAuthenticationModes is called.
func (s *Service) GetAuthenticationModes(sessionID string, supportedUILayouts []map[string]string) (authenticationModes []map[string]string, dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "Getting authentication modes for session %s", sessionID)
	authenticationModes, err := s.broker.GetAuthenticationModes(sessionID, supportedUILayouts)
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	log.Debugf(context.Background(), "Got authentication modes for session %s: %v", sessionID, authenticationModes)
	return authenticationModes, nil
}

// SelectAuthenticationMode is the method through which the broker and the daemon will communicate once dbusInterface.SelectAuthenticationMode is called.
func (s *Service) SelectAuthenticationMode(sessionID, authenticationModeName string) (uiLayoutInfo map[string]string, dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "Selecting authentication mode %s for session %s", authenticationModeName, sessionID)
	uiLayoutInfo, err := s.broker.SelectAuthenticationMode(sessionID, authenticationModeName)
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	log.Debugf(context.Background(), "Selected authentication mode %s for session %s: %v", authenticationModeName, sessionID, uiLayoutInfo)
	return uiLayoutInfo, nil
}

// IsAuthenticated is the method through which the broker and the daemon will communicate once dbusInterface.IsAuthenticated is called.
func (s *Service) IsAuthenticated(sessionID, authenticationData string) (access, data string, dbusErr *dbus.Error) {
	// Do *not* log authenticationData here, because it may contain the user's password in cleartext.
	log.Debugf(context.Background(), "Handling IsAuthenticated call for session %s", sessionID)
	access, data, err := s.broker.IsAuthenticated(sessionID, authenticationData)
	if errors.Is(err, context.Canceled) {
		return access, data, makeCanceledError()
	}
	if err != nil {
		log.Warningf(context.Background(), "IsAuthenticated error: %v", err)
		return broker.AuthDenied, "", dbus.MakeFailedError(err)
	}
	log.Debugf(context.Background(), "IsAuthenticated result (session %s): %s, %s", sessionID, access, data)
	return access, data, nil
}

// EndSession is the method through which the broker and the daemon will communicate once dbusInterface.EndSession is called.
func (s *Service) EndSession(sessionID string) (dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "Ending session %s", sessionID)
	err := s.broker.EndSession(sessionID)
	if err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// CancelIsAuthenticated is the method through which the broker and the daemon will communicate once dbusInterface.CancelIsAuthenticated is called.
func (s *Service) CancelIsAuthenticated(sessionID string) (dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "Cancelling IsAuthenticated call for session %s", sessionID)
	s.broker.CancelIsAuthenticated(sessionID)
	return nil
}

// UserPreCheck is the method through which the broker and the daemon will communicate once dbusInterface.UserPreCheck is called.
func (s *Service) UserPreCheck(username string) (userinfo string, dbusErr *dbus.Error) {
	log.Debugf(context.Background(), "UserPreCheck: %s", username)
	userinfo, err := s.broker.UserPreCheck(username)
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	log.Debugf(context.Background(), "UserPreCheck result: %s", userinfo)
	return userinfo, nil
}

// makeCanceledError creates a dbus.Error for a canceled operation.
func makeCanceledError() *dbus.Error {
	return &dbus.Error{Name: "com.ubuntu.authd.Canceled"}
}
