package pam_test

import (
	"fmt"
	"testing"

	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

func ptrValue[T any](value T) *T {
	return &value
}

func bytesPointerDecoder(ptr pam.BinaryPointer) ([]byte, error) {
	if ptr == nil {
		return nil, nil
	}
	return *(*[]byte)(ptr), nil
}

func TestSetGetItem(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
		item  pam.Item
		value *string

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
		"Setting_and_getting_an_user": {
			item:      pam.User,
			value:     ptrValue("the-user"),
			wantValue: ptrValue("the-user"),
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
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			tx := NewModuleTransactionDummy(nil)

			if tc.value != nil {
				err := tx.SetItem(tc.item, *tc.value)
				require.ErrorIs(t, err, tc.wantSetError)
			}

			if tc.wantValue != nil {
				value, err := tx.GetItem(tc.item)
				require.Equal(t, value, *tc.wantValue)
				require.ErrorIs(t, err, tc.wantGetError)
			}
		})
	}
}

func TestSetPutEnv(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
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
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			tx := NewModuleTransactionDummy(nil)
			envList, err := tx.GetEnvList()
			require.NoErrorf(t, err, "Setup: GetEnvList should not return an error")
			require.Lenf(t, envList, 0, "Setup: GetEnvList should have elements")

			if tc.presetValues != nil && !tc.skipPut {
				for env, value := range tc.presetValues {
					err := tx.PutEnv(env + "=" + value)
					require.NoError(t, err)
				}
				envList, err = tx.GetEnvList()
				require.NoError(t, err)
				require.Equal(t, tc.presetValues, envList)
			}

			if !tc.skipPut {
				var env string
				if tc.value != nil {
					env = tc.env + "=" + *tc.value
				} else {
					env = tc.env
				}
				err := tx.PutEnv(env)
				require.ErrorIs(t, err, tc.wantPutError)

				wantEnv := map[string]string{}
				if tc.wantPutError == nil {
					if tc.value != nil {
						wantEnv = map[string]string{tc.env: *tc.value}
					}
					if tc.value != nil && tc.wantValue != nil {
						wantEnv = map[string]string{tc.env: *tc.wantValue}
					}
				}
				gotEnv, err := tx.GetEnvList()
				require.NoError(t, err, "tx.GetEnvList should not return an error")
				require.Equal(t, wantEnv, gotEnv, "returned env lits should match expected")
			}

			if tc.wantValue != nil {
				value := tx.GetEnv(tc.env)
				require.Equal(t, value, *tc.wantValue)
			}
		})
	}
}

func TestSetGetData(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
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
			presetData: map[string]any{"some-data": []any{"hey! That's", true}},
			key:        "data",
			data:       []any{"hey! That's", true},
			wantData:   []any{"hey! That's", true},
		},
		"Set_replaces_data": {
			presetData: map[string]any{"some-data": []any{"hey! That's", true}},
			key:        "some-data",
			data: ModuleTransactionDummy{
				Items: map[pam.Item]string{pam.Tty: "yay"},
				Env:   map[string]string{"foo": "bar"},
			},
			wantData: ModuleTransactionDummy{
				Items: map[pam.Item]string{pam.Tty: "yay"},
				Env:   map[string]string{"foo": "bar"},
			},
		},
		// This is weird, but it's to mimic actual PAM behavior:
		// See: https://github.com/linux-pam/linux-pam/pull/780
		"Nil_is_returned_when_getting_data_that_has_been_removed": {
			presetData: map[string]any{"some-data": []any{"hey! That's", true}},
			key:        "some-data",
			data:       nil,
		},

		// Error cases
		"Error_when_getting_data_that_has_never_been_set": {
			skipSet:      true,
			key:          "not set",
			wantGetError: pam.ErrNoModuleData,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			tx := NewModuleTransactionDummy(nil)

			if tc.presetData != nil && !tc.skipSet {
				for key, value := range tc.presetData {
					err := tx.SetData(key, value)
					require.NoError(t, err)
				}
			}

			if !tc.skipSet {
				err := tx.SetData(tc.key, tc.data)
				require.ErrorIs(t, err, tc.wantSetError)
			}

			if !tc.skipGet {
				data, err := tx.GetData(tc.key)
				require.ErrorIs(t, err, tc.wantGetError)
				require.Equal(t, tc.wantData, data)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

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
			t.Cleanup(MaybeDoLeakCheck)

			tx := NewModuleTransactionDummy(tc.convHandler)

			if tc.presetUser != "" {
				err := tx.SetItem(pam.User, tc.presetUser)
				require.NoError(t, err)
			}

			prompt := "Who are you?"
			user, err := tx.GetUser(prompt)
			require.ErrorIs(t, err, tc.wantError)
			require.Equal(t, tc.want, user)
		})
	}
}

