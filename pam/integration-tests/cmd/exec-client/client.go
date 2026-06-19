// TiCS: disabled // This is a test helper.

//go:build pam_tests_exec_client

// Package main is the package for the exec test client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/dbusmodule"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
)

var (
	pamFlags      = flag.Int64("flags", 0, "pam flags")
	serverAddress = flag.String("server-address", "", "the dbus connection address to use to communicate with module")
	logFile       = flag.String("client-log", "", "the file where to save logs")
	argsFile      = flag.String("client-args-file", "", "the file where arguments are saved")
)

func main() {
	err := mainFunc()
	if err == nil {
		log.Info(context.TODO(), "Exiting with success")
		os.Exit(0)
	}

	log.Errorf(context.TODO(), "Exiting with error: %v", err)
	var pamError pam.Error
	if !errors.As(err, &pamError) {
		os.Exit(255)
	}
	os.Exit(int(pamError))
}

func mainFunc() error {
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		return fmt.Errorf("%w: not enough arguments", pam_test.ErrInvalid)
	}

	serverAddressEnv := os.Getenv("AUTHD_PAM_SERVER_ADDRESS")
	if serverAddressEnv != "" {
		*serverAddress = serverAddressEnv
	}

	if serverAddress == nil {
		return fmt.Errorf("%w: no connection provided", pam_test.ErrInvalid)
	}

	mTx, closeFunc, err := newModuleWrapper(*serverAddress)
	if err != nil {
		return fmt.Errorf("%w: can't connect to server: %w", pam_test.ErrInvalid, err)
	}
	defer closeFunc()

	action, args := args[0], args[1:]

	// We parse again the remaining arguments again
	flag.CommandLine.Parse(args)
	args = flag.CommandLine.Args()

	log.SetLevel(log.DebugLevel)
	if logFile != nil && *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		log.SetOutput(f)
	}

	if argsFile != nil && *argsFile != "" {
		log.Debug(context.TODO(), "Reading arguments from %s", *argsFile)
		ca, err := os.ReadFile(*argsFile)
		if err != nil {
			return err
		}
		args = append(args, strings.Split(string(ca), "\t")...)
	}

	actionFlags := pam.Flags(0)
	if pamFlags != nil {
		actionFlags = pam.Flags(*pamFlags)
	}

	if err := mTx.SetData("exec-client-flags-"+actionToServiceType(action), actionFlags); err != nil {
		return err
	}

	switch action {
	case "authenticate":
		return handleArgs(mTx, args)
	case "acct_mgmt":
		return handleArgs(mTx, args)
	case "open_session":
		return handleArgs(mTx, args)
	case "close_session":
		return handleArgs(mTx, args)
	case "chauthtok":
		return handleArgs(mTx, args)
	case "setcred":
		return handleArgs(mTx, args)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
}

func actionToServiceType(action string) string {
	switch action {
	case "open_session":
		return "session"
	case "close_session":
		return "session"
	case "chauthtok":
		return "password"
	case "setcred":
		return "password"
	default:
		return action
	}
}

func handleArgs(mTx pam.ModuleTransaction, args []string) error {
	for _, arg := range args {
		err := handleArg(mTx, arg)
		if err != nil {
			return err
		}
	}
	return nil
}

func handleArg(mTx pam.ModuleTransaction, arg string) error {
	// We support handing operations with variant values such as:
	//   {"act": <"$action">, "args": <[$input...]>, "exp": <[$expected...]>}
	// Where:
	//  - $action is the method to call
	//  - $input is a list of variant arguments
	//  - $expected is an optional list of expected variant return values
	// For example:
	//   {"act": <"SetItem">, "args": <[<2>, <"user">]>}
	// or:
	//   {"act": <"SetItem">, "args": <[<-1>, <"foo">]>, "exp": <[<29>]>}

	log.Debugf(context.TODO(), "Parsing argument '%v'", arg)

	var parsedArg map[string]dbus.Variant
	variant, err := dbus.ParseVariant(arg, dbus.SignatureOf(parsedArg))
	if err != nil {
		return fmt.Errorf("can't parse %s as variant: %w, %w", arg, err, pam_test.ErrInvalidArguments)
	}

	if err := variant.Store(&parsedArg); err != nil {
		return fmt.Errorf("can't store %s as variant map: %w, %w", arg, err, pam_test.ErrInvalidArguments)
	}

	action, err := getVariantMapItem[string](parsedArg, "act")
	if err != nil {
		return fmt.Errorf("can't parse action: %v: %w", err, pam_test.ErrInvalidMethod)
	}
	if action == "" {
		return fmt.Errorf("no action found: %w", pam_test.ErrInvalidMethod)
	}

	method := reflect.ValueOf(mTx).MethodByName(action)
	if method == (reflect.Value{}) {
		return fmt.Errorf("no method %s found: %w", action, pam_test.ErrInvalidMethod)
	}

	inputArgs, err := getVariantMapItem[[]dbus.Variant](parsedArg, "args")
	if err != nil {
		return fmt.Errorf("can't parse arguments: %w", err)
	}
	if inputArgs == nil {
		return fmt.Errorf("can't find arguments: %w", pam_test.ErrInvalidArguments)
	}

	expectedRet, err := getVariantMapItem[[]dbus.Variant](parsedArg, "exp")
	if err != nil {
		return fmt.Errorf("can't parse expected return values: %w", err)
	}

	callArgs, err := getCallArgs(action, method, inputArgs)
	if err != nil {
		return err
	}

	retValues := method.Call(callArgs)
	if expectedRet == nil {
		// If return value is not explicitly handled, we just return the error if we got one
		for _, ret := range retValues {
			if !ret.CanInterface() {
				continue
			}
			iface := ret.Interface()
			switch value := iface.(type) {
			case error:
				return value
			default:
				log.Debugf(context.TODO(), "Ignoring %s returned value %#v", action, ret)
			}
		}
		return nil
	}

	return checkReturnedValues(action, method, expectedRet, retValues)
}

