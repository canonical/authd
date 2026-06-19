package dbusmodule_test

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
	"golang.org/x/exp/slices"
)

type methodCall struct {
	m    string
	args []any
}

type methodReturn struct {
	m      string
	values []any
}

type testServer struct {
	t             *testing.T
	mu            *sync.Mutex
	calledMethods []methodCall
	returns       []methodReturn
}

const testDBusErrorName = ifaceName + ".Error"

// SetEnv sets PAM environment variable.
func (ts *testServer) SetEnv(env string, value string) (int, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, env, value)

	ret, err := getSetterReturnValues(ts, methodName)
	if err != nil {
		return -1, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return ret, nil
}

// UnsetEnv sets a PAM environment variable.
func (ts *testServer) UnsetEnv(env string) (int, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, env)

	ret, err := getSetterReturnValues(ts, methodName)
	if err != nil {
		return -1, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return ret, nil
}

// GetEnv is used to retrieve a PAM environment variable.
func (ts *testServer) GetEnv(env string) (int, string, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, env)

	status, env, err := getGetterReturnValues[string](ts, methodName)
	if err != nil {
		return -1, "", dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return status, env, nil
}

// GetEnvList returns a copy of the PAM environment as a map.
func (ts *testServer) GetEnvList() (int, map[string]string, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName)

	status, envList, err := getGetterReturnValues[map[string]string](ts, methodName)
	if err != nil {
		return -1, nil, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return status, envList, nil
}

// SetItem sets a PAM item value.
func (ts *testServer) SetItem(item pam.Item, value string) (int, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, item, value)

	ret, err := getSetterReturnValues(ts, methodName)
	if err != nil {
		return -1, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return ret, nil
}

// GetItem gets a PAM item value.
func (ts *testServer) GetItem(item pam.Item) (int, string, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, item)

	status, value, err := getGetterReturnValues[string](ts, methodName)
	if err != nil {
		return -1, "", dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return status, value, nil
}

// SetData sets the PAM data.
func (ts *testServer) SetData(key string, value dbus.Variant) (int, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, key, value)

	ret, err := getSetterReturnValues(ts, methodName)
	if err != nil {
		return -1, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return ret, nil
}

// UnsetData unsets the PAM data.
func (ts *testServer) UnsetData(key string) (int, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, key)

	ret, err := getSetterReturnValues(ts, methodName)
	if err != nil {
		return -1, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return ret, nil
}

// SetData gets the PAM data.
func (ts *testServer) GetData(key string) (int, dbus.Variant, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, key)

	status, variant, err := getGetterReturnValues[dbus.Variant](ts, methodName)
	if err != nil {
		return -1, dbus.Variant{}, dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return status, variant, nil
}

// Prompt prompts for a PAM string conversation.
func (ts *testServer) Prompt(style pam.Style, prompt string) (int, string, *dbus.Error) {
	methodName := ts.getMethodName()
	ts.addCalledMethod(methodName, style, prompt)

	status, reply, err := getGetterReturnValues[string](ts, methodName)
	if err != nil {
		return -1, "", dbus.NewError(testDBusErrorName, []any{err.Error()})
	}
	return status, reply, nil
}

func (ts *testServer) getMethodName() string {
	pc := make([]uintptr, 2)
	runtime.Callers(2, pc)
	splits := strings.Split(runtime.FuncForPC(pc[0]).Name(), ".")
	return splits[len(splits)-1]
}

func (ts *testServer) addCalledMethod(method string, args ...any) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.calledMethods = append(ts.calledMethods, methodCall{method, args})
}

func (ts *testServer) getCalledMethods() []methodCall {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	return slices.Clone(ts.calledMethods)
}

func (ts *testServer) getReturnValues(method string, expectedNumber int) ([]any, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.returns) < 1 {
		return nil, fmt.Errorf("no return values found while calling %s", method)
	}
	idx := slices.IndexFunc(ts.returns, func(r methodReturn) bool { return r.m == method })
	if idx < 0 {
		return nil, fmt.Errorf("no return values defined for method %s", method)
	}
	if expectedNumber == 0 || len(ts.returns[idx].values) != expectedNumber {
		return nil, fmt.Errorf("not enough return values for method %s: found %v", method, ts.returns[idx].values)
	}

	values := ts.returns[idx].values
	ts.returns = slices.Delete(ts.returns, idx, 1)
	return values, nil
}

func getSetterReturnValues(ts *testServer, method string) (int, error) {
	retValues, err := ts.getReturnValues(method, 1)
	if err != nil {
		return -1, err
	}
	return getStatusValue(retValues[0])
}

func getGetterReturnValues[T any](ts *testServer, method string) (int, T, error) {
	retValues, err := ts.getReturnValues(method, 2)
	if err != nil {
		return -1, *new(T), err
	}
	s, err := getStatusValue(retValues[0])
	if err != nil {
		return -1, *new(T), err
	}
	v, ok := retValues[1].(T)
	if !ok {
		return -1, *new(T), fmt.Errorf("%#v is not a %T", retValues[1], *new(T))
	}
	return s, v, nil
}

func getStatusValue(v any) (int, error) {
	switch rv := v.(type) {
	case int:
		return rv, nil
	case pam.Error:
		return int(rv), nil
	case pam.Item:
		return int(rv), nil
	default:
		return -1, fmt.Errorf("%#v is not an integer", v)
	}
}