func TestStartStringConv(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
		prompt                string
		promptFormat          string
		promptFormatArgs      []interface{}
		convStyle             pam.Style
		convError             error
		convHandler           *pam.ConversationFunc
		convShouldNotBeCalled bool

		want      string
		wantError error
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
		"Error_if_no_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationFunc(nil)),
			wantError:   pam.ErrConv,
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
			t.Cleanup(MaybeDoLeakCheck)

			convFunCalled := false
			tx := NewModuleTransactionDummy(func() pam.ConversationFunc {
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
						return tc.want, tc.convError
					})
			}())

			var reply pam.StringConvResponse
			var err error

			if tc.promptFormat != "" {
				reply, err = tx.StartStringConvf(tc.convStyle, tc.promptFormat,
					tc.promptFormatArgs...)
			} else {
				reply, err = tx.StartStringConv(tc.convStyle, tc.prompt)
			}

			wantConFuncCalled := !tc.convShouldNotBeCalled && tc.convHandler == nil
			require.Equal(t, wantConFuncCalled, convFunCalled)
			require.ErrorIs(t, err, tc.wantError)

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

func TestStartBinaryConv(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
		request     []byte
		convError   error
		convHandler *pam.ConversationHandler

		want      []byte
		wantError error
	}{
		"Simple_binary_conversation": {
			request: []byte{0x01, 0x02, 0x03},
			want:    []byte{0x00, 0x01, 0x02, 0x03, 0x4},
		},

		// Error cases
		"Error_if_no_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationHandler(nil)),
			wantError:   pam.ErrConv,
		},
		"Error_if_no_binary_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationHandler(pam.ConversationFunc(
				func(s pam.Style, msg string) (string, error) {
					return "", nil
				}))),
			wantError: pam.ErrConv,
		},
		"Error_if_the_conversation_handler_fails": {
			request:   []byte{0x03, 0x02, 0x01},
			convError: pam.ErrBuf,
			wantError: pam.ErrBuf,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			convFunCalled := false
			tx := NewModuleTransactionDummy(func() pam.ConversationHandler {
				if tc.convHandler != nil {
					return *tc.convHandler
				}

				return pam.BinaryConversationFunc(
					func(ptr pam.BinaryPointer) ([]byte, error) {
						convFunCalled = true
						require.NotNil(t, ptr)
						bytes := *(*[]byte)(ptr)
						require.Equal(t, tc.request, bytes)
						return tc.want, tc.convError
					})
			}())

			response, err := tx.StartBinaryConv(tc.request)
			require.ErrorIs(t, err, tc.wantError)
			require.Equal(t, tc.convHandler == nil, convFunCalled)

			if tc.wantError != nil {
				require.Nil(t, response)
				return
			}

			defer response.Release()
			require.NotNil(t, response)
			require.Equal(t, pam.BinaryPrompt, response.Style())
			require.NotNil(t, response.Data())
			bytes, err := response.Decode(bytesPointerDecoder)
			require.NoError(t, err)
			require.Equal(t, tc.want, bytes)

			bytes, err = response.Decode(nil)
			require.ErrorContains(t, err, "nil decoder provided")
			require.Nil(t, bytes)
		})
	}
}