func getVariantMapItem[T any](parsedArg map[string]dbus.Variant, key string) (T, error) {
	var variantValue T
	args, ok := parsedArg[key]
	if !ok {
		return *new(T), nil
	}
	err := args.Store(&variantValue)
	if err != nil {
		return *new(T), pam_test.ErrInvalidArguments
	}

	return variantValue, nil
}

func tryConvertTyped[T any](arg T, expected reflect.Type) (reflect.Value, error) {
	argValue := reflect.ValueOf(arg)
	argValueType := argValue.Type()
	if !argValueType.ConvertibleTo(expected) {
		return reflect.Value{}, fmt.Errorf("cannot convert %s to %s", argValueType, expected)
	}

	return argValue.Convert(expected), nil
}

func tryConvertVariant(variant dbus.Variant, expected reflect.Type) (reflect.Value, error) {
	if expected.ConvertibleTo(reflect.TypeFor[error]()) {
		pamError, err := tryExtractVariant(variant, reflect.TypeFor[pam.Error]())
		if err != nil {
			return tryConvertTyped(errors.New(variant.String()), expected)
		}
		if pamError.IsZero() {
			return reflect.Zero(expected), nil
		}
		return pamError, nil
	}

	if expected.ConvertibleTo(reflect.TypeFor[pam.StringConvResponse]()) {
		variantMapValue, err := tryExtractVariant(variant, reflect.TypeFor[map[string]dbus.Variant]())
		if err != nil {
			return reflect.Value{}, err
		}
		if variantMapValue.IsZero() {
			return reflect.Zero(expected), nil
		}

		variantMap := variantMapValue.Interface().(map[string]dbus.Variant)
		style, err := getVariantMapItem[pam.Style](variantMap, "style")
		if err != nil {
			return reflect.Value{}, err
		}
		reply, err := getVariantMapItem[string](variantMap, "reply")
		if err != nil {
			return reflect.Value{}, err
		}

		return reflect.ValueOf(dbusmodule.StringResponse{style, reply}), nil
	}

	return tryExtractVariant(variant, expected)
}

func isVariantNothing(variant dbus.Variant) bool {
	strValue, ok := variant.Value().(string)
	if !ok {
		return false
	}

	return isVariantNothingString(strValue)
}

func isVariantNothingString(arg string) bool {
	ok, _ := regexp.MatchString("<@m[vbynqiuxthd] nothing>", arg)
	return ok
}

func tryExtractVariant(variant dbus.Variant, expected reflect.Type) (reflect.Value, error) {
	if isVariantNothing(variant) {
		return reflect.Zero(expected), nil
	}
	return tryConvertTyped(variant.Value(), expected)
}

func getCallArgs(action string, method reflect.Value, args []dbus.Variant) ([]reflect.Value, error) {
	methodType := method.Type()
	if len(args) != methodType.NumIn() && !methodType.IsVariadic() {
		return nil, fmt.Errorf("method %s %s needs %d arguments (%d provided): %w",
			action, methodType, methodType.NumIn(), len(args), pam_test.ErrInvalidArguments)
	}

	var callArgs []reflect.Value
	for idx, arg := range args {
		var inType reflect.Type
		if !methodType.IsVariadic() || idx < methodType.NumIn()-1 {
			inType = methodType.In(idx)
		} else {
			// We're handling a variadic type as per the check above.
			inType = methodType.In(methodType.NumIn() - 1).Elem()
		}
		value, err := tryConvertVariant(arg, inType)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", err, pam_test.ErrArgumentTypeMismatch)
		}

		callArgs = append(callArgs, value)
	}

	return callArgs, nil
}

func checkReturnedValues(action string, method reflect.Value, wantArgs []dbus.Variant, retValues []reflect.Value) error {
	methodType := method.Type()
	if len(wantArgs) != methodType.NumOut() || len(wantArgs) != len(retValues) {
		return fmt.Errorf("method %s %s returns %d arguments (%d provided): %w",
			action, methodType, methodType.NumOut(), len(wantArgs), pam_test.ErrReturnMismatch)
	}

	for idx, wantArg := range wantArgs {
		retValue := retValues[idx]
		log.Debugf(context.TODO(), "Checking %s returned value %#v", action, retValue.Interface())

		wantValue, err := tryConvertVariant(wantArg, methodType.Out(idx))
		if err != nil {
			return fmt.Errorf("%w: %w", err, pam_test.ErrReturnMismatch)
		}

		if reflect.DeepEqual(retValue.Interface(), wantValue.Interface()) {
			continue
		}
		if tryCompareValues(wantValue.Interface(), retValue.Interface()) {
			continue
		}

		return fmt.Errorf("values do not match: expected '%#v', got '%#v': %w",
			wantValue.Interface(), retValue.Interface(), pam_test.ErrReturnMismatch)
	}

	return nil
}

func tryCompareValues(wantValue any, actualValue any) bool {
	switch w := wantValue.(type) {
	case error:
		if err, ok := actualValue.(error); ok {
			return errors.Is(err, w)
		}
	case nil:
		if string, ok := actualValue.(string); ok {
			return isVariantNothingString(string)
		}
	}

	return false
}
