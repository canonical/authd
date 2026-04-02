package dbusmodule_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/dbusmodule"
	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

const ifaceName = "com.ubuntu.authd.pam"
const objectPath = "/com/ubuntu/authd/pam"

func TestTransactionConnectionError(t *testing.T) {
	t.Parallel()

	tx, cleanup, err := dbusmodule.NewTransaction(context.TODO(), "invalid-address")
	require.Nil(t, tx, "Transaction must be unset")
	require.Nil(t, cleanup, "Cleanup func must be unset")
	require.NotNil(t, err, "Error must be set")
}

func TestTransactionHandler(t *testing.T) {
	t.Parallel()

	tx, _ := prepareTransaction(t, nil)
	dbusTx, ok := tx.(*dbusmodule.Transaction)
	require.True(t, ok, "Transaction should be a dbus module Transaction")
	require.ErrorIs(t, dbusTx.InvokeHandler(nil, 0, nil), pam.ErrAbort)
}

func TestTransactionSetEnv(t *testing.T) {
	t.Parallel()

	const setMethodName = "SetEnv"
	const unsetMethodName = "UnsetEnv"

	tests := map[string]struct {
		env            string
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantError      error
	}{
		"Sets_an_env": {
			env:            "ENV=foo",
			methodReturns:  methodReturn{m: setMethodName, values: []any{0}},
			wantMethodCall: methodCall{setMethodName, []any{"ENV", "foo"}},
		},
		"Sets_an_empty_env": {
			env:            "ENV=",
			methodReturns:  methodReturn{m: setMethodName, values: []any{0}},
			wantMethodCall: methodCall{setMethodName, []any{"ENV", ""}},
		},
		"Unsets_an_env": {
			env:            "ENV",
			methodReturns:  methodReturn{m: unsetMethodName, values: []any{0}},
			wantMethodCall: methodCall{unsetMethodName, []any{"ENV"}},
		},

		// Error cases
		"Errors_when_setting_an_env,_receiving_a_DBus_error": {
			env:       "ENV=foo",
			wantError: pam.ErrSystem,
		},
		"Errors_when_setting_an_env,_receiving_a_PAM_error": {
			env:            "ENV=foo",
			methodReturns:  methodReturn{m: setMethodName, values: []any{pam.ErrBadItem}},
			wantMethodCall: methodCall{setMethodName, []any{"ENV", "foo"}},
			wantError:      pam.ErrBadItem,
		},
		"Errors_when_unsetting_an_env,_receiving_a_DBus_error": {
			env:       "ENV",
			wantError: pam.ErrSystem,
		},
		"Errors_when_unsetting_an_env,_receiving_a_PAM_error": {
			env:            "ENV",
			methodReturns:  methodReturn{m: unsetMethodName, values: []any{pam.ErrAbort}},
			wantMethodCall: methodCall{unsetMethodName, []any{"ENV"}},
			wantError:      pam.ErrAbort,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			err := tx.PutEnv(tc.env)
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestTransactionGetEnv(t *testing.T) {
	t.Parallel()

	const methodName = "GetEnv"

	tests := map[string]struct {
		env            string
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantValue      string
	}{
		"Gets_an_empty_env": {
			env:            "ENV",
			wantMethodCall: methodCall{methodName, []any{"ENV"}},
			methodReturns:  methodReturn{m: methodName, values: []any{0, ""}},
		},
		"Gets_an_value_env": {
			env:            "ENV",
			wantMethodCall: methodCall{methodName, []any{"ENV"}},
			methodReturns:  methodReturn{m: methodName, values: []any{0, "some-value"}},
			wantValue:      "some-value",
		},

		// Error cases
		"Errors_when_getting_an_env,_receiving_a_DBus_error": {
			env: "ENV",
		},
		"Errors_when_getting_an_env,_receiving_a_PAM_error": {
			env:            "ENV",
			wantMethodCall: methodCall{methodName, []any{"ENV", "foo"}},
			methodReturns:  methodReturn{m: methodName, values: []any{pam.ErrIncomplete, "some-value"}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})
			require.NotNil(t, ts, "Setup: failed creating transaction")

			value := tx.GetEnv(tc.env)
			require.Equal(t, tc.wantValue, value, "Env value mismatch")
		})
	}
}

func TestTransactionGetEnvList(t *testing.T) {
	t.Parallel()

	const methodName = "GetEnvList"
	someEnvList := map[string]string{"env1": "value1", "env2": "value2"}

	tests := map[string]struct {
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantEnvList    map[string]string
		wantError      error
	}{
		"Gets_an_empty_env_list": {
			wantMethodCall: methodCall{m: methodName},
			methodReturns:  methodReturn{m: methodName, values: []any{0, map[string]string{}}},
			wantEnvList:    map[string]string{},
		},
		"Gets_a_filled_env_list": {
			wantMethodCall: methodCall{m: methodName},
			methodReturns:  methodReturn{m: methodName, values: []any{0, someEnvList}},
			wantEnvList:    someEnvList,
		},

		// Error cases
		"Errors_when_getting_an_env_list,_receiving_a_DBus_error": {
			wantError: pam.ErrSystem,
		},
		"Errors_when_getting_an_env,_receiving_a_PAM_error": {
			wantMethodCall: methodCall{m: methodName},
			methodReturns:  methodReturn{m: methodName, values: []any{pam.ErrBuf, someEnvList}},
			wantError:      pam.ErrBuf,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			envList, err := tx.GetEnvList()
			require.Equal(t, tc.wantEnvList, envList, "Env list does not match")
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestTransactionSetItem(t *testing.T) {
	t.Parallel()

	const methodName = "SetItem"

	tests := map[string]struct {
		item           pam.Item
		value          string
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantError      error
	}{
		"Sets_an_item": {
			item:           pam.User,
			value:          "user",
			methodReturns:  methodReturn{m: methodName, values: []any{0}},
			wantMethodCall: methodCall{methodName, []any{pam.User, "user"}},
		},
		"Sets_an_empty_item": {
			item:           pam.Rhost,
			value:          "",
			methodReturns:  methodReturn{m: methodName, values: []any{0}},
			wantMethodCall: methodCall{methodName, []any{pam.Rhost, ""}},
		},

		// Error cases
		"Errors_when_setting_an_item,_receiving_a_DBus_error": {
			value:     "item",
			wantError: pam.ErrSystem,
		},
		"Errors_when_setting_an_item,_receiving_a_PAM_error": {
			item:           pam.User,
			value:          "user",
			methodReturns:  methodReturn{m: methodName, values: []any{pam.ErrBadItem}},
			wantMethodCall: methodCall{methodName, []any{pam.User, "user"}},
			wantError:      pam.ErrBadItem,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			err := tx.SetItem(tc.item, tc.value)
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestTransactionGetItem(t *testing.T) {
	t.Parallel()

	const methodName = "GetItem"

	tests := map[string]struct {
		item           pam.Item
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantValue      string
		wantError      error
	}{
		"Gets_an_item": {
			item:           pam.User,
			wantValue:      "user",
			methodReturns:  methodReturn{m: methodName, values: []any{0, "user"}},
			wantMethodCall: methodCall{methodName, []any{pam.User}},
		},
		"Gets_an_empty_item": {
			item:           pam.Rhost,
			wantValue:      "",
			methodReturns:  methodReturn{m: methodName, values: []any{0, ""}},
			wantMethodCall: methodCall{methodName, []any{pam.Rhost}},
		},

		// Error cases
		"Errors_when_getting_an_item,_receiving_a_DBus_error": {
			item:      pam.Item(-1),
			wantError: pam.ErrSystem,
		},
		"Errors_when_getting_an_item,_receiving_a_PAM_error": {
			item:           pam.Item(-1),
			methodReturns:  methodReturn{m: methodName, values: []any{pam.ErrBadItem, "user"}},
			wantMethodCall: methodCall{methodName, []any{pam.Item(-1)}},
			wantError:      pam.ErrBadItem,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			value, err := tx.GetItem(tc.item)
			require.Equal(t, tc.wantValue, value, "Item %v values do not match", tc.item)
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestTransactionSetData(t *testing.T) {
	t.Parallel()

	const setMethodName = "SetData"
	const unsetMethodName = "UnsetData"
	testData := map[string]int32{"uno": 1, "due": 2, "tre": 3}
	variantTestData := dbus.MakeVariant(any(testData))

	tests := map[string]struct {
		key            string
		data           any
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantError      error
	}{
		"Sets_some_data": {
			key:            "data",
			data:           testData,
			methodReturns:  methodReturn{m: setMethodName, values: []any{0}},
			wantMethodCall: methodCall{setMethodName, []any{"data", variantTestData}},
		},
		"Unsets_some_data": {
			key:            "data-to-unset",
			data:           nil,
			methodReturns:  methodReturn{m: unsetMethodName, values: []any{0}},
			wantMethodCall: methodCall{unsetMethodName, []any{"data-to-unset"}},
		},

		// Error cases
		"Errors_when_setting_data,_receiving_a_DBus_error": {
			key:       "data",
			data:      testData,
			wantError: pam.ErrSystem,
		},
		"Errors_when_setting_data,_receiving_a_PAM_error": {
			key:            "data",
			data:           testData,
			methodReturns:  methodReturn{m: setMethodName, values: []any{pam.ErrBuf}},
			wantMethodCall: methodCall{setMethodName, []any{"data", variantTestData}},
			wantError:      pam.ErrBuf,
		},
		"Errors_when_unsetting_data,_receiving_a_DBus_error": {
			key:       "data",
			wantError: pam.ErrSystem,
		},
		"Errors_when_unsetting_data,_receiving_a_PAM_error": {
			key:            "data-to-unset",
			data:           nil,
			methodReturns:  methodReturn{m: unsetMethodName, values: []any{pam.ErrAbort}},
			wantMethodCall: methodCall{unsetMethodName, []any{"data-to-unset"}},
			wantError:      pam.ErrAbort,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			err := tx.SetData(tc.key, tc.data)
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestTransactionGetData(t *testing.T) {
	t.Parallel()

	const methodName = "GetData"
	testData := map[string]int32{"uno": 1, "due": 2, "tre": 3}
	variantTestData := dbus.MakeVariant(any(testData))

	tests := map[string]struct {
		key            string
		methodReturns  methodReturn
		wantMethodCall methodCall
		wantData       any
		wantError      error
	}{
		"Gets_some_data": {
			key:            "data",
			wantData:       testData,
			methodReturns:  methodReturn{m: methodName, values: []any{0, variantTestData}},
			wantMethodCall: methodCall{methodName, []any{"data"}},
		},

		// Error cases
		"Errors_when_getting_data,_receiving_a_DBus_error": {
			key:       "data",
			wantError: pam.ErrSystem,
		},
		"Errors_when_getting_data,_receiving_a_PAM_error": {
			key:            "data",
			methodReturns:  methodReturn{m: methodName, values: []any{pam.ErrNoModuleData, variantTestData}},
			wantMethodCall: methodCall{methodName, []any{"data"}},
			wantError:      pam.ErrNoModuleData,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, ts := prepareTransaction(t, []methodReturn{tc.methodReturns})

			data, err := tx.GetData(tc.key)
			require.Equal(t, tc.wantData, data, "Data mismatch")
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				calledMethods := ts.getCalledMethods()
				require.Len(t, calledMethods, 1, "Method calls not matching")
				require.Equal(t, tc.wantMethodCall, calledMethods[0], "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestStartStringConv(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		prompt                string
		promptFormat          string
		promptFormatArgs      []interface{}
		convStyle             pam.Style
		convError             pam.Error
		convShouldNotBeCalled bool

		want            string
		wantMethodCalls *methodCallExpectations
		wantError       error
	}{
		"Messages_with_error_style_are_handled_by_conversation": {
			prompt:    "This is an error!",
			convStyle: pam.ErrorMsg,
			want:      "I'm handling it fine though",
		},
		"Conversation_prompt_can_be_formatted": {
			promptFormat:     "Sending some %s, right? %v",
			promptFormatArgs: []interface{}{"info", true},
			convStyle:        pam.TextInfo,
			want:             "And returning some text back",
		},

		// Error cases
		"Error_if_conversation_receives_a_DBus_error": {
			wantError:             pam.ErrSystem,
			convShouldNotBeCalled: true,
		},
		"Error_if_the_conversation_handler_fails": {
			prompt:    "Tell me your secret!",
			convStyle: pam.PromptEchoOff,
			convError: pam.ErrBuf,
			wantError: pam.ErrBuf,
		},
		"Error_when_conversation_uses_binary_content_style": {
			prompt:                "I am a binary content\xff!",
			convStyle:             pam.BinaryPrompt,
			convError:             pam.ErrConv,
			wantError:             pam.ErrConv,
			convShouldNotBeCalled: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mce := methodCallExpectations{}
			prompt := tc.prompt
			if tc.promptFormat != "" {
				prompt = fmt.Sprintf(tc.promptFormat, tc.promptFormatArgs...)
			}

			if !tc.convShouldNotBeCalled {
				mce.add("Prompt", []any{tc.convStyle, prompt}, []any{tc.convError, tc.want})
			}

			tx, ts := prepareTransaction(t, mce.methodReturns)

			var reply pam.StringConvResponse
			var err error
			if tc.promptFormat != "" {
				reply, err = tx.StartStringConvf(tc.convStyle, tc.promptFormat,
					tc.promptFormatArgs...)
			} else {
				reply, err = tx.StartStringConv(tc.convStyle, tc.prompt)
			}

			if !errors.Is(tc.wantError, pam.ErrSystem) {
				require.Equal(t, mce.wantMethodCalls, ts.getCalledMethods(), "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)

			if tc.wantError != nil {
				require.Zero(t, reply)
				return
			}

			require.NotNil(t, reply)
			require.Equal(t, tc.want, reply.Response())
			require.Equal(t, tc.convStyle, reply.Style())
		})
	}
}

func TestTransactionGetUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		presetUser string
		getError   pam.Error
		convUser   string
		convError  pam.Error

		want      string
		wantError error
	}{
		"Getting_a_previously_set_user_does_not_require_conversation_handler": {
			presetUser: "an-user",
			want:       "an-user",
		},
		"Getting_a_previously_set_user_does_not_use_conversation_handler": {
			presetUser: "an-user",
			convUser:   "another-user",
			want:       "an-user",
		},
		"Getting_the_user_uses_conversation_handler_if_none_was_set": {
			want:     "provided-user",
			convUser: "provided-user",
		},

		// Error cases
		"Error_when_can't_get_user_item": {
			want:      "",
			getError:  pam.ErrBadItem,
			wantError: pam.ErrBadItem,
		},
		"Error_when_conversation_fails": {
			want:      "",
			convError: pam.ErrConv,
			wantError: pam.ErrConv,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mce := methodCallExpectations{}
			prompt := "Who are you?"

			mce.add("GetItem", []any{pam.User}, []any{tc.getError, tc.presetUser})
			if tc.presetUser == "" && tc.getError == pam.Error(0) {
				mce.add("Prompt", []any{pam.PromptEchoOn, prompt}, []any{tc.convError, tc.convUser})
			}

			tx, ts := prepareTransaction(t, mce.methodReturns)
			user, err := tx.GetUser(prompt)
			require.Equal(t, tc.want, user, "User dos not mach")
			if !errors.Is(tc.wantError, pam.ErrSystem) {
				require.Equal(t, mce.wantMethodCalls, ts.getCalledMethods(), "Method calls mismatch")
			}
			requireDbusErrorIs(t, err, tc.wantError)
		})
	}
}

func TestStartBinaryConv(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data      []byte
		wantError error
	}{
		// Error cases
		"Error_as_they_are_not_supported": {
			wantError: pam.ErrConv,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx, _ := prepareTransaction(t, nil)
			ret, err := tx.StartBinaryConv(tc.data)
			require.ErrorIs(t, err, tc.wantError)
			require.Nil(t, ret)
		})
	}
}

type methodCallExpectations struct {
	methodReturns   []methodReturn
	wantMethodCalls []methodCall
}

func (mce *methodCallExpectations) add(method string, args []any, ret []any) {
	mce.wantMethodCalls = append(mce.wantMethodCalls, methodCall{
		m: method, args: args,
	})
	mce.methodReturns = append(mce.methodReturns, methodReturn{
		m: method, values: ret,
	})
}

func requireDbusErrorIs(t *testing.T, err error, wantError error) {
	t.Helper()

	require.ErrorIs(t, err, wantError, "Error is not matching")

	if errors.Is(wantError, pam.ErrSystem) {
		var dbusError dbus.Error
		require.True(t, errors.As(err, &dbusError), "Error should be a dbus error")
	}
}

func prepareTransaction(t *testing.T, expectedReturns []methodReturn) (pam.ModuleTransaction, *testServer) {
	t.Helper()

	address, obj := prepareTestServer(t, expectedReturns)
	tx, cleanup, err := dbusmodule.NewTransaction(context.TODO(), address,
		dbusmodule.WithSharedConnection(true))
	require.NoError(t, err, "Setup: Can't connect to %s", address)
	t.Cleanup(cleanup)

	t.Logf("Using bus at address %s", address)

	return tx, obj
}

func prepareTestServer(t *testing.T, expectedReturns []methodReturn) (string, *testServer) {
	t.Helper()

	address, cleanup, err := testutils.StartBusMock()
	require.NoError(t, err, "Setup: Creating mock bus failed")
	t.Cleanup(cleanup)

	conn, err := dbus.Connect(address)
	require.NoError(t, err, "Setup: Connecting to system Bus failed")
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed closing the D-Bus connection: %v", err)
		}
	})

	obj := &testServer{t: t, mu: &sync.Mutex{}, returns: expectedReturns}
	err = conn.Export(obj, objectPath, ifaceName)
	require.NoError(t, err, "Setup: Exporting test server object to bus failed")

	reply, err := conn.RequestName(ifaceName, dbus.NameFlagDoNotQueue)
	require.NoError(t, err, "Setup: can't get dbus name")
	require.Equal(t, reply, dbus.RequestNameReplyPrimaryOwner,
		"Setup: can't get dbus name")

	return address, obj
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	m.Run()
}