func TestStartBinaryPointerConv(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
		request     []byte
		convError   error
		convHandler *pam.ConversationHandler

		want      []byte
		wantError error
	}{
		"With_nil_argument": {
			request: nil,
			want:    nil,
		},
		"With_empty_argument": {
			request: []byte{},
			want:    []byte{},
		},
		"With_simple_argument": {
			request: []byte{0x01, 0x02, 0x03},
			want:    []byte{0x00, 0x01, 0x02, 0x03, 0x4},
		},

		// Error cases
		"Error_if_no_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationHandler(nil)),
			wantError:   pam.ErrConv,
		},
		"Error_if_no_binary_conversation_handler_is_set": {
			convHandler: ptrValue(pam.ConversationHandler(pam.ConversationFunc(
				func(s pam.Style, msg string) (string, error) {
					return "", nil
				}))),
			wantError: pam.ErrConv,
		},
		"Error_if_the_conversation_handler_fails": {
			request:   []byte{0xde, 0xad, 0xbe, 0xef, 0xf},
			convError: pam.ErrBuf,
			wantError: pam.ErrBuf,
		},
		"Error_if_no_conversation_handler_is_set_handles_allocated_data": {
			convError: pam.ErrSystem,
			want:      []byte{0xde, 0xad, 0xbe, 0xef, 0xf},
			wantError: pam.ErrSystem,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			convFunCalled := false
			tx := NewModuleTransactionDummy(func() pam.ConversationHandler {
				if tc.convHandler != nil {
					return *tc.convHandler
				}

				return pam.BinaryPointerConversationFunc(
					func(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
						convFunCalled = true
						if tc.request == nil {
							require.Nil(t, ptr)
						} else {
							require.NotNil(t, ptr)
						}
						bytes := cBytesToBytes(ptr, len(tc.request))
						require.Equal(t, tc.request, bytes)
						return allocateCBytes(tc.want), tc.convError
					})
			}())
			res, err := tx.StartConv(pam.NewBinaryConvRequest(
				allocateCBytes(tc.request), releaseCBytesPointer))
			require.ErrorIs(t, err, tc.wantError)
			require.Equal(t, tc.convHandler == nil, convFunCalled)

			if tc.wantError != nil {
				require.Nil(t, res)
				return
			}

			response, ok := res.(pam.BinaryConvResponse)
			require.True(t, ok)
			defer response.Release()
			require.NotNil(t, response)
			require.Equal(t, pam.BinaryPrompt, response.Style())
			if tc.want == nil {
				require.Nil(t, response.Data())
			} else {
				require.NotNil(t, response.Data())
			}
			bytes, err := response.Decode(func(ptr pam.BinaryPointer) ([]byte, error) {
				return cBytesToBytes(ptr, len(tc.want)), nil
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, bytes)

			bytes, err = response.Decode(nil)
			require.ErrorContains(t, err, "nil decoder provided")
			require.Nil(t, bytes)
		})
	}
}

type multiConvHandler struct {
	t            *testing.T
	responses    []pam.ConvResponse
	wantRequests []pam.ConvRequest
	timesCalled  int
}

func (c *multiConvHandler) next() (pam.ConvRequest, pam.ConvResponse) {
	i := c.timesCalled
	c.timesCalled++

	return c.wantRequests[i], c.responses[i]
}

func (c *multiConvHandler) RespondPAM(style pam.Style, prompt string) (string, error) {
	wantReq, response := c.next()
	require.Equal(c.t, wantReq.Style(), style)
	stringReq, ok := wantReq.(pam.StringConvRequest)
	require.True(c.t, ok)
	require.Equal(c.t, stringReq.Prompt(), prompt)
	stringRes, ok := response.(pam.StringConvResponse)
	require.True(c.t, ok)
	return stringRes.Response(), nil
}

func (c *multiConvHandler) RespondPAMBinary(ptr pam.BinaryPointer) ([]byte, error) {
	wantReq, response := c.next()
	require.Equal(c.t, wantReq.Style(), pam.BinaryPrompt)

	binReq, ok := wantReq.(pam.BinaryConvRequester)
	require.True(c.t, ok)
	wantReqBytes, err := bytesPointerDecoder(binReq.Pointer())
	require.NoError(c.t, err)
	actualReqBytes, err := bytesPointerDecoder(ptr)
	require.NoError(c.t, err)
	require.Equal(c.t, wantReqBytes, actualReqBytes)

	binRes, ok := response.(pam.BinaryConvResponse)
	require.True(c.t, ok)
	bytes, err := binRes.Decode(bytesPointerDecoder)
	require.NoError(c.t, err)
	return bytes, nil
}

