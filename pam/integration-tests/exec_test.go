package main_test

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

var execModuleSources = []string{"./pam/go-exec/module.c"}

const execServiceName = "exec-module"

func TestExecModule(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	if !pam.CheckPamHasStartConfdir() {
		t.Fatal("can't test with this libpam version!")
	}

	libPath := buildExecModuleWithCFlags(t, []string{"-DAUTHD_TEST_EXEC_MODULE"}, false)
	execClient := buildExecClient(t)

	// We do multiple tests inside this test function not to have to re-compile
	// the library and to ensure that we don't have to care about merging its coverage.

	// These are the module initialization tests.
	moduleInitTests := map[string]struct {
		moduleArgs []string
		wantError  error
	}{
		// Error cases
		"Error_on_no_arguments": {
			wantError: pam.ErrModuleUnknown,
		},
		"Error_on_empty_executable_parameter": {
			moduleArgs: []string{""},
			wantError:  pam.ErrModuleUnknown,
		},
		"Error_on_non_existent_executable_parameter": {
			moduleArgs: []string{"/non-existent/file"},
			wantError:  pam.ErrModuleUnknown,
		},
		"Error_on_non_executable_parameter": {
			moduleArgs: execModuleSources,
			wantError:  pam.ErrModuleUnknown,
		},
		"Error_on_not_runnable_parameter": {
			moduleArgs: []string{filepath.Join(testutils.ProjectRoot())},
			wantError:  pam.ErrSystem,
		},
	}
	for name, tc := range moduleInitTests {
		t.Run("ModuleInit "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			tx := preparePamTransaction(t, libPath, "", tc.moduleArgs, "")
			require.ErrorIs(t, tx.Authenticate(0), tc.wantError)
		})
	}

	// The tests below are based on the ones of the pam_test.ModuleTransactionDummy
	// but we're using the exec commands to ensure that everything works as expected.
	// We don't split the tests in different functions not to to have to regenerate the
	// same library for each test and to ensure that the C coverage is properly computed.

	// These tests are meant to check the exec client behavior itself.
	cliTests := map[string]struct {
		methodCalls   []cliMethodCall
		rawModuleArgs []string
		wantError     error
	}{
		"SetGet_Item": {
			methodCalls: []cliMethodCall{
				{m: "SetItem", args: []any{pam.Rhost, "some-rhost-value"}, r: []any{nil}},
				{m: "GetItem", args: []any{pam.Rhost}, r: []any{"some-rhost-value", nil}},
			},
		},
		"SetGet_Item_handling_errors": {
			methodCalls: []cliMethodCall{
				{m: "SetItem", args: []any{pam.Item(-1), "some-value"}, r: []any{pam.ErrBadItem}},
				{m: "GetItem", args: []any{pam.Item(-1)}, r: []any{"", pam.ErrBadItem}},
			},
		},
		"SetGet_Env": {
			methodCalls: []cliMethodCall{
				{m: "PutEnv", args: []any{"FooEnv=bar"}, r: []any{nil}},
				{m: "GetEnv", args: []any{"FooEnv"}, r: []any{"bar"}},
				{m: "GetEnv", args: []any{"AnotherEnv"}},

				{m: "PutEnv", args: []any{"Bar=foo"}, r: []any{pam.Error(0)}},

				{m: "PutEnv", args: []any{"FooEnv="}},
				{m: "GetEnv", args: []any{"FooEnv"}},

				{m: "PutEnv", args: []any{"FooEnv"}},
				{m: "GetEnv", args: []any{"FooEnv"}},
			},
		},
		"SetGet_Data": {
			methodCalls: []cliMethodCall{
				{m: "SetData", args: []any{"FooData", "bar"}, r: []any{nil}},
				{m: "GetData", args: []any{"FooData"}, r: []any{"bar", nil}},

				{m: "GetData", args: []any{"AnotherData"}, r: []any{nil, pam.ErrNoModuleData}},

				{m: "SetData", args: []any{"FooData", []int{1, 2, 3}}},
				{m: "GetData", args: []any{"FooData"}, r: []any{[]int{1, 2, 3}, nil}},

				{m: "SetData", args: []any{"FooData", nil}},
				{m: "GetData", args: []any{"FooData"}, r: []any{nil, nil}},
			},
		},
		"GetEnvList_empty": {
			methodCalls: []cliMethodCall{
				{m: "GetEnvList", r: []any{map[string]string{}, nil}},
			},
		},
		"GetEnvList_populated": {
			methodCalls: []cliMethodCall{
				{m: "PutEnv", args: []any{"Env=value"}},
				{m: "PutEnv", args: []any{"Env2=value2"}},
				{m: "GetEnvList", r: []any{
					map[string]string{
						"Env":  "value",
						"Env2": "value2",
					},
					nil,
				}},
			},
		},

		// Error cases
		"Error_providing_invalid_variant_argument": {
			rawModuleArgs: []string{"$not_A-variant Action"},
			wantError:     pam_test.ErrInvalidArguments,
		},
		"Error_providing_no_action": {
			rawModuleArgs: []string{dbus.MakeVariant(map[string]dbus.Variant{}).String()},
			wantError:     pam_test.ErrInvalidMethod,
		},
		"Error_providing_invalid_action_type": {
			rawModuleArgs: []string{dbus.MakeVariant(
				map[string]dbus.Variant{"act": dbus.MakeVariant([]int{1, 2, 3})},
			).String()},
			wantError: pam_test.ErrInvalidMethod,
		},
		"Error_when_not_providing_arguments": {
			rawModuleArgs: []string{dbus.MakeVariant(
				map[string]dbus.Variant{"act": dbus.MakeVariant("SetItem")},
			).String()},
			wantError: pam_test.ErrInvalidArguments,
		},
		"Error_when_providing_no_arguments": {
			rawModuleArgs: []string{dbus.MakeVariant(
				map[string]dbus.Variant{
					"act":  dbus.MakeVariant("SetItem"),
					"args": dbus.MakeVariant([]dbus.Variant{}),
				},
			).String()},
			wantError: pam_test.ErrInvalidArguments,
		},
		"Error_providing_invalid_arguments_type": {
			rawModuleArgs: []string{dbus.MakeVariant(
				map[string]dbus.Variant{
					"act":  dbus.MakeVariant("GetItem"),
					"args": dbus.MakeVariant("not enough"),
				},
			).String()},
			wantError: pam_test.ErrInvalidArguments,
		},
		"Error_when_providing_empty_arguments": {
			methodCalls: []cliMethodCall{{m: "SetItem", args: []any{}}},
			wantError:   pam_test.ErrInvalidArguments,
		},
		"Error_when_not_providing_enough_arguments": {
			methodCalls: []cliMethodCall{{m: "SetItem", args: []any{pam.User}}},
			wantError:   pam_test.ErrInvalidArguments,
		},
		"Error_when_providing_empty_return_values": {
			methodCalls: []cliMethodCall{{m: "SetItem", args: []any{pam.User, "an-user"}, r: []any{}}},
			wantError:   pam_test.ErrReturnMismatch,
		},
		"Error_when_not_providing_enough_return_values": {
			methodCalls: []cliMethodCall{{m: "GetItem", args: []any{pam.User}, r: []any{}}},
			wantError:   pam_test.ErrReturnMismatch,
		},
		"Error_when_calling_unknown_method": {
			methodCalls: []cliMethodCall{{m: "ThisMethodDoesNotExist"}},
			wantError:   pam_test.ErrInvalidMethod,
		},
		"Error_when_calling_unknown_dbus_method": {
			methodCalls: []cliMethodCall{{m: "CallUnhandledMethod"}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_argument_types_do_not_match_arguments": {
			methodCalls: []cliMethodCall{{m: "SetItem", args: []any{"an-item", "value"}}},
			wantError:   pam_test.ErrArgumentTypeMismatch,
		},
		"Error_when_return_values_types_do_not_match_expected": {
			methodCalls: []cliMethodCall{
				{m: "GetItem", args: []any{pam.Item(-1)}, r: []any{"", "should have been an error"}},
			},
			wantError: pam_test.ErrReturnMismatch,
		},
		"Error_when_trying_to_compare_an_unexpected_variant_value": {
			methodCalls: []cliMethodCall{{m: "GetEnvList", r: []any{"", nil}}},
			wantError:   pam_test.ErrReturnMismatch,
		},
		"Error_when_trying_to_compare_a_not-matching_variant_value": {
			methodCalls: []cliMethodCall{{m: "GetEnvList", r: []any{"string", nil}}},
			wantError:   pam_test.ErrReturnMismatch,
		},
		"Error_when_getting_not-available_user_data": {
			methodCalls: []cliMethodCall{{m: "GetData", args: []any{"NotAvailable"}}},
			wantError:   pam.ErrNoModuleData,
		},
		"Error_when_client_fails_panicking": {
			methodCalls: []cliMethodCall{{m: "SimulateClientPanic", args: []any{"Client panicked! (As expected)"}}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_client_fails_because_an_unhandled_error": {
			methodCalls: []cliMethodCall{{m: "SimulateClientError", args: []any{"Client error!"}}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_client_fails_because_a_client_SIGTERM_signal": {
			methodCalls: []cliMethodCall{{m: "SimulateClientSignal", args: []any{syscall.SIGTERM, true}}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_client_fails_because_a_client_SIGKILL_signal": {
			methodCalls: []cliMethodCall{{m: "SimulateClientSignal", args: []any{syscall.SIGKILL, true}}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_client_fails_because_a_client_SIGSEGV_signal": {
			methodCalls: []cliMethodCall{{m: "SimulateClientSignal", args: []any{syscall.SIGSEGV, true}}},
			wantError:   pam.ErrSystem,
		},
		"Error_when_client_fails_because_a_client_SIGABRT_signal": {
			methodCalls: []cliMethodCall{{m: "SimulateClientSignal", args: []any{syscall.SIGABRT, true}}},
			wantError:   pam.ErrSystem,
		},
	}
	for name, tc := range cliTests {
		t.Run("Client "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			if len(tc.rawModuleArgs) == 0 {
				tc.rawModuleArgs = methodCallsAsArgs(tc.methodCalls)
			}
			tx := preparePamTransaction(t, libPath, execClient, tc.rawModuleArgs, "")
			require.ErrorIs(t, tx.Authenticate(0), tc.wantError)
		})
	}

	// These tests are meant to check the exec client pam flags.
	actionFlags := map[string]struct {
		flags pam.Flags
	}{
		"No_flags_set":                    {},
		"Silent_flag_set":                 {flags: pam.Silent},
		"Silent_and_RefreshCred_flag_set": {flags: pam.Silent | pam.RefreshCred},
	}
	for name, tc := range actionFlags {
		t.Run("Flags "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			tx := preparePamTransactionWithActionArgs(t, libPath, execClient, actionArgsMap{
				pam_test.Auth: methodCallsAsArgs([]cliMethodCall{
					{"GetData", []any{"exec-client-flags-authenticate"}, []any{tc.flags, nil}},
				}),
				pam_test.Account: methodCallsAsArgs([]cliMethodCall{
					{"GetData", []any{"exec-client-flags-authenticate"}, []any{tc.flags, nil}},
				}),
				pam_test.Session: methodCallsAsArgs([]cliMethodCall{
					{"GetData", []any{"exec-client-flags-session"}, []any{tc.flags, nil}},
				}),
				// We can't fully test this since PAM adds some flags we don't control to SetCred and ChangeAuthTok
				pam_test.Password: methodCallsAsArgs([]cliMethodCall{}),
			}, "")

			performAllPAMActions(t, tx, tc.flags, nil)
		})
	}

	// These tests are meant to check the exec client arguments.
	actionArgs := map[string]struct {
		moduleArgs []string
	}{
		"Do_not_deadlock_if_invalid_log_path_is_provided": {
			[]string{"--exec-log", "/not-existent/file-path.log"},
		},
	}
	for name, tc := range actionArgs {
		t.Run("ActionArgs "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			moduleArgs := append(getModuleArgs(t, "", nil), tc.moduleArgs...)
			moduleArgs = append(moduleArgs, "--", execClient)
			serviceFile := createServiceFile(t, execServiceName, libPath, moduleArgs)
			tx := preparePamTransactionForServiceFile(t, serviceFile, "", nil)
			performAllPAMActions(t, tx, pam.Flags(0), nil)
		})
	}

	// These tests are checking Get/Set item and ensuring those values are matching
	// both inside the client and in the calling application.
	itemsTests := map[string]struct {
		item  pam.Item
		value *string
		user  string

		wantValue    *string
		wantGetError error
		wantSetError error
	}{
		"Set_user": {
			item:  pam.User,
			value: ptrValue("a user"),
		},
		"Returns_empty_when_getting_an_unset_user": {
			item:      pam.User,
			wantValue: ptrValue(""),
		},
		"Returns_the_user_when_getting_a_preset_user": {
			item:      pam.User,
			user:      "preset PAM user",
			wantValue: ptrValue("preset PAM user"),
		},
		"Setting_and_getting_an_user": {
			item:      pam.User,
			value:     ptrValue("the-user"),
			wantValue: ptrValue("the-user"),
		},
		"Getting_the_preset_service_name": {
			item:      pam.Service,
			wantValue: ptrValue(execServiceName),
		},

		// Error cases
		"Error_when_setting_invalid_item": {
			item:         pam.Item(-1),
			value:        ptrValue("some value"),
			wantSetError: pam.ErrBadItem,
		},
		"Error_when_getting_invalid_item": {
			item:         pam.Item(-1),
			wantGetError: pam.ErrBadItem,
			wantValue:    ptrValue(""),
		},
	}
	for name, tc := range itemsTests {
		t.Run("Item "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			var methodCalls []cliMethodCall
			var wantExitError error

			if tc.value != nil {
				methodCalls = append(methodCalls,
					cliMethodCall{m: "SetItem", args: []any{tc.item, *tc.value}})
				wantExitError = tc.wantSetError
			}

			if tc.wantValue != nil {
				methodCalls = append(methodCalls,
					cliMethodCall{m: "GetItem", args: []any{tc.item}})
				wantExitError = tc.wantGetError
			}

			tx := preparePamTransaction(t, libPath, execClient, methodCallsAsArgs(methodCalls), tc.user)
			performAllPAMActions(t, tx, 0, wantExitError)

			if tc.value != nil && tc.wantSetError == nil {
				value, err := tx.GetItem(tc.item)
				require.Equal(t, *tc.value, value, "Item %v value mismatch", tc.item)
				require.NoError(t, err, "Can't get a PAM item %v", tc.item)
			}

			if tc.wantValue != nil && tc.wantGetError == nil {
				value, err := tx.GetItem(tc.item)
				require.Equal(t, *tc.wantValue, value, "Item %v value mismatch", tc.item)
				require.NoError(t, err, "Can't get a PAM item %v", tc.item)
			}
		})
	}

	// These tests are checking that setting and unsetting env variables works
	// both inside the executed module and the caller one.
	envTests := map[string]struct {
		env          string
		value        *string
		presetValues map[string]string
		skipPut      bool

		wantValue    *string
		wantPutError error
	}{
		"Put_var": {
			env:   "AN_ENV",
			value: ptrValue("value"),
		},
		"Unset_a_not-previously_set_value": {
			env:          "NEVER_SET_ENV",
			wantPutError: pam.ErrBadItem,
			wantValue:    ptrValue(""),
		},
		"Unset_a_preset_value": {
			presetValues: map[string]string{"PRESET_ENV": "hey!"},
			env:          "PRESET_ENV",
			wantValue:    ptrValue(""),
		},
		"Changes_a_preset_var": {
			presetValues: map[string]string{"PRESET_ENV": "hey!"},
			env:          "PRESET_ENV",
			value:        ptrValue("hello!"),
			wantValue:    ptrValue("hello!"),
		},
		"Get_an_unset_env": {
			skipPut:   true,
			env:       "AN_UNSET_ENV",
			wantValue: ptrValue(""),
		},
		"Gets_an_invalid_env_name": {
			env:       "",
			value:     ptrValue("Invalid Value"),
			wantValue: ptrValue(""),
			skipPut:   true,
		},

		// Error cases
		"Error_when_putting_an_invalid_env_name": {
			env:          "",
			value:        ptrValue("Invalid Value"),
			wantPutError: pam.ErrBadItem,
		},
	}
	for name, tc := range envTests {
		t.Run("Env "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			wantEnvList := map[string]string{}

			methodCalls := []cliMethodCall{
				{m: "GetEnvList", r: []any{map[string]string{}, nil}},
			}

			if tc.presetValues != nil && !tc.skipPut {
				for env, value := range tc.presetValues {
					methodCalls = append(methodCalls, cliMethodCall{
						m: "PutEnv", args: []any{fmt.Sprintf("%s=%s", env, value)}, r: []any{nil},
					})
				}
				wantEnvList = maps.Clone(tc.presetValues)
				methodCalls = append(methodCalls, cliMethodCall{
					m: "GetEnvList", r: []any{maps.Clone(wantEnvList), nil},
				})
			}

			if !tc.skipPut {
				var env string
				if tc.value != nil {
					env = tc.env + "=" + *tc.value
				} else {
					env = tc.env
				}
				methodCalls = append(methodCalls, cliMethodCall{
					m: "PutEnv", args: []any{env}, r: []any{tc.wantPutError},
				})

				if tc.wantPutError == nil {
					if tc.value != nil {
						wantEnvList[tc.env] = *tc.value
					}
					if tc.value != nil && tc.wantValue != nil {
						wantEnvList[tc.env] = *tc.wantValue
					}
					if tc.value == nil {
						delete(wantEnvList, tc.env)
					}
				}
				methodCalls = append(methodCalls, cliMethodCall{
					m: "GetEnvList", r: []any{maps.Clone(wantEnvList), nil},
				})
			}

			if tc.wantValue != nil {
				methodCalls = append(methodCalls, cliMethodCall{
					m: "GetEnv", args: []any{tc.env}, r: []any{*tc.wantValue},
				})
			}

			tx := preparePamTransactionWithActionArgs(t, libPath, execClient, actionArgsMap{
				pam_test.Auth: methodCallsAsArgs(methodCalls),
				pam_test.Account: methodCallsAsArgs([]cliMethodCall{
					{m: "GetEnvList", r: []any{maps.Clone(wantEnvList), nil}},
				}),
			}, "")
			envList, err := tx.GetEnvList()
			require.NoError(t, err, "Setup: GetEnvList should not return an error")
			require.Len(t, envList, 0, "Setup: GetEnvList should have elements")

			require.NoError(t, tx.Authenticate(0), "Calling AcctMgmt should not error")
			require.NoError(t, tx.AcctMgmt(0), "Calling AcctMgmt should not error")

			gotEnv, err := tx.GetEnvList()
			require.NoError(t, err, "tx.GetEnvList should not return an error")
			require.Equal(t, wantEnvList, gotEnv, "returned env lits should match expected")
		})
	}

	// These tests are ensuring that data values can be set on a module and fetched during
	// various stages.
	dataTests := map[string]struct {
		key        string
		data       any
		presetData map[string]any
		skipSet    bool
		skipGet    bool

		wantData     any
		wantSetError error
		wantGetError error
	}{
		"Sets_and_gets_data": {
			presetData: map[string]any{"some-data": []string{"hey! That's", "true"}},
			key:        "data",
			data:       []string{"hey! That's", "true"},
			wantData:   []string{"hey! That's", "true"},
		},
		"Gets_previously_set_data": {
			presetData: map[string]any{"some-old-data": []int{3, 2, 1}},
			key:        "some-old-data",
			skipSet:    true,
			wantData:   []int{3, 2, 1},
		},
		"Data_can_be_nil": {
			// This is actually a libpam issue, but we should respect that for now
			// See: https://github.com/linux-pam/linux-pam/pull/780
			data:     nil,
			key:      "nil-data",
			wantData: nil,
		},
		"Set_replaces_data": {
			presetData: map[string]any{"some-data": []string{"hey! That's", "true"}},
			key:        "some-data",
			data: []map[string]string{
				{"hey": "yay"},
				{"foo": "bar"},
			},
			wantData: []map[string]string{
				{"hey": "yay"},
				{"foo": "bar"},
			},
		},
		"No_error_when_getting_data_that_has_been_removed": {
			presetData: map[string]any{"some-data": []string{"hey! That's", "true"}},
			key:        "some-data",
			data:       nil,
			wantData:   nil,
		},

		// Error cases
		"Error_when_getting_data_that_has_never_been_set": {
			skipSet:      true,
			key:          "not set",
			wantGetError: pam.ErrNoModuleData,
		},
	}
	for name, tc := range dataTests {
		t.Run("Data "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			var presetMethodCalls []cliMethodCall
			var methodCalls []cliMethodCall
			var postMethodCalls []cliMethodCall

			if tc.presetData != nil {
				for key, value := range tc.presetData {
					presetMethodCalls = append(methodCalls, cliMethodCall{
						m: "SetData", args: []any{key, value},
					})
				}
			}

			if !tc.skipSet {
				methodCalls = append(methodCalls, cliMethodCall{
					"SetData", []any{tc.key, tc.data}, []any{tc.wantSetError},
				})
			}

			if !tc.skipGet {
				mc := cliMethodCall{
					"GetData", []any{tc.key}, []any{tc.wantData, tc.wantGetError},
				}
				methodCalls = append(methodCalls, mc)
				postMethodCalls = append(methodCalls, mc)
			}

			tx := preparePamTransactionWithActionArgs(t, libPath, execClient, actionArgsMap{
				pam_test.Auth:     methodCallsAsArgs(presetMethodCalls),
				pam_test.Account:  methodCallsAsArgs(methodCalls),
				pam_test.Password: methodCallsAsArgs(postMethodCalls),
			}, "")
			require.NoError(t, tx.Authenticate(0))
			require.NoError(t, tx.AcctMgmt(0))
			require.NoError(t, tx.ChangeAuthTok(0))
		})
	}

	// These tests are checking that string conversations are working as expected.
	stringConvTests := map[string]struct {
		prompt                string
		promptFormat          string
		promptFormatArgs      []interface{}
		convStyle             pam.Style
		convError             error
		convHandler           *pam.ConversationFunc
		convShouldNotBeCalled bool

		want           string
		stringResponse any
		wantError      error
		wantExitError  error
	}{
		"Messages_with_info_style_are_handled_by_conversation": {
			prompt:    "This is an info message!",
			convStyle: pam.TextInfo,
		},
		"Messages_with_error_style_are_handled_by_conversation": {
			prompt:    "This is an error message!",
			convStyle: pam.ErrorMsg,
		},
		"Messages_with_echo_on_style_are_handled_by_conversation": {
			prompt:    "This is an echo on message!",
			convStyle: pam.PromptEchoOn,
			want:      "I'm handling it perfectly!",
		},
		"Conversation_prompt_can_be_formatted": {
			promptFormat:     "Sending some %s, right? %v - But that's %v or %d?",
			promptFormatArgs: []interface{}{"info", true, nil, 123},
			convStyle:        pam.PromptEchoOff,
			want:             "And returning some text back",
		},

		// Error cases
		"Error_if_no_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationFunc(nil)),
			wantError:   pam.ErrConv,
		},
		"Error_if_the_conversation_handler_fails": {
			prompt:    "Tell me your secret!",
			convStyle: pam.PromptEchoOff,
			convError: pam.ErrBuf,
			wantError: pam.ErrConv,
		},
		"Error_when_conversation_uses_binary_content_style": {
			prompt:                "I am a binary content\xff!",
			convStyle:             pam.BinaryPrompt,
			convError:             pam.ErrConv,
			wantError:             pam.ErrConv,
			convShouldNotBeCalled: true,
		},
		"Error_when_when_parsing_returned_response_fails": {
			prompt:         "Hello!",
			convStyle:      pam.PromptEchoOn,
			want:           "Hey, hey!",
			stringResponse: "Hey, hey!",
			wantExitError:  pam_test.ErrReturnMismatch,
		},
		"Error_when_when_parsing_returned_value_style_fails": {
			prompt:    "Hello!",
			convStyle: pam.PromptEchoOn,
			want:      "Hey, hey!",
			stringResponse: map[string]dbus.Variant{
				"style": dbus.MakeVariant("shouldn't be a string"),
				"reply": dbus.MakeVariant("Hey, hey!"),
			},
			wantExitError: pam_test.ErrInvalidArguments,
		},
		"Error_when_when_parsing_returned_reply_fails": {
			prompt:    "Hello!",
			convStyle: pam.PromptEchoOff,
			want:      "Hey, hey!",
			stringResponse: map[string]dbus.Variant{
				"style": dbus.MakeVariant(pam.PromptEchoOff),
				"reply": dbus.MakeVariant(2.55),
			},
			wantExitError: pam_test.ErrInvalidArguments,
		},
	}
	for name, tc := range stringConvTests {
		t.Run("StringConv "+name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFunCalled := false
			convHandler := func() pam.ConversationFunc {
				if tc.convHandler != nil {
					return *tc.convHandler
				}
				prompt := tc.prompt
				if tc.promptFormat != "" {
					prompt = fmt.Sprintf(tc.promptFormat, tc.promptFormatArgs...)
				}
				return pam.ConversationFunc(
					func(style pam.Style, msg string) (string, error) {
						convFunCalled = true
						require.Equal(t, prompt, msg)
						require.Equal(t, tc.convStyle, style)
						switch style {
						case pam.PromptEchoOff, pam.PromptEchoOn:
							return tc.want, tc.convError
						default:
							return "", tc.convError
						}
					})
			}()

			var methodCalls []cliMethodCall
			wantStringResponse := any(nil)
			if tc.wantError == nil && tc.stringResponse == nil {
				wantStringResponse = map[string]dbus.Variant{
					"style": dbus.MakeVariant(tc.convStyle),
					"reply": dbus.MakeVariant(tc.want),
				}
			}
			if tc.stringResponse != nil {
				wantStringResponse = tc.stringResponse
			}

			wantReturnValues := []any{
				wantStringResponse,
				tc.wantError,
			}

			if tc.promptFormat != "" {
				methodCalls = append(methodCalls, cliMethodCall{
					m:    "StartStringConvf",
					args: append([]any{tc.convStyle, tc.promptFormat}, tc.promptFormatArgs...),
					r:    wantReturnValues,
				})
			} else {
				methodCalls = append(methodCalls, cliMethodCall{
					m:    "StartStringConv",
					args: []any{tc.convStyle, tc.prompt},
					r:    wantReturnValues,
				})
			}

			tx := preparePamTransactionWithConv(t, libPath, execClient,
				methodCallsAsArgs(methodCalls), "", convHandler)
			require.ErrorIs(t, tx.Authenticate(0), tc.wantExitError,
				"Authenticate does not return expected error")

			wantConFuncCalled := !tc.convShouldNotBeCalled && tc.convHandler == nil
			require.Equal(t, wantConFuncCalled, convFunCalled)
		})
	}

	// These tests are checking that GetUser works as expected, in case using conversation.
	tests := map[string]struct {
		presetUser  string
		convHandler pam.ConversationHandler

		want      string
		wantError error
	}{
		"Getting_a_previously_set_user_does_not_require_conversation_handler": {
			presetUser: "an-user",
			want:       "an-user",
		},
		"Getting_a_previously_set_user_does_not_use_conversation_handler": {
			presetUser: "an-user",
			want:       "an-user",
			convHandler: pam.ConversationFunc(func(s pam.Style, msg string) (string, error) {
				return "another-user", pam.ErrConv
			}),
		},
		"Getting_the_user_uses_conversation_handler_if_none_was_set": {
			want: "provided-user",
			convHandler: pam.ConversationFunc(
				func(s pam.Style, msg string) (string, error) {
					require.Equal(t, msg, "Who are you?")
					if msg != "Who are you?" {
						return "", pam.ErrConv
					}
					if s == pam.PromptEchoOn {
						return "provided-user", nil
					}
					return "", pam.ErrConv
				}),
		},

		// Error cases
		"Error_when_no_conversation_is_set": {
			want:      "",
			wantError: pam.ErrConv,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var methodCalls []cliMethodCall

			prompt := "Who are you?"
			methodCalls = append(methodCalls, cliMethodCall{
				m:    "GetUser",
				args: []any{prompt},
				r:    []any{tc.want, tc.wantError},
			})

			tx := preparePamTransactionWithConv(t, libPath, execClient,
				methodCallsAsArgs(methodCalls), tc.presetUser, tc.convHandler)
			require.NoError(t, tx.Authenticate(0), "Authenticate should not fail")
		})
	}
}

