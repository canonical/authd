package adapter

import (
	"github.com/msteinert/pam/v2"
)

// Various signalling return messaging to PAM.

// PamReturnStatus is the interface that all PAM return types should implement.
type PamReturnStatus interface {
	Message() string
}

// PamReturnError is an interface that PAM errors return types should implement.
type PamReturnError interface {
	PamReturnStatus
	Status() pam.Error
}

// PamSuccess signals PAM module to return with provided pam.Success and Quit tea.Model.
type PamSuccess struct {
	BrokerID string
	msg      string
}

// Message returns the message that should be sent to pam as info message.
func (p PamSuccess) Message() string {
	return p.msg
}

// PamNewUser signals that a new user account was just created on the first SSH
// login. The SSH session must be restarted because sshd bound to the pre-auth
// UID before authentication ran. The message is shown to the user as
// pam.TextInfo before the PAM module returns pam.ErrAuth.
type PamNewUser struct {
	msg string
}

// Message returns the informational message to send to the user.
func (p PamNewUser) Message() string {
	return p.msg
}

// Status returns pam.ErrMaxtries so that the SSH session is closed without
// offering another retry within the same connection. The user must start a
// fresh connection, at which point sshd will look up the user in the authd
// database and get the correct UID.
func (p PamNewUser) Status() pam.Error {
	return pam.ErrMaxtries
}

// pamError signals PAM module to return the provided error message and Quit tea.Model.
type pamError struct {
	status pam.Error
	msg    string
}

// Status returns the PAM exit status code.
func (p pamError) Status() pam.Error {
	return p.status
}

// Message returns the message that should be sent to pam as error message.
func (p pamError) Message() string {
	if p.msg != "" {
		return p.msg
	}
	if p.status == pam.ErrIgnore {
		return ""
	}
	return p.status.Error()
}