func TestStartConvMulti(t *testing.T) {
	t.Parallel()
	t.Cleanup(MaybeDoLeakCheck)

	tests := map[string]struct {
		requests []pam.ConvRequest

		wantResponses []pam.ConvResponse
		wantConvCalls *int
		wantError     error
	}{
		"Can_address_multiple_string_requests": {
			requests: []pam.ConvRequest{
				pam.NewStringConvRequest(pam.PromptEchoOff, "give some PromptEchoOff"),
				pam.NewStringConvRequest(pam.PromptEchoOn, "give some PromptEchoOn"),
				pam.NewStringConvRequest(pam.ErrorMsg, "give some ErrorMsg"),
				pam.NewStringConvRequest(pam.TextInfo, "give some TextInfo"),
			},
			wantResponses: []pam.ConvResponse{
				StringResponseDummy{pam.PromptEchoOff, "answer to PromptEchoOff"},
				StringResponseDummy{pam.PromptEchoOn, "answer to PromptEchoOn"},
				StringResponseDummy{pam.ErrorMsg, "answer to ErrorMsg"},
				StringResponseDummy{pam.TextInfo, "answer to TextInfo"},
			},
		},
		"Can_address_multiple_binary_requests": {
			requests: []pam.ConvRequest{
				NewBinaryRequestDummy(nil),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{})),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0xFF, 0x00, 0xBA, 0xAB})),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0x55})),
			},
			wantResponses: []pam.ConvResponse{
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{})},
				&BinaryResponseDummy{nil},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0x53})},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0xAF, 0x00, 0xBA, 0xAC})},
			},
		},
		"Can_address_multiple_mixed_binary_and_string_requests": {
			requests: []pam.ConvRequest{
				NewBinaryRequestDummy(nil),
				pam.NewStringConvRequest(pam.PromptEchoOff, "PromptEchoOff"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{})),
				pam.NewStringConvRequest(pam.PromptEchoOn, "PromptEchoOn"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0xFF, 0x00, 0xBA, 0xAB})),
				pam.NewStringConvRequest(pam.ErrorMsg, "ErrorMsg"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0x55})),
				pam.NewStringConvRequest(pam.TextInfo, "TextInfo"),
			},
			wantResponses: []pam.ConvResponse{
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{})},
				StringResponseDummy{pam.PromptEchoOff, "PromptEchoOff"},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0x55})},
				StringResponseDummy{pam.PromptEchoOn, "PromptEchoOn"},
				&BinaryResponseDummy{nil},
				StringResponseDummy{pam.ErrorMsg, "ErrorMsg"},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0xAF, 0x00, 0xBA, 0xAC})},
				StringResponseDummy{pam.TextInfo, "TextInfo"},
			},
		},

		// Error cases
		"Error_if_no_request_is_provided": {
			wantError: pam.ErrConv,
		},
		"Error_if_one_of_the_multiple_request_fails": {
			requests: []pam.ConvRequest{
				NewBinaryRequestDummy(nil),
				pam.NewStringConvRequest(pam.PromptEchoOff, "PromptEchoOff"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{})),
				pam.NewStringConvRequest(pam.PromptEchoOn, "PromptEchoOn"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0xFF, 0x00, 0xBA, 0xAB})),
				// The case below will lead to the whole request to fail!
				pam.NewStringConvRequest(pam.Style(-1), "Invalid style"),
				pam.NewStringConvRequest(pam.ErrorMsg, "ErrorMsg"),
				NewBinaryRequestDummy(pam.BinaryPointer(&[]byte{0x55})),
				pam.NewStringConvRequest(pam.TextInfo, "TextInfo"),
			},
			wantResponses: []pam.ConvResponse{
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{})},
				StringResponseDummy{pam.PromptEchoOff, "PromptEchoOff"},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0x55})},
				StringResponseDummy{pam.PromptEchoOn, "PromptEchoOn"},
				&BinaryResponseDummy{nil},
				StringResponseDummy{pam.Style(-1), "Invalid style"},
				StringResponseDummy{pam.ErrorMsg, "ErrorMsg"},
				&BinaryResponseDummy{pam.BinaryPointer(&[]byte{0xAF, 0x00, 0xBA, 0xAC})},
				StringResponseDummy{pam.TextInfo, "TextInfo"},
			},
			wantConvCalls: ptrValue(5),
			wantError:     pam.ErrConv,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(MaybeDoLeakCheck)

			require.Equalf(t, len(tc.wantResponses), len(tc.requests),
				"Setup: mismatch on expectations / requests numbers")

			convHandler := &multiConvHandler{
				t:            t,
				wantRequests: tc.requests,
				responses:    tc.wantResponses,
			}
			tx := NewModuleTransactionDummy(convHandler)

			responses, err := tx.StartConvMulti(tc.requests)
			require.ErrorIs(t, err, tc.wantError)

			wantConvCalls := len(tc.requests)
			if tc.wantConvCalls != nil {
				wantConvCalls = *tc.wantConvCalls
			}
			require.Equal(t, convHandler.timesCalled, wantConvCalls)

			if tc.wantError != nil {
				require.Nil(t, responses)
				return
			}

			require.NotNil(t, responses)
			require.Len(t, responses, len(tc.requests))

			for i, res := range responses {
				wantRes := tc.wantResponses[i]
				require.Equal(t, wantRes.Style(), res.Style())

				switch r := res.(type) {
				case pam.StringConvResponse:
					require.Equal(t, wantRes, res)
				case pam.BinaryConvResponse:
					wantBinRes, ok := wantRes.(pam.BinaryConvResponse)
					require.True(t, ok)
					wb, err := wantBinRes.Decode(bytesPointerDecoder)
					require.NoError(t, err)
					bytes, err := r.Decode(bytesPointerDecoder)
					require.NoError(t, err)
					require.Equal(t, wb, bytes)
				default:
					t.Fatalf("conversation %#v is not handled", r)
				}
			}
		})
	}
}