func TestExecModuleUnimplementedActions(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	if !pam.CheckPamHasStartConfdir() {
		t.Fatal("can't test with this libpam version!")
	}

	libPath := buildExecModule(t)
	execClient := buildExecClient(t)

	tx := preparePamTransaction(t, libPath, execClient, nil, "an-user")
	require.Error(t, tx.SetCred(pam.Flags(0)), pam.ErrIgnore)
	require.Error(t, tx.OpenSession(pam.Flags(0)), pam.ErrIgnore)
	require.Error(t, tx.CloseSession(pam.Flags(0)), pam.ErrIgnore)
}

func getModuleArgs(t *testing.T, clientPath string, args []string) []string {
	t.Helper()

	moduleArgs := []string{"--exec-debug"}
	if env := testutils.CoverDirEnv(); env != "" {
		moduleArgs = append(moduleArgs, "--exec-env", env)
	}
	if testutils.IsRace() {
		moduleArgs = append(moduleArgs, "--exec-env", "GORACE")
	}
	if testutils.IsAsan() {
		moduleArgs = append(moduleArgs, "--exec-env", "ASAN_OPTIONS")
		moduleArgs = append(moduleArgs, "--exec-env", "LSAN_OPTIONS")
	}

	logFile := os.Stderr.Name()
	if !testing.Verbose() {
		logFile = prepareFileLogging(t, "exec-module.log")
	}
	moduleArgs = append(moduleArgs, "--exec-log", logFile)

	if clientPath != "" {
		moduleArgs = append(moduleArgs, "--", clientPath)
		moduleArgs = append(moduleArgs, "-client-log", logFile)

		if len(strings.Join(append(moduleArgs, args...), " ")) > 768 {
			// FIXME: If the number of arguments is too big, we may break old PAM.
			// This is not required anymore when we can use libpam 1.6.0 in CI:
			// https://github.com/linux-pam/linux-pam/pull/658
			clientArgsPath := filepath.Join(t.TempDir(), "client-args-file")
			require.NoError(t, os.WriteFile(clientArgsPath, []byte(strings.Join(args, "\t")), 0600),
				"Setup: Creation of client args file failed")
			testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, clientArgsPath)
			return append(moduleArgs, "-client-args-file", clientArgsPath)
		}
	}
	return append(moduleArgs, args...)
}

