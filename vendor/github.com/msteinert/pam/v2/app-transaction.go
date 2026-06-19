//go:build !go_pam_module

package pam

/*
#include "transaction.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync/atomic"
	"unsafe"
)

// ConversationHandler is an interface for objects that can be used as
// conversation callbacks during PAM authentication.
type ConversationHandler interface {
	// RespondPAM receives a message style and a message string. If the
	// message Style is PromptEchoOff or PromptEchoOn then the function
	// should return a response string.
	RespondPAM(Style, string) (string, error)
}

// BinaryConversationHandler is an interface for objects that can be used as
// conversation callbacks during PAM authentication if binary protocol is going
// to be supported.
type BinaryConversationHandler interface {
	ConversationHandler
	// RespondPAMBinary receives a pointer to the binary message. It's up to
	// the receiver to parse it according to the protocol specifications.
	// The function can return a byte array that will passed as pointer back
	// to the module.
	RespondPAMBinary(BinaryPointer) ([]byte, error)
}

// BinaryPointerConversationHandler is an interface for objects that can be used as
// conversation callbacks during PAM authentication if binary protocol is going
// to be supported.
type BinaryPointerConversationHandler interface {
	ConversationHandler
	// RespondPAMBinary receives a pointer to the binary message. It's up to
	// the receiver to parse it according to the protocol specifications.
	// The function must return a pointer that is allocated via malloc or
	// similar, as it's expected to be free'd by the conversation handler.
	RespondPAMBinary(BinaryPointer) (BinaryPointer, error)
}

// ConversationFunc is an adapter to allow the use of ordinary functions as
// conversation callbacks.
type ConversationFunc func(Style, string) (string, error)

// RespondPAM is a conversation callback adapter.
func (f ConversationFunc) RespondPAM(s Style, msg string) (string, error) {
	return f(s, msg)
}

// BinaryConversationFunc is an adapter to allow the use of ordinary functions
// as binary (only) conversation callbacks.
type BinaryConversationFunc func(BinaryPointer) ([]byte, error)

// RespondPAMBinary is a conversation callback adapter.
func (f BinaryConversationFunc) RespondPAMBinary(ptr BinaryPointer) ([]byte, error) {
	return f(ptr)
}

// RespondPAM is a dummy conversation callback adapter.
func (f BinaryConversationFunc) RespondPAM(Style, string) (string, error) {
	return "", ErrConv
}

// BinaryPointerConversationFunc is an adapter to allow the use of ordinary
// functions as binary pointer (only) conversation callbacks.
type BinaryPointerConversationFunc func(BinaryPointer) (BinaryPointer, error)

// RespondPAMBinary is a conversation callback adapter.
func (f BinaryPointerConversationFunc) RespondPAMBinary(ptr BinaryPointer) (BinaryPointer, error) {
	return f(ptr)
}

// RespondPAM is a dummy conversation callback adapter.
func (f BinaryPointerConversationFunc) RespondPAM(Style, string) (string, error) {
	return "", ErrConv
}

// _go_pam_conv_handler is a C wrapper for the conversation callback function.
//
//export _go_pam_conv_handler
func _go_pam_conv_handler(msg *C.struct_pam_message, c C.uintptr_t, outMsg **C.char) C.int {
	convHandler, ok := cgo.Handle(c).Value().(ConversationHandler)
	if !ok || convHandler == nil {
		return C.int(ErrConv)
	}
	replyMsg, r := pamConvHandler(Style(msg.msg_style), msg.msg, convHandler)
	*outMsg = replyMsg
	return r
}

// pamConvHandler is a Go wrapper for the conversation callback function.
func pamConvHandler(style Style, msg *C.char, handler ConversationHandler) (*C.char, C.int) {
	var r string
	var err error
	switch cb := handler.(type) {
	case BinaryConversationHandler:
		if style == BinaryPrompt {
			bytes, err := cb.RespondPAMBinary(BinaryPointer(msg))
			if err != nil {
				return nil, C.int(ErrConv)
			}
			if bytes == nil {
				return nil, success
			}
			return (*C.char)(C.CBytes(bytes)), success
		}
		handler = cb
	case BinaryPointerConversationHandler:
		if style == BinaryPrompt {
			ptr, err := cb.RespondPAMBinary(BinaryPointer(msg))
			if err != nil {
				defer C.free(unsafe.Pointer(ptr))
				return nil, C.int(ErrConv)
			}
			return (*C.char)(ptr), success
		}
		handler = cb
	case ConversationHandler:
		if style == BinaryPrompt {
			return nil, C.int(ErrConv)
		}
		handler = cb
	default:
		return nil, C.int(ErrConv)
	}
	r, err = handler.RespondPAM(style, C.GoString(msg))
	if err != nil {
		return nil, C.int(ErrConv)
	}
	return C.CString(r), success
}

// Transaction is the application's handle for a PAM transaction.
type Transaction struct {
	transactionBase

	conv *C.struct_pam_conv
	c    cgo.Handle
}

// Start initiates a new PAM transaction. Service is treated identically to
// how pam_start treats it internally.
//
// All application calls to PAM begin with Start*. The returned
// transaction provides an interface to the remainder of the API.
//
// It's responsibility of the Transaction owner to release all the resources
// allocated underneath by PAM by calling End() once done.
//
// It's not advised to End the transaction using a runtime.SetFinalizer unless
// you're absolutely sure that your stack is multi-thread friendly (normally it
// is not!) and using a LockOSThread/UnlockOSThread pair.
func Start(service, user string, handler ConversationHandler) (*Transaction, error) {
	return start(service, user, handler, "")
}

// StartFunc registers the handler func as a conversation handler and starts
// the transaction (see Start() documentation).
func StartFunc(service, user string, handler func(Style, string) (string, error)) (*Transaction, error) {
	return start(service, user, ConversationFunc(handler), "")
}

// StartConfDir initiates a new PAM transaction. Service is treated identically to
// how pam_start treats it internally.
// confdir allows to define where all pam services are defined. This is used to provide
// custom paths for tests.
//
// All application calls to PAM begin with Start*. The returned
// transaction provides an interface to the remainder of the API.
//
// It's responsibility of the Transaction owner to release all the resources
// allocated underneath by PAM by calling End() once done.
//
// It's not advised to End the transaction using a runtime.SetFinalizer unless
// you're absolutely sure that your stack is multi-thread friendly (normally it
// is not!) and using a LockOSThread/UnlockOSThread pair.
func StartConfDir(service, user string, handler ConversationHandler, confDir string) (*Transaction, error) {
	if !CheckPamHasStartConfdir() {
		return nil, fmt.Errorf(
			"%w: StartConfDir was used, but the pam version on the system is not recent enough",
			ErrSystem)
	}

	return start(service, user, handler, confDir)
}

func start(service, user string, handler ConversationHandler, confDir string) (*Transaction, error) {
	switch handler.(type) {
	case BinaryConversationHandler:
		if !CheckPamHasBinaryProtocol() {
			return nil, fmt.Errorf("%w: BinaryConversationHandler was used, but it is not supported by this platform",
				ErrSystem)
		}
	case BinaryPointerConversationHandler:
		if !CheckPamHasBinaryProtocol() {
			return nil, fmt.Errorf(
				"%w: BinaryPointerConversationHandler was used, but it is not supported by this platform",
				ErrSystem)
		}
	}
	t := &Transaction{
		conv: &C.struct_pam_conv{},
		c:    cgo.NewHandle(handler),
	}

	C.init_pam_conv(t.conv, C.uintptr_t(t.c))
	s := C.CString(service)
	defer C.free(unsafe.Pointer(s))
	var u *C.char
	if len(user) != 0 {
		u = C.CString(user)
		defer C.free(unsafe.Pointer(u))
	}
	var err error
	if confDir == "" {
		err = t.handlePamStatus(C.pam_start(s, u, t.conv, &t.handle))
	} else {
		c := C.CString(confDir)
		defer C.free(unsafe.Pointer(c))
		err = t.handlePamStatus(C.pam_start_confdir(s, u, t.conv, c, &t.handle))
	}
	if err != nil {
		var _ = t.End()
		return nil, err
	}
	return t, nil
}

// Authenticate is used to authenticate the user.
//
// Valid flags: Silent, DisallowNullAuthtok
func (t *Transaction) Authenticate(f Flags) error {
	return t.handlePamStatus(C.pam_authenticate(t.handle, C.int(f)))
}

// SetCred is used to establish, maintain and delete the credentials of a
// user.
//
// Valid flags: EstablishCred, DeleteCred, ReinitializeCred, RefreshCred
func (t *Transaction) SetCred(f Flags) error {
	return t.handlePamStatus(C.pam_setcred(t.handle, C.int(f)))
}

// AcctMgmt is used to determine if the user's account is valid.
//
// Valid flags: Silent, DisallowNullAuthtok
func (t *Transaction) AcctMgmt(f Flags) error {
	return t.handlePamStatus(C.pam_acct_mgmt(t.handle, C.int(f)))
}

// ChangeAuthTok is used to change the authentication token.
//
// Valid flags: Silent, ChangeExpiredAuthtok
func (t *Transaction) ChangeAuthTok(f Flags) error {
	return t.handlePamStatus(C.pam_chauthtok(t.handle, C.int(f)))
}

// OpenSession sets up a user session for an authenticated user.
//
// Valid flags: Slient
func (t *Transaction) OpenSession(f Flags) error {
	return t.handlePamStatus(C.pam_open_session(t.handle, C.int(f)))
}

// CloseSession closes a previously opened session.
//
// Valid flags: Silent
func (t *Transaction) CloseSession(f Flags) error {
	return t.handlePamStatus(C.pam_close_session(t.handle, C.int(f)))
}

// End cleans up the PAM handle and deletes the callback function.
// It must be called when done with the transaction.
func (t *Transaction) End() error {
	handle := atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&t.handle)), nil)
	if handle == nil {
		return nil
	}

	defer t.c.Delete()
	return t.handlePamStatus(C.pam_end((*C.pam_handle_t)(handle),
		C.int(t.lastStatus.Load())))
}
