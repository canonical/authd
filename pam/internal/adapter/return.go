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
	AuthTok  string
	msg      string
}

// Message returns the message that should be sent to pam as info message.
func (p PamSuccess) Message() string {
	return p.msg
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