func preparePamTransaction(t *testing.T, libPath string, clientPath string, args []string, user string) *pam.Transaction {
	t.Helper()

	return preparePamTransactionWithConv(t, libPath, clientPath, args, user, nil)
}

func preparePamTransactionWithConv(t *testing.T, libPath string, clientPath string, args []string, user string, conv pam.ConversationHandler) *pam.Transaction {
	t.Helper()

	serviceFile := createServiceFile(t, execServiceName, libPath, getModuleArgs(t, clientPath, args))
	return preparePamTransactionForServiceFile(t, serviceFile, user, conv)
}

func preparePamTransactionWithActionArgs(t *testing.T, libPath string, clientPath string, actionArgs actionArgsMap, user string) *pam.Transaction {
	t.Helper()

	actionArgs = maps.Clone(actionArgs)
	for a := range actionArgs {
		actionArgs[a] = getModuleArgs(t, clientPath, actionArgs[a])
	}

	serviceFile := createServiceFileWithActionArgs(t, execServiceName, libPath, actionArgs)
	return preparePamTransactionForServiceFile(t, serviceFile, user, nil)
}

func preparePamTransactionForServiceFile(t *testing.T, serviceFile string, user string, conv pam.ConversationHandler) *pam.Transaction {
	t.Helper()

	var tx *pam.Transaction
	var err error

	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	// FIXME: pam.Transaction doesn't handle well pam.ConversationHandler(nil)
	if conv != nil && !reflect.ValueOf(conv).IsNil() {
		tx, err = pam.StartConfDir(filepath.Base(serviceFile), user, conv, filepath.Dir(serviceFile))
	} else {
		tx, err = pam.StartConfDir(filepath.Base(serviceFile), user, nil, filepath.Dir(serviceFile))
	}
	testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, serviceFile)
	require.NoError(t, err, "PAM: Error to initialize module")
	require.NotNil(t, tx, "PAM: Transaction is not set")
	t.Cleanup(func() { require.NoError(t, tx.End(), "PAM: can't end transaction") })

	return tx
}

