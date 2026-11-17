package dbusservice

import (
	"context"
	"errors"

	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
)

const (
	dbusService          = "org.freedesktop.DBus"
	dbusServicePath      = "/org/freedesktop/DBus"
	dbusNameOwnerChanged = "NameOwnerChanged"
)

// trackSession records the bus client that created a session, so that the
// session can be ended if the client disappears without ending it.
func (s *Interface) trackSession(sender dbus.Sender, sessionID string) {
	if sender == "" {
		return
	}

	s.sessionsMu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]dbus.Sender)
	}
	s.sessions[sessionID] = sender
	s.sessionsMu.Unlock()

	// The client may have disappeared while the session was being created, in
	// which case its name owner change has already been notified.
	if s.service != nil && !s.service.hasNameOwner(sender) {
		s.endSessionsForSender(sender)
	}
}

// untrackSession forgets a session that is not active anymore.
func (s *Interface) untrackSession(sessionID string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	delete(s.sessions, sessionID)
}

// endSessionsForSender ends all the sessions created by the given bus client.
func (s *Interface) endSessionsForSender(sender dbus.Sender) {
	s.sessionsMu.Lock()
	sessionIDs := make([]string, 0, len(s.sessions))
	for sessionID, owner := range s.sessions {
		if owner != sender {
			continue
		}
		sessionIDs = append(sessionIDs, sessionID)
		delete(s.sessions, sessionID)
	}
	s.sessionsMu.Unlock()

	if s.broker == nil {
		return
	}

	for _, sessionID := range sessionIDs {
		log.Debugf(context.Background(), "%s: Ending session %s of vanished client %s",
			s.iface, sessionID, sender)
		if err := s.broker.EndSession(sessionID); err != nil {
			log.Warningf(context.Background(), "%s: Could not end session %s of vanished client %s: %v",
				s.iface, sessionID, sender, err)
		}
	}
}

// hasNameOwner returns whether the given bus client is still connected.
func (s *Service) hasNameOwner(sender dbus.Sender) bool {
	if s.conn == nil {
		return true
	}

	var owner string
	err := s.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, string(sender)).Store(&owner)
	if err != nil {
		var dbusErr dbus.Error
		if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" {
			return false
		}

		// Keep the session when the client state is unknown, as dropping it
		// could abort a login that is still in progress.
		log.Warningf(context.Background(), "Could not check whether client %s is still connected: %v", sender, err)
		return true
	}

	return owner != ""
}

// watchNameOwnerChanges ends the sessions of the clients that disappear from
// the bus, as they will not be able to end them anymore.
func (s *Service) watchNameOwnerChanges() error {
	err := s.conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbusServicePath),
		dbus.WithMatchInterface(dbusService),
		dbus.WithMatchMember(dbusNameOwnerChanged),
	)
	if err != nil {
		return err
	}

	s.signals = make(chan *dbus.Signal, 16)
	s.conn.Signal(s.signals)

	go func() {
		for {
			select {
			case signal, ok := <-s.signals:
				if !ok {
					return
				}
				s.handleNameOwnerChanged(signal)
			case <-s.serve:
				return
			}
		}
	}()

	return nil
}

func (s *Service) handleNameOwnerChanged(signal *dbus.Signal) {
	// Only the bus itself can be trusted about its clients: any other sender
	// may be a client forging a disconnection of somebody else.
	if signal.Sender != dbusService {
		return
	}

	if signal.Name != dbusService+"."+dbusNameOwnerChanged || len(signal.Body) != 3 {
		return
	}

	name, _ := signal.Body[0].(string)
	oldOwner, _ := signal.Body[1].(string)
	newOwner, _ := signal.Body[2].(string)

	// A unique name has no owner anymore, so the client is gone for good.
	if newOwner != "" || oldOwner == "" || name != oldOwner {
		return
	}

	log.Debugf(context.Background(), "Client %s disappeared from the bus, ending its sessions", oldOwner)
	for _, iface := range s.interfaces {
		iface.endSessionsForSender(dbus.Sender(oldOwner))
	}
}
