// Package pam_test includes test tools for the PAM module
package pam_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/msteinert/pam/v2"
)

// ModuleTransactionDummy is an implementation of [pam.ModuleTransaction] for
// testing purposes.
type ModuleTransactionDummy struct {
	Items       map[pam.Item]string
	Env         map[string]string
	Data        map[string]any
	convHandler pam.ConversationHandler
}

// NewModuleTransactionDummy returns a new PamModuleTransactionDummy.
func NewModuleTransactionDummy(convHandler pam.ConversationHandler) pam.ModuleTransaction {
	return &ModuleTransactionDummy{
		convHandler: convHandler,
		Data:        make(map[string]any),
		Env:         make(map[string]string),
		Items:       make(map[pam.Item]string),
	}
}

// InvokeHandler is called by the C code to invoke the proper handler.
func (m *ModuleTransactionDummy) InvokeHandler(handler pam.ModuleHandlerFunc,
	flags pam.Flags, args []string) error {
	return pam.ErrAbort
}

// SetItem sets a PAM information item.
func (m *ModuleTransactionDummy) SetItem(item pam.Item, value string) error {
	if item <= 0 {
		return pam.ErrBadItem
	}

	m.Items[item] = value
	return nil
}

// GetItem retrieves a PAM information item.
func (m *ModuleTransactionDummy) GetItem(item pam.Item) (string, error) {
	if item <= 0 {
		return "", pam.ErrBadItem
	}
	return m.Items[item], nil
}

// PutEnv adds or changes the value of PAM environment variables.
//
// NAME=value will set a variable to a value.
// NAME= will set a variable to an empty value.
// NAME (without an "=") will delete a variable.
func (m *ModuleTransactionDummy) PutEnv(nameVal string) error {
	env, value, found := strings.Cut(nameVal, "=")
	if !found {
		if _, found := m.Env[env]; !found {
			return pam.ErrBadItem
		}
		delete(m.Env, env)
		return nil
	}
	if env == "" {
		return pam.ErrBadItem
	}
	m.Env[env] = value
	return nil
}

// GetEnv is used to retrieve a PAM environment variable.
func (m *ModuleTransactionDummy) GetEnv(name string) string {
	return m.Env[name]
}

// GetEnvList returns a copy of the PAM environment as a map.
func (m *ModuleTransactionDummy) GetEnvList() (map[string]string, error) {
	return m.Env, nil
}

// GetUser is similar to GetItem(User), but it would start a conversation if
// no user is currently set in PAM.
func (m *ModuleTransactionDummy) GetUser(prompt string) (string, error) {
	user, err := m.GetItem(pam.User)
	if err != nil {
		return "", err
	}
	if user != "" {
		return user, nil
	}

	resp, err := m.StartStringConv(pam.PromptEchoOn, prompt)
	if err != nil {
		return "", err
	}

	return resp.Response(), nil
}

// SetData allows to save any value in the module data that is preserved
// during the whole time the module is loaded.
func (m *ModuleTransactionDummy) SetData(key string, data any) error {
	m.Data[key] = data
	return nil
}

// GetData allows to get any value from the module data saved using SetData
// that is preserved across the whole time the module is loaded.
func (m *ModuleTransactionDummy) GetData(key string) (any, error) {
	data, found := m.Data[key]
	if !found {
		return nil, pam.ErrNoModuleData
	}
	return data, nil
}

// StartStringConv starts a text-based conversation using the provided style
// and prompt.
func (m *ModuleTransactionDummy) StartStringConv(style pam.Style, prompt string) (
	pam.StringConvResponse, error) {
	if style == pam.BinaryPrompt {
		return nil, fmt.Errorf("%w: binary style is not supported", pam.ErrConv)
	}

	res, err := m.StartConv(pam.NewStringConvRequest(style, prompt))
	if err != nil {
		return nil, err
	}

	stringRes, ok := res.(pam.StringConvResponse)
	if !ok {
		return nil, fmt.Errorf("%w: can't convert to pam.StringConvResponse", pam.ErrConv)
	}
	return stringRes, nil
}

// StartStringConvf allows to start string conversation with formatting support.
func (m *ModuleTransactionDummy) StartStringConvf(style pam.Style, format string, args ...interface{}) (
	pam.StringConvResponse, error) {
	return m.StartStringConv(style, fmt.Sprintf(format, args...))
}

// StartBinaryConv starts a binary conversation using the provided bytes.
func (m *ModuleTransactionDummy) StartBinaryConv(bytes []byte) (
	pam.BinaryConvResponse, error) {
	res, err := m.StartConv(NewBinaryRequestDummyFromBytes(bytes))
	if err != nil {
		return nil, err
	}

	binaryRes, ok := res.(pam.BinaryConvResponse)
	if !ok {
		return nil, fmt.Errorf("%w: can't convert to pam.BinaryConvResponse", pam.ErrConv)
	}
	return binaryRes, nil
}

// StartConv initiates a PAM conversation using the provided ConvRequest.
func (m *ModuleTransactionDummy) StartConv(req pam.ConvRequest) (
	pam.ConvResponse, error) {
	resp, err := m.StartConvMulti([]pam.ConvRequest{req})
	if err != nil {
		return nil, err
	}
	if len(resp) != 1 {
		return nil, fmt.Errorf("%w: not enough values returned", pam.ErrConv)
	}
	return resp[0], nil
}