func performAllPAMActions(t *testing.T, tx *pam.Transaction, flags pam.Flags, wantError error) {
	t.Helper()

	t.Run("Authenticate", func(t *testing.T) { require.ErrorIs(t, tx.Authenticate(flags), wantError) })
	t.Run("AcctMgmt", func(t *testing.T) { require.ErrorIs(t, tx.AcctMgmt(flags), wantError) })
	t.Run("ChangeAuthTok", func(t *testing.T) { require.ErrorIs(t, tx.ChangeAuthTok(flags), wantError) })
	t.Run("SetCred", func(t *testing.T) { require.ErrorIs(t, tx.SetCred(flags), wantError) })
	t.Run("Open and Close Session", func(t *testing.T) {
		require.ErrorIs(t, tx.OpenSession(flags), wantError)
		require.ErrorIs(t, tx.CloseSession(flags), wantError)
	})
}

func buildExecModule(t *testing.T) string {
	t.Helper()

	return buildExecModuleWithCFlags(t, nil, false)
}

func buildExecModuleWithCFlags(t *testing.T, cFlags []string, forPreload bool) string {
	t.Helper()

	pkgConfigDeps := []string{"gio-2.0", "gio-unix-2.0"}
	// t.Name() can be a subtest, so replace the directory slash to get a valid filename.
	return buildSharedModule(t, "Building PAM module", execModuleSources, pkgConfigDeps, cFlags,
		[]string{"-lpam"}, "pam_authd_exec"+strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")),
		forPreload)
}

