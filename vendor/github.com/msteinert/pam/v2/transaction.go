// Package pam provides a wrapper for the PAM application API.
package pam

//#cgo CFLAGS: -Wall -Wno-unused-variable -std=c99
//#cgo LDFLAGS: -lpam
//
//#include "transaction.h"
import "C"

import (
	"strings"
	"sync/atomic"
	"unsafe"
)

// success indicates a successful function return.
const success = C.PAM_SUCCESS

// Style is the type of message that the conversation handler should display.
type Style int

// Conversation handler style types.
const (
	// PromptEchoOff indicates the conversation handler should obtain a
	// string without echoing any text.
	PromptEchoOff Style = C.PAM_PROMPT_ECHO_OFF
	// PromptEchoOn indicates the conversation handler should obtain a
	// string while echoing text.
	PromptEchoOn Style = C.PAM_PROMPT_ECHO_ON
	// ErrorMsg indicates the conversation handler should display an
	// error message.
	ErrorMsg Style = C.PAM_ERROR_MSG
	// TextInfo indicates the conversation handler should display some
	// text.
	TextInfo Style = C.PAM_TEXT_INFO
	// BinaryPrompt indicates the conversation handler that should implement
	// the private binary protocol
	BinaryPrompt Style = C.PAM_BINARY_PROMPT
)

// BinaryPointer exposes the type used for the data in a binary conversation
// it represents a pointer to data that is produced by the module and that
// must be parsed depending on the protocol in use
type BinaryPointer unsafe.Pointer

// NativeHandle is the type of the native PAM handle for a transaction so that
// it can be exported
type NativeHandle = *C.pam_handle_t

// transactionBase is a handler for a PAM transaction that can be used to
// group the operations that can be performed both by the application and the
// module side
type transactionBase struct {
	handle     NativeHandle
	lastStatus atomic.Int32
}

// Allows to call pam functions managing return status
func (t *transactionBase) handlePamStatus(cStatus C.int) error {
	t.lastStatus.Store(int32(cStatus))
	if status := Error(cStatus); status != success {
		return status
	}
	return nil
}

// Item is a an PAM information type.
type Item int

// PAM Item types.
const (
	// Service is the name which identifies the PAM stack.
	Service Item = C.PAM_SERVICE
	// User identifies the username identity used by a service.
	User Item = C.PAM_USER
	// Tty is the terminal name.
	Tty Item = C.PAM_TTY
	// Rhost is the requesting host name.
	Rhost Item = C.PAM_RHOST
	// Authtok is the currently active authentication token.
	Authtok Item = C.PAM_AUTHTOK
	// Oldauthtok is the old authentication token.
	Oldauthtok Item = C.PAM_OLDAUTHTOK
	// Ruser is the requesting user name.
	Ruser Item = C.PAM_RUSER
	// UserPrompt is the string use to prompt for a username.
	UserPrompt Item = C.PAM_USER_PROMPT
	// FailDelay is the app supplied function to override failure delays.
	FailDelay Item = C.PAM_FAIL_DELAY
	// Xdisplay is the X display name
	Xdisplay Item = C.PAM_XDISPLAY
	// Xauthdata is the X server authentication data.
	Xauthdata Item = C.PAM_XAUTHDATA
	// AuthtokType is the type for pam_get_authtok
	AuthtokType Item = C.PAM_AUTHTOK_TYPE
)

// SetItem sets a PAM information item.
func (t *transactionBase) SetItem(i Item, item string) error {
	cs := unsafe.Pointer(C.CString(item))
	defer C.free(cs)
	return t.handlePamStatus(C.pam_set_item(t.handle, C.int(i), cs))
}

// GetItem retrieves a PAM information item.
func (t *transactionBase) GetItem(i Item) (string, error) {
	var s unsafe.Pointer
	err := t.handlePamStatus(C.pam_get_item(t.handle, C.int(i), &s))
	if err != nil {
		return "", err
	}
	return C.GoString((*C.char)(s)), nil
}

// Flags are inputs to various PAM functions than be combined with a bitwise
// or. Refer to the official PAM documentation for which flags are accepted
// by which functions.
type Flags int

// PAM Flag types.
const (
	// Silent indicates that no messages should be emitted.
	Silent Flags = C.PAM_SILENT
	// DisallowNullAuthtok indicates that authorization should fail
	// if the user does not have a registered authentication token.
	DisallowNullAuthtok Flags = C.PAM_DISALLOW_NULL_AUTHTOK
	// EstablishCred indicates that credentials should be established
	// for the user.
	EstablishCred Flags = C.PAM_ESTABLISH_CRED
	// DeleteCred indicates that credentials should be deleted.
	DeleteCred Flags = C.PAM_DELETE_CRED
	// ReinitializeCred indicates that credentials should be fully
	// reinitialized.
	ReinitializeCred Flags = C.PAM_REINITIALIZE_CRED
	// RefreshCred indicates that the lifetime of existing credentials
	// should be extended.
	RefreshCred Flags = C.PAM_REFRESH_CRED
	// ChangeExpiredAuthtok indicates that the authentication token
	// should be changed if it has expired.
	ChangeExpiredAuthtok Flags = C.PAM_CHANGE_EXPIRED_AUTHTOK
	// PrelimCheck indicates that the password service should only
	// perform preliminary checks. No passwords should be updated.
	PrelimCheck Flags = C.PAM_PRELIM_CHECK
	// UpdateAuthtok indicates that password service should update
	// passwords Note: [pam.PrelimCheck] and [pam.UpdateAuthtok] cannot
	// both be set simultaneously!
	UpdateAuthtok Flags = C.PAM_UPDATE_AUTHTOK
)

// PutEnv adds or changes the value of PAM environment variables.
//
// NAME=value will set a variable to a value.
// NAME= will set a variable to an empty value.
// NAME (without an "=") will delete a variable.
func (t *transactionBase) PutEnv(nameval string) error {
	cs := C.CString(nameval)
	defer C.free(unsafe.Pointer(cs))
	return t.handlePamStatus(C.pam_putenv(t.handle, cs))
}

// GetEnv is used to retrieve a PAM environment variable.
func (t *transactionBase) GetEnv(name string) string {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	value := C.pam_getenv(t.handle, cs)
	if value == nil {
		return ""
	}
	return C.GoString(value)
}

func next(p **C.char) **C.char {
	return (**C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + unsafe.Sizeof(p)))
}

// GetEnvList returns a copy of the PAM environment as a map.
func (t *transactionBase) GetEnvList() (map[string]string, error) {
	env := make(map[string]string)
	p := C.pam_getenvlist(t.handle)
	if p == nil {
		t.lastStatus.Store(int32(ErrBuf))
		return nil, ErrBuf
	}
	t.lastStatus.Store(success)
	for q := p; *q != nil; q = next(q) {
		chunks := strings.SplitN(C.GoString(*q), "=", 2)
		if len(chunks) == 2 {
			env[chunks[0]] = chunks[1]
		}
		C.free(unsafe.Pointer(*q))
	}
	C.free(unsafe.Pointer(p))
	return env, nil
}

// CheckPamHasStartConfdir return if pam on system supports pam_system_confdir
func CheckPamHasStartConfdir() bool {
	return C.check_pam_start_confdir() == 0
}

// CheckPamHasBinaryProtocol return if pam on system supports PAM_BINARY_PROMPT
func CheckPamHasBinaryProtocol() bool {
	return C.BINARY_PROMPT_IS_SUPPORTED != 0
}
