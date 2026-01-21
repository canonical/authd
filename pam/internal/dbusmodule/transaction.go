// Package dbusmodule includes the tool for DBus PAM module interactions.
package dbusmodule

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
)

// Transaction is a [pam.Transaction] with dbus support.
type Transaction struct {
	obj dbus.BusObject
}

type options struct {
	isSharedConnection bool
}

// TransactionOptions is the function signature used to tweak the transaction.
type TransactionOptions func(*options)

// WithSharedConnection indicates that we're using a shared dbus connection.
func WithSharedConnection(isShared bool) func(o *options) {
	return func(o *options) {
		o.isSharedConnection = true
	}
}

const ifaceName = "com.ubuntu.authd.pam"
const objectPath = "/com/ubuntu/authd/pam"

// FIXME: dbus.Variant does not support maybe types, so we're using a variant string instead.
const variantNothing = "<@mv nothing>"

// NewTransaction creates a new [dbusmodule.Transaction] with the provided connection.
// A [pam.ModuleTransaction] implementation is returned together with a cleanup function that
// should be called to release the connection.
func NewTransaction(ctx context.Context, address string, o ...TransactionOptions) (tx pam.ModuleTransaction, cleanup func(), err error) {
	opts := options{}
	for _, f := range o {
		f(&opts)
	}

	log.Debugf(context.TODO(), "Connecting to %s", address)
	conn, err := dbus.Dial(address, dbus.WithContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { conn.Close() }
	if err = conn.Auth(nil); err != nil {
		cleanup()
		return nil, nil, err
	}
	if opts.isSharedConnection {
		if err = conn.Hello(); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	obj := conn.Object(ifaceName, objectPath)
	return &Transaction{obj: obj}, cleanup, nil
}

// BusObject gets the DBus object.
func (tx *Transaction) BusObject() dbus.BusObject {
	return tx.obj
}

// SetData allows to save any value in the module data that is preserved
// during the whole time the module is loaded.
func (tx *Transaction) SetData(key string, data any) error {
	if data == nil {
		return dbusUnsetter(tx.obj, "UnsetData", key)
	}
	return dbusSetter(tx.obj, "SetData", key, dbus.MakeVariant(data))
}

// GetData allows to get any value from the module data saved using SetData
// that is preserved across the whole time the module is loaded.
func (tx *Transaction) GetData(key string) (any, error) {
	// See the FIXME on variantNothing, all this should be managed by variant.
	data, err := dbusGetter[any](tx.obj, "GetData", key)
	if data == variantNothing {
		return nil, err
	}
	return data, err
}

// SetItem sets a PAM item.
func (tx *Transaction) SetItem(item pam.Item, value string) error {
	return dbusSetter(tx.obj, "SetItem", item, value)
}

// GetItem retrieves a PAM item.
func (tx *Transaction) GetItem(item pam.Item) (string, error) {
	return dbusGetter[string](tx.obj, "GetItem", item)
}

// PutEnv adds or changes the value of PAM environment variables.
//
// NAME=value will set a variable to a value.
// NAME= will set a variable to an empty value.
// NAME (without an "=") will delete a variable.
func (tx *Transaction) PutEnv(nameVal string) error {
	if !strings.Contains(nameVal, "=") {
		return dbusUnsetter(tx.obj, "UnsetEnv", nameVal)
	}
	envPair := strings.SplitN(nameVal, "=", 2)
	return dbusSetter(tx.obj, "SetEnv", envPair[0], envPair[1])
}

// GetEnv is used to retrieve a PAM environment variable.
func (tx *Transaction) GetEnv(name string) string {
	env, err := dbusGetter[string](tx.obj, "GetEnv", name)
	if err != nil {
		return ""
	}
	return env
}

// GetEnvList returns a copy of the PAM environment as a map.
func (tx *Transaction) GetEnvList() (map[string]string, error) {
	var r int
	var envMap map[string]string
	method := fmt.Sprintf("%s.GetEnvList", ifaceName)
	err := tx.obj.Call(method, dbus.FlagNoAutoStart).Store(&r, &envMap)
	if err != nil {
		log.Debugf(context.TODO(), "failed to call %s: %v", method, err)
		return nil, fmt.Errorf("%w: %w", pam.ErrSystem, err)
	}
	if r != 0 {
		log.Debugf(context.TODO(), "failed to call %s: %s", method, pam.Error(r))
		return nil, pam.Error(r)
	}
	return envMap, nil
}

// GetUser is similar to GetItem(User), but it would start a conversation if
// no user is currently set in PAM.
func (tx *Transaction) GetUser(prompt string) (string, error) {
	user, err := tx.GetItem(pam.User)
	if err != nil {
		return "", err
	}
	if user != "" {
		return user, nil
	}

	resp, err := tx.StartStringConv(pam.PromptEchoOn, prompt)
	if err != nil {
		return "", err
	}

	return resp.Response(), nil
}

// StartStringConv starts a text-based conversation using the provided style
// and prompt.
func (tx *Transaction) StartStringConv(style pam.Style, prompt string) (
	pam.StringConvResponse, error) {
	res, err := tx.StartConv(pam.NewStringConvRequest(style, prompt))
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
func (tx *Transaction) StartStringConvf(style pam.Style, format string, args ...interface{}) (
	pam.StringConvResponse, error) {
	return tx.StartStringConv(style, fmt.Sprintf(format, args...))
}

// StartBinaryConv starts a binary conversation using the provided bytes.
func (tx *Transaction) StartBinaryConv(bytes []byte) (
	pam.BinaryConvResponse, error) {
	return nil, fmt.Errorf("%w: binary conversations are not supported", pam.ErrConv)
}

// StartConv initiates a PAM conversation using the provided ConvRequest.
func (tx *Transaction) StartConv(req pam.ConvRequest) (
	pam.ConvResponse, error) {
	resp, err := tx.StartConvMulti([]pam.ConvRequest{req})
	if err != nil {
		return nil, err
	}
	if len(resp) != 1 {
		return nil, fmt.Errorf("%w: not enough values returned", pam.ErrConv)
	}
	return resp[0], nil
}

func (tx *Transaction) handleStringRequest(req pam.StringConvRequest) (pam.StringConvResponse, error) {
	if req.Style() == pam.BinaryPrompt {
		return nil, fmt.Errorf("%w: binary style is not supported", pam.ErrConv)
	}

	var r int
	var reply string
	method := fmt.Sprintf("%s.Prompt", ifaceName)
	err := tx.obj.Call(method, dbus.FlagNoAutoStart, req.Style(), req.Prompt()).Store(&r, &reply)
	if err != nil {
		log.Debugf(context.TODO(), "failed to call %s: %v", method, err)
		return nil, fmt.Errorf("%w: %w", pam.ErrSystem, err)
	}
	if r != 0 {
		log.Debugf(context.TODO(), "failed to call %s: %s", method, pam.Error(r))
		return nil, pam.Error(r)
	}
	return StringResponse{
		req.Style(),
		reply,
	}, nil
}

// StartConvMulti initiates a PAM conversation with multiple ConvRequest's.
func (tx *Transaction) StartConvMulti(requests []pam.ConvRequest) (
	responses []pam.ConvResponse, err error) {
	defer decorate.OnError(&err, "%v", err)

	if len(requests) == 0 {
		return nil, errors.New("no requests defined")
	}

	responses = make([]pam.ConvResponse, 0, len(requests))
	for _, req := range requests {
		switch r := req.(type) {
		case pam.StringConvRequest:
			response, err := tx.handleStringRequest(r)
			if err != nil {
				return nil, err
			}
			responses = append(responses, response)
		default:
			return nil, fmt.Errorf("unsupported conversation type %#v", r)
		}
	}

	return responses, nil
}

// InvokeHandler is called by the C code to invoke the proper handler.
func (tx *Transaction) InvokeHandler(handler pam.ModuleHandlerFunc,
	flags pam.Flags, args []string) error {
	return pam.ErrAbort
}

func dbusSetter[V any, K any](obj dbus.BusObject, method string, key K, value V) error {
	var r int
	method = fmt.Sprintf("%s.%s", ifaceName, method)
	err := obj.Call(method, dbus.FlagNoAutoStart, key, value).Store(&r)
	if err != nil {
		log.Debugf(context.TODO(), "failed to call %s: %v", method, err)
		return fmt.Errorf("%w: %w", pam.ErrSystem, err)
	}
	if r != 0 {
		log.Debugf(context.TODO(), "failed to call %s: %s", method, pam.Error(r))
		return pam.Error(r)
	}
	return nil
}

func dbusUnsetter[K any](obj dbus.BusObject, method string, key K) error {
	var r int
	method = fmt.Sprintf("%s.%s", ifaceName, method)
	err := obj.Call(method, dbus.FlagNoAutoStart, key).Store(&r)
	if err != nil {
		log.Debugf(context.TODO(), "failed to call %s: %v", method, err)
		return fmt.Errorf("%w: %w", pam.ErrSystem, err)
	}
	if r != 0 {
		log.Debugf(context.TODO(), "failed to call %s: %s", method, pam.Error(r))
		return pam.Error(r)
	}
	return nil
}

func dbusGetter[V any, K any](obj dbus.BusObject, method string, key K) (V, error) {
	var r int
	var v V
	method = fmt.Sprintf("%s.%s", ifaceName, method)
	err := obj.Call(method, 0, key).Store(&r, &v)
	if err != nil {
		log.Debugf(context.TODO(), "failed to call %s: %v", method, err)
		return v, fmt.Errorf("%w: %w", pam.ErrSystem, err)
	}
	if r != 0 {
		log.Debugf(context.TODO(), "failed to call %s: %s", method, pam.Error(r))
		return *new(V), pam.Error(r)
	}
	return v, nil
}

// StringResponse is a simple implementation of [pam.StringConvResponse].
type StringResponse struct {
	ConvStyle pam.Style
	Content   string
}

// Style returns the conversation style of the StringResponse.
func (s StringResponse) Style() pam.Style {
	return s.ConvStyle
}

// Response returns the string response of the StringResponse.
func (s StringResponse) Response() string {
	return s.Content
}