func buildExecClient(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("go", "build")
	cmd.Dir = filepath.Join(testutils.CurrentDir(), "cmd/exec-client")
	cmd.Args = append(cmd.Args, testutils.GoBuildFlags()...)
	cmd.Args = append(cmd.Args, "-gcflags=all=-N -l")
	cmd.Args = append(cmd.Args, "-tags=pam_tests_exec_client")
	cmd.Env = append(os.Environ(), `CGO_CFLAGS=-O0 -g3`)

	execPath := filepath.Join(t.TempDir(), "exec-client")
	t.Logf("Compiling Exec client at %s", execPath)
	t.Log(strings.Join(cmd.Args, " "))

	cmd.Args = append(cmd.Args, "-o", execPath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "Setup: could not compile PAM exec client: %s", out)

	return execPath
}

func ptrValue[T any](value T) *T {
	return &value
}

type cliMethodCall struct {
	// m is the method name to call.
	m string
	// args is the arguments to pass to the method.
	args []any
	// r is the expected method return values
	r []any
}

func (cmc cliMethodCall) format() string {
	argsParser := func(values []any) []dbus.Variant {
		var variantValues []dbus.Variant
		for _, v := range values {
			variantValues = append(variantValues, getVariant(v))
		}
		return variantValues
	}

	callMap := map[string]dbus.Variant{}
	callMap["act"] = dbus.MakeVariant(cmc.m)
	callMap["args"] = dbus.MakeVariant(argsParser(cmc.args))

	if cmc.r != nil {
		callMap["exp"] = dbus.MakeVariant(argsParser(cmc.r))
	}

	return dbus.MakeVariant(callMap).String()
}

func getVariant(value any) dbus.Variant {
	switch v := value.(type) {
	case pam.Error:
		return getVariant(int(v))
	case syscall.Signal:
		return getVariant(int(v))
	case nil:
		return getVariant("<@mv nothing>")
	default:
		return dbus.MakeVariant(value)
	}
}

func methodCallsAsArgs(methodCalls []cliMethodCall) []string {
	var args []string
	for _, mc := range methodCalls {
		args = append(args, mc.format())
	}
	return args
}
