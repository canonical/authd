// Package pam provides a wrapper for the PAM application API.
package pam

/*
#include "transaction.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"
)

const maxNumMsg = C.PAM_MAX_NUM_MSG

// ModuleTransaction is an interface that a pam module transaction
// should implement.
type ModuleTransaction interface {
	SetItem(Item, string) error
	GetItem(Item) (string, error)
	PutEnv(nameVal string) error
	GetEnv(name string) string
	GetEnvList() (map[string]string, error)
	GetUser(prompt string) (string, error)
	SetData(key string, data any) error
	GetData(key string) (any, error)
	StartStringConv(style Style, prompt string) (StringConvResponse, error)
	StartStringConvf(style Style, format string, args ...interface{}) (
		StringConvResponse, error)
	StartBinaryConv([]byte) (BinaryConvResponse, error)
	StartConv(ConvRequest) (ConvResponse, error)
	StartConvMulti([]ConvRequest) ([]ConvResponse, error)
}

// ModuleHandlerFunc is a function type used by the ModuleHandler.
type ModuleHandlerFunc func(ModuleTransaction, Flags, []string) error

// ModuleTransaction is the module-side handle for a PAM transaction.
type moduleTransaction struct {
	transactionBase
	convMutex *sync.Mutex
}

// ModuleHandler is an interface for objects that can be used to create
// PAM modules from go.
type ModuleHandler interface {
	AcctMgmt(ModuleTransaction, Flags, []string) error
	Authenticate(ModuleTransaction, Flags, []string) error
	ChangeAuthTok(ModuleTransaction, Flags, []string) error
	CloseSession(ModuleTransaction, Flags, []string) error
	OpenSession(ModuleTransaction, Flags, []string) error
	SetCred(ModuleTransaction, Flags, []string) error
}

// ModuleTransactionInvoker is an interface that a pam module transaction
// should implement to redirect requests from C handlers to go,
type ModuleTransactionInvoker interface {
	ModuleTransaction
	InvokeHandler(handler ModuleHandlerFunc, flags Flags, args []string) error
}

// NewModuleTransactionParallelConv allows initializing a transaction from the
// module side. Conversations using this transaction can be multi-thread, but
// this requires the application loading the module to support this, otherwise
// we may just break their assumptions.
func NewModuleTransactionParallelConv(handle NativeHandle) ModuleTransaction {
	return &moduleTransaction{transactionBase{handle: handle}, nil}
}

// NewModuleTransactionInvoker allows initializing a transaction invoker from the
// module side.
func NewModuleTransactionInvoker(handle NativeHandle) ModuleTransactionInvoker {
	return &moduleTransaction{transactionBase{handle: handle}, &sync.Mutex{}}
}

// NewModuleTransactionInvokerParallelConv allows initializing a transaction invoker
// from the module side.
// Conversations using this transaction can be multi-thread, but this requires
// the application loading the module to support this, otherwise we may just
// break their assumptions.
func NewModuleTransactionInvokerParallelConv(handle NativeHandle) ModuleTransactionInvoker {
	return &moduleTransaction{transactionBase{handle: handle}, nil}
}

func (m *moduleTransaction) InvokeHandler(handler ModuleHandlerFunc,
	flags Flags, args []string) error {
	invoker := func() error {
		if handler == nil {
			return ErrIgnore
		}
		err := handler(m, flags, args)
		if err != nil {
			service, _ := m.GetItem(Service)

			var pamErr Error
			if !errors.As(err, &pamErr) {
				err = ErrSystem
			}

			if pamErr == ErrIgnore || service == "" {
				return err
			}

			return fmt.Errorf("%s failed: %w", service, err)
		}
		return nil
	}
	err := invoker()
	if errors.Is(err, Error(0)) {
		err = nil
	}
	var status int32
	if err != nil {
		status = int32(ErrSystem)

		var pamErr Error
		if errors.As(err, &pamErr) {
			status = int32(pamErr)
		}
	}
	m.lastStatus.Store(status)
	return err
}

type moduleTransactionIface interface {
	getUser(outUser **C.char, prompt *C.char) C.int
	setData(key *C.char, handle C.uintptr_t) C.int
	getData(key *C.char, outHandle *C.uintptr_t) C.int
	getConv() (*C.struct_pam_conv, error)
	hasBinaryProtocol() bool
	startConv(conv *C.struct_pam_conv, nMsg C.int,
		messages **C.struct_pam_message,
		outResponses **C.struct_pam_response) C.int
}

func (m *moduleTransaction) getUser(outUser **C.char, prompt *C.char) C.int {
	return C.pam_get_user(m.handle, outUser, prompt)
}

// getUserImpl is the default implementation for GetUser, but kept as private so
// that can be used to test the pam package
func (m *moduleTransaction) getUserImpl(iface moduleTransactionIface,
	prompt string) (string, error) {
	var user *C.char
	var cPrompt = C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))
	err := m.handlePamStatus(iface.getUser(&user, cPrompt))
	if err != nil {
		return "", err
	}
	return C.GoString(user), nil
}

// GetUser is similar to GetItem(User), but it would start a conversation if
// no user is currently set in PAM.
func (m *moduleTransaction) GetUser(prompt string) (string, error) {
	return m.getUserImpl(m, prompt)
}

// SetData allows to save any value in the module data that is preserved
// during the whole time the module is loaded.
func (m *moduleTransaction) SetData(key string, data any) error {
	return m.setDataImpl(m, key, data)
}

func (m *moduleTransaction) setData(key *C.char, handle C.uintptr_t) C.int {
	return C.set_data(m.handle, key, handle)
}

// setDataImpl is the implementation for SetData for testing purposes.
func (m *moduleTransaction) setDataImpl(iface moduleTransactionIface,
	key string, data any) error {
	var cKey = C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	var handle cgo.Handle
	if data != nil {
		handle = cgo.NewHandle(data)
	}
	return m.handlePamStatus(iface.setData(cKey, C.uintptr_t(handle)))
}

//export _go_pam_data_cleanup
func _go_pam_data_cleanup(h NativeHandle, handle C.uintptr_t, status C.int) {
	cgo.Handle(handle).Delete()
}

// GetData allows to get any value from the module data saved using SetData
// that is preserved across the whole time the module is loaded.
func (m *moduleTransaction) GetData(key string) (any, error) {
	return m.getDataImpl(m, key)
}

func (m *moduleTransaction) getData(key *C.char, outHandle *C.uintptr_t) C.int {
	return C.get_data(m.handle, key, outHandle)
}

// getDataImpl is the implementation for GetData for testing purposes.
func (m *moduleTransaction) getDataImpl(iface moduleTransactionIface,
	key string) (any, error) {
	var cKey = C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	var handle C.uintptr_t
	if err := m.handlePamStatus(iface.getData(cKey, &handle)); err != nil {
		return nil, err
	}
	if goHandle := cgo.Handle(handle); goHandle != cgo.Handle(0) {
		return goHandle.Value(), nil
	}

	return nil, m.handlePamStatus(C.int(ErrNoModuleData))
}

// getConv is a private function to get the conversation pointer to be used
// with C.do_conv() to initiate conversations.
func (m *moduleTransaction) getConv() (*C.struct_pam_conv, error) {
	var convPtr unsafe.Pointer

	if err := m.handlePamStatus(
		C.pam_get_item(m.handle, C.PAM_CONV, &convPtr)); err != nil {
		return nil, err
	}

	return (*C.struct_pam_conv)(convPtr), nil
}

// ConvRequest is an interface that all the Conversation requests should
// implement.
type ConvRequest interface {
	Style() Style
}

// ConvResponse is an interface that all the Conversation responses should
// implement.
type ConvResponse interface {
	Style() Style
}

// StringConvRequest is a ConvRequest for performing text-based conversations.
type StringConvRequest struct {
	style  Style
	prompt string
}

// NewStringConvRequest creates a new StringConvRequest.
func NewStringConvRequest(style Style, prompt string) StringConvRequest {
	return StringConvRequest{style, prompt}
}

// Style returns the conversation style of the StringConvRequest.
func (s StringConvRequest) Style() Style {
	return s.style
}

// Prompt returns the conversation style of the StringConvRequest.
func (s StringConvRequest) Prompt() string {
	return s.prompt
}

// StringConvResponse is an interface that string Conversation responses implements.
type StringConvResponse interface {
	ConvResponse
	Response() string
}

// stringConvResponse is a StringConvResponse implementation used for text-based
// conversation responses.
type stringConvResponse struct {
	style    Style
	response string
}

// Style returns the conversation style of the StringConvResponse.
func (s stringConvResponse) Style() Style {
	return s.style
}

// Response returns the string response of the conversation.
func (s stringConvResponse) Response() string {
	return s.response
}

// BinaryFinalizer is a type of function that can be used to release
// the binary when it's not required anymore
type BinaryFinalizer func(BinaryPointer)

// BinaryConvRequester is the interface that binary ConvRequests should
// implement
type BinaryConvRequester interface {
	ConvRequest
	Pointer() BinaryPointer
	CreateResponse(BinaryPointer) BinaryConvResponse
	Release()
}

// BinaryConvRequest is a ConvRequest for performing binary conversations.
type BinaryConvRequest struct {
	ptr               atomic.Uintptr
	finalizer         BinaryFinalizer
	responseFinalizer BinaryFinalizer
}

// NewBinaryConvRequestFull creates a new BinaryConvRequest with finalizer
// for response BinaryResponse.
func NewBinaryConvRequestFull(ptr BinaryPointer, finalizer BinaryFinalizer,
	responseFinalizer BinaryFinalizer) *BinaryConvRequest {
	b := &BinaryConvRequest{finalizer: finalizer, responseFinalizer: responseFinalizer}
	b.ptr.Store(uintptr(ptr))
	if ptr == nil || finalizer == nil {
		return b
	}

	// The ownership of the data here is temporary
	runtime.SetFinalizer(b, func(b *BinaryConvRequest) { b.Release() })
	return b
}

// NewBinaryConvRequest creates a new BinaryConvRequest
func NewBinaryConvRequest(ptr BinaryPointer, finalizer BinaryFinalizer) *BinaryConvRequest {
	return NewBinaryConvRequestFull(ptr, finalizer, finalizer)
}

// NewBinaryConvRequestFromBytes creates a new BinaryConvRequest from an array
// of bytes.
func NewBinaryConvRequestFromBytes(bytes []byte) *BinaryConvRequest {
	if bytes == nil {
		return &BinaryConvRequest{}
	}
	return NewBinaryConvRequest(BinaryPointer(C.CBytes(bytes)),
		func(ptr BinaryPointer) { C.free(unsafe.Pointer(ptr)) })
}

// Style returns the response style for the request, so always BinaryPrompt.
func (b *BinaryConvRequest) Style() Style {
	return BinaryPrompt
}

// Pointer returns the conversation style of the StringConvRequest.
func (b *BinaryConvRequest) Pointer() BinaryPointer {
	ptr := b.ptr.Load()
	return *(*BinaryPointer)(unsafe.Pointer(&ptr))
}

// CreateResponse creates a new BinaryConvResponse from the request
func (b *BinaryConvRequest) CreateResponse(ptr BinaryPointer) BinaryConvResponse {
	bcr := &binaryConvResponse{ptr, b.responseFinalizer, &sync.Mutex{}}
	runtime.SetFinalizer(bcr, func(bcr *binaryConvResponse) {
		bcr.Release()
	})
	return bcr
}

// Release releases the resources allocated by the request
func (b *BinaryConvRequest) Release() {
	ptr := b.ptr.Swap(0)
	if b.finalizer != nil {
		b.finalizer(*(*BinaryPointer)(unsafe.Pointer(&ptr)))
		runtime.SetFinalizer(b, nil)
	}
}

// BinaryDecoder is a function type for decode the a binary pointer data into
// bytes
type BinaryDecoder func(BinaryPointer) ([]byte, error)

// BinaryConvResponse is a subtype of ConvResponse used for binary
// conversation responses.
type BinaryConvResponse interface {
	ConvResponse
	Data() BinaryPointer
	Decode(BinaryDecoder) ([]byte, error)
	Release()
}

type binaryConvResponse struct {
	ptr       BinaryPointer
	finalizer BinaryFinalizer
	mutex     *sync.Mutex
}

// Style returns the response style for the response, so always BinaryPrompt.
func (b binaryConvResponse) Style() Style {
	return BinaryPrompt
}

// Data returns the response native pointer, it's up to the protocol to parse
// it accordingly.
func (b *binaryConvResponse) Data() BinaryPointer {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.ptr
}

// Decode decodes the binary data using the provided decoder function.
func (b *binaryConvResponse) Decode(decoder BinaryDecoder) (
	[]byte, error) {
	if decoder == nil {
		return nil, errors.New("nil decoder provided")
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return decoder(b.ptr)
}

// Release releases the binary conversation response data.
// This is also automatically via a finalizer, but applications may control
// this explicitly deferring execution of this.
func (b *binaryConvResponse) Release() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	ptr := b.ptr
	b.ptr = nil
	if b.finalizer != nil {
		b.finalizer(ptr)
	} else {
		C.free(unsafe.Pointer(ptr))
	}
}

// StartStringConv starts a text-based conversation using the provided style
// and prompt.
func (m *moduleTransaction) StartStringConv(style Style, prompt string) (
	StringConvResponse, error) {
	return m.startStringConvImpl(m, style, prompt)
}

func (m *moduleTransaction) startStringConvImpl(iface moduleTransactionIface,
	style Style, prompt string) (
	StringConvResponse, error) {
	switch style {
	case BinaryPrompt:
		return nil, fmt.Errorf("%w: binary style is not supported", ErrConv)
	}

	res, err := m.startConvImpl(iface, NewStringConvRequest(style, prompt))
	if err != nil {
		return nil, err
	}

	stringRes, _ := res.(stringConvResponse)
	return stringRes, nil
}

// StartStringConvf allows to start string conversation with formatting support.
func (m *moduleTransaction) StartStringConvf(style Style, format string, args ...interface{}) (
	StringConvResponse, error) {
	return m.StartStringConv(style, fmt.Sprintf(format, args...))
}

// HasBinaryProtocol checks if binary protocol is supported.
func (m *moduleTransaction) hasBinaryProtocol() bool {
	return CheckPamHasBinaryProtocol()
}

// StartBinaryConv starts a binary conversation using the provided bytes.
func (m *moduleTransaction) StartBinaryConv(bytes []byte) (
	BinaryConvResponse, error) {
	return m.startBinaryConvImpl(m, bytes)
}

func (m *moduleTransaction) startBinaryConvImpl(iface moduleTransactionIface,
	bytes []byte) (
	BinaryConvResponse, error) {
	res, err := m.startConvImpl(iface, NewBinaryConvRequestFromBytes(bytes))
	if err != nil {
		return nil, err
	}

	binaryRes, _ := res.(BinaryConvResponse)
	return binaryRes, nil
}

// StartConv initiates a PAM conversation using the provided ConvRequest.
func (m *moduleTransaction) StartConv(req ConvRequest) (
	ConvResponse, error) {
	return m.startConvImpl(m, req)
}

func (m *moduleTransaction) startConvImpl(iface moduleTransactionIface, req ConvRequest) (
	ConvResponse, error) {
	resp, err := m.startConvMultiImpl(iface, []ConvRequest{req})
	if err != nil {
		return nil, err
	}
	if len(resp) != 1 {
		return nil, fmt.Errorf("%w: not enough values returned", ErrConv)
	}
	return resp[0], nil
}

func (m *moduleTransaction) startConv(conv *C.struct_pam_conv, nMsg C.int,
	messages **C.struct_pam_message, outResponses **C.struct_pam_response) C.int {
	return C.start_pam_conv(conv, nMsg, messages, outResponses)
}

// startConvMultiImpl is the implementation for GetData for testing purposes.
func (m *moduleTransaction) startConvMultiImpl(iface moduleTransactionIface,
	requests []ConvRequest) (responses []ConvResponse, err error) {
	defer func() {
		if err == nil {
			_ = m.handlePamStatus(success)
			return
		}
		var pamErr Error
		if !errors.As(err, &pamErr) {
			err = errors.Join(ErrConv, err)
			pamErr = ErrConv
		}
		_ = m.handlePamStatus(C.int(pamErr))
	}()

	if len(requests) == 0 {
		return nil, errors.New("no requests defined")
	}
	if len(requests) > maxNumMsg {
		return nil, errors.New("too many requests")
	}

	conv, err := iface.getConv()
	if err != nil {
		return nil, err
	}

	if conv == nil || conv.conv == nil {
		return nil, errors.New("impossible to find conv handler")
	}

	// FIXME: Just use make([]C.struct_pam_message, 0, len(requests))
	// and append, when it's possible to use runtime.Pinner
	var cMessagePtr *C.struct_pam_message
	cMessages := (**C.struct_pam_message)(C.calloc(C.size_t(len(requests)),
		(C.size_t)(unsafe.Sizeof(cMessagePtr))))
	defer C.free(unsafe.Pointer(cMessages))
	goMsgs := unsafe.Slice(cMessages, len(requests))

	for i, req := range requests {
		var cBytes unsafe.Pointer
		switch r := req.(type) {
		case StringConvRequest:
			cBytes = unsafe.Pointer(C.CString(r.Prompt()))
			defer C.free(cBytes)
		case BinaryConvRequester:
			if !iface.hasBinaryProtocol() {
				return nil, errors.New("%w: binary protocol is not supported")
			}
			cBytes = unsafe.Pointer(r.Pointer())
		default:
			return nil, fmt.Errorf("unsupported conversation type %#v", r)
		}

		cMessage := (*C.struct_pam_message)(C.calloc(1,
			(C.size_t)(unsafe.Sizeof(*goMsgs[i]))))
		defer C.free(unsafe.Pointer(cMessage))
		cMessage.msg_style = C.int(req.Style())
		cMessage.msg = (*C.char)(cBytes)
		goMsgs[i] = cMessage
	}

	if m.convMutex != nil {
		m.convMutex.Lock()
		defer m.convMutex.Unlock()
	}
	var cResponses *C.struct_pam_response
	ret := iface.startConv(conv, C.int(len(requests)), cMessages, &cResponses)
	if ret != success {
		return nil, Error(ret)
	}

	goResponses := unsafe.Slice(cResponses, len(requests))
	defer func() {
		for i, resp := range goResponses {
			if resp.resp == nil {
				continue
			}
			switch req := requests[i].(type) {
			case BinaryConvRequester:
				// In the binary prompt case, we need to rely on the provided
				// finalizer to release the response, so let's create a new one.
				req.CreateResponse(BinaryPointer(resp.resp)).Release()
			default:
				C.free(unsafe.Pointer(resp.resp))
			}
		}
		C.free(unsafe.Pointer(cResponses))
	}()

	responses = make([]ConvResponse, 0, len(requests))
	for i, resp := range goResponses {
		request := requests[i]
		msgStyle := request.Style()
		switch msgStyle {
		case PromptEchoOff:
			fallthrough
		case PromptEchoOn:
			fallthrough
		case ErrorMsg:
			fallthrough
		case TextInfo:
			responses = append(responses, stringConvResponse{
				style:    msgStyle,
				response: C.GoString(resp.resp),
			})
		case BinaryPrompt:
			// Let's steal the resp ownership here, so that the request
			// finalizer won't act on it.
			bcr, _ := request.(BinaryConvRequester)
			resp := bcr.CreateResponse(BinaryPointer(resp.resp))
			goResponses[i].resp = nil
			responses = append(responses, resp)
		default:
			return nil,
				fmt.Errorf("unsupported conversation type %v", msgStyle)
		}
	}

	return responses, nil
}

// StartConvMulti initiates a PAM conversation with multiple ConvRequest's.
func (m *moduleTransaction) StartConvMulti(requests []ConvRequest) (
	[]ConvResponse, error) {
	return m.startConvMultiImpl(m, requests)
}