func (m *ModuleTransactionDummy) handleStringRequest(req pam.ConvRequest) (pam.StringConvResponse, error) {
	msgStyle := req.Style()
	if m.convHandler == nil {
		return nil, fmt.Errorf("no conversation handler provided for style %v", msgStyle)
	}
	stringReq, ok := req.(pam.StringConvRequest)
	if !ok {
		return nil, fmt.Errorf("conversation request is not a string request")
	}
	reply, err := m.convHandler.RespondPAM(msgStyle, stringReq.Prompt())
	if err != nil {
		return nil, err
	}

	return StringResponseDummy{
		msgStyle,
		reply,
	}, nil
}

func (m *ModuleTransactionDummy) handleBinaryRequest(req pam.ConvRequest) (pam.BinaryConvResponse, error) {
	if m.convHandler == nil {
		return nil, errors.New("no binary handler provided")
	}

	//nolint:forcetypeassert // req must be a pam.BinaryConvRequester, if that's not the case we should
	// just panic since this code is only expected to run in tests.
	binReq := req.(pam.BinaryConvRequester)

	switch handler := m.convHandler.(type) {
	case pam.BinaryConversationHandler:
		r, err := handler.RespondPAMBinary(binReq.Pointer())
		if err != nil {
			return nil, err
		}
		return binReq.CreateResponse(pam.BinaryPointer(&r)), nil

	case pam.BinaryPointerConversationHandler:
		r, err := handler.RespondPAMBinary(binReq.Pointer())
		if err != nil {
			if r != nil {
				resp := binReq.CreateResponse(r)
				resp.Release()
			}
			return nil, err
		}
		return binReq.CreateResponse(r), nil

	default:
		return nil, fmt.Errorf("unsupported conversation handler %#v", handler)
	}
}

// StartConvMulti initiates a PAM conversation with multiple ConvRequest's.
func (m *ModuleTransactionDummy) StartConvMulti(requests []pam.ConvRequest) (
	responses []pam.ConvResponse, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", pam.ErrConv, err)
		}
	}()

	if len(requests) == 0 {
		return nil, errors.New("no requests defined")
	}

	responses = make([]pam.ConvResponse, 0, len(requests))
	for _, req := range requests {
		msgStyle := req.Style()
		switch msgStyle {
		case pam.PromptEchoOff:
			fallthrough
		case pam.PromptEchoOn:
			fallthrough
		case pam.ErrorMsg:
			fallthrough
		case pam.TextInfo:
			response, err := m.handleStringRequest(req)
			if err != nil {
				return nil, err
			}
			responses = append(responses, response)
		case pam.BinaryPrompt:
			response, err := m.handleBinaryRequest(req)
			if err != nil {
				return nil, err
			}
			responses = append(responses, response)
		default:
			return nil, fmt.Errorf("unsupported conversation type %v", msgStyle)
		}
	}

	return responses, nil
}

// BinaryRequestDummy is a dummy pam.BinaryConvRequester implementation.
type BinaryRequestDummy struct {
	ptr pam.BinaryPointer
}

// NewBinaryRequestDummy creates a new BinaryConvRequest with finalizer
// for response BinaryResponse.
func NewBinaryRequestDummy(ptr pam.BinaryPointer) *BinaryRequestDummy {
	return &BinaryRequestDummy{ptr}
}

// NewBinaryRequestDummyFromBytes creates a new BinaryConvRequestDummy from
// an array of bytes.
func NewBinaryRequestDummyFromBytes(bytes []byte) *BinaryRequestDummy {
	if bytes == nil {
		return &BinaryRequestDummy{}
	}
	return NewBinaryRequestDummy(pam.BinaryPointer(&bytes))
}

// Style returns the response style for the request, so always BinaryPrompt.
func (b BinaryRequestDummy) Style() pam.Style {
	return pam.BinaryPrompt
}

// Pointer returns the conversation style of the StringConvRequest.
func (b BinaryRequestDummy) Pointer() pam.BinaryPointer {
	return b.ptr
}

// CreateResponse creates a new BinaryConvResponse from the request.
func (b BinaryRequestDummy) CreateResponse(ptr pam.BinaryPointer) pam.BinaryConvResponse {
	bcr := &BinaryResponseDummy{ptr}
	runtime.SetFinalizer(bcr, func(bcr *BinaryResponseDummy) {
		bcr.Release()
	})
	return bcr
}

// Release releases the resources allocated by the request.
func (b *BinaryRequestDummy) Release() {
	b.ptr = nil
}

// StringResponseDummy is a simple implementation of pam.StringConvResponse.
type StringResponseDummy struct {
	style   pam.Style
	content string
}

// Style returns the conversation style of the StringResponseDummy.
func (s StringResponseDummy) Style() pam.Style {
	return s.style
}

// Response returns the string response of the StringResponseDummy.
func (s StringResponseDummy) Response() string {
	return s.content
}

// BinaryResponseDummy is an implementation of pam.BinaryConvResponse.
type BinaryResponseDummy struct {
	ptr pam.BinaryPointer
}

// Style returns the response style for the response, so always BinaryPrompt.
func (b BinaryResponseDummy) Style() pam.Style {
	return pam.BinaryPrompt
}

// Data returns the response native pointer, it's up to the protocol to parse
// it accordingly.
func (b BinaryResponseDummy) Data() pam.BinaryPointer {
	return b.ptr
}

// Release releases the memory associated with the pointer.
func (b *BinaryResponseDummy) Release() {
	b.ptr = nil
	runtime.SetFinalizer(b, nil)
}

// Decode decodes the binary data using the provided decoder function.
func (b BinaryResponseDummy) Decode(decoder pam.BinaryDecoder) (
	[]byte, error) {
	if decoder == nil {
		return nil, errors.New("nil decoder provided")
	}
	return decoder(b.Data())
}
