package gdm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/canonical/authd/internal/brokers/layouts"
	authd "github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSendToGdm(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		value []byte

		wantReturn                   []byte
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"JSON_null_data_can_be_sent_and_received": {
			value: []byte(`null`),
		},
		"JSON_number_can_be_sent_and_received": {
			value: []byte(`1.5`),
		},
		"Single_char_is_sent_and_received_as_string": {
			value: []byte(`"m"`),
		},
		"JSON_null_is_returned": {
			value:      []byte(`"give me 🚫"`),
			wantReturn: []byte("null"),
		},
		"Utf-8_data_is_sent_and_returned": {
			value:      []byte(`"give me 🍕"`),
			wantReturn: []byte(`"😋"`),
		},
		"Nil_data_returned": {
			value:      []byte(`"give me 🚫"`),
			wantReturn: []byte(nil),
		},

		// Error cases
		"Error_on_empty_data": {
			value:                        []byte{},
			wantError:                    ErrInvalidJSON,
			wantConvHandlerNotToBeCalled: true,
		},
		"Error_on_nil_data": {
			value:                        nil,
			wantError:                    ErrInvalidJSON,
			wantConvHandlerNotToBeCalled: true,
		},
		"Error_with_empty_data_returned": {
			value:      []byte(`"give me 🗑‼"`),
			wantReturn: []byte{},
			wantError:  ErrInvalidJSON,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(pam.BinaryPointerConversationFunc(
				func(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
					convFuncCalled = true
					require.NotNil(t, ptr)
					req, err := decodeJSONProtoMessage(ptr)
					require.NoError(t, err)
					require.Equal(t, tc.value, req)
					if tc.wantReturn != nil {
						msg, err := newJSONProtoMessage(tc.wantReturn)
						return pam.BinaryPointer(msg), err
					}
					msg, err := newJSONProtoMessage(req)
					return pam.BinaryPointer(msg), err
				}))

			data, err := sendToGdm(mtx, tc.value)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.ErrorIs(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)

			if tc.wantReturn != nil {
				require.Equal(t, tc.wantReturn, data)
				return
			}
			require.Equal(t, tc.value, data)
		})
	}
}

func TestSendDataPrivate(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		value *Data

		wantReturn                   []byte
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Send_data_can_handle_null_JSON_value_as_return_value": {
			value: &Data{
				Type: DataType_event,
				Event: &EventData{
					Type: EventType_brokerSelected,
					Data: &EventData_BrokerSelected{},
				},
			},

			wantReturn: []byte("null"),
		},
		"Can_send_Hello_packet_data": {
			value: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			wantReturn: []byte(`"hello gdm!"`),
		},

		// Error cases
		"Error_on_empty_data": {
			value:                        &Data{},
			wantConvHandlerNotToBeCalled: true,
			wantReturn:                   nil,
			wantError:                    errors.New("unexpected type unknownType"),
		},
		"Error_on_missing_data_return": {
			value: &Data{
				Type: DataType_event,
				Event: &EventData{
					Type: EventType_brokerSelected,
					Data: nil,
				},
			},

			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
		"Error_on_wrong_data": {
			value: &Data{
				Type:    DataType_event,
				Request: &RequestData{},
			},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(pam.BinaryPointerConversationFunc(
				func(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
					convFuncCalled = true
					require.NotNil(t, ptr)
					req, err := decodeJSONProtoMessage(ptr)
					require.NoError(t, err)
					valueJSON, err := tc.value.JSON()
					require.NoError(t, err)
					require.Equal(t, valueJSON, req)
					if tc.wantReturn != nil {
						msg, err := newJSONProtoMessage(tc.wantReturn)
						require.NoError(t, err)
						return pam.BinaryPointer(msg), nil
					}
					msg, err := newJSONProtoMessage(req)
					require.NoError(t, err)
					return pam.BinaryPointer(msg), nil
				}))

			data, err := sendData(mtx, tc.value)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.Nil(t, data)
				require.ErrorContains(t, err, tc.wantError.Error())
				return
			}
			require.NoError(t, err)

			if tc.wantReturn != nil {
				require.Equal(t, tc.wantReturn, data)
				return
			}
			require.Equal(t, tc.value, data)
		})
	}
}

func TestSendData(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		value                *Data
		uncheckedReturnValue bool

		wantReturn                   *Data
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Can_send_Hello_packet_data": {
			value: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			wantReturn: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{},
			},
		},

		// Error cases
		"Error_on_empty_data": {
			value:                        &Data{},
			wantConvHandlerNotToBeCalled: true,
			wantReturn:                   nil,
			wantError:                    errors.New("unexpected type unknownType"),
		},
		"Error_on_empty_returned_data": {
			value: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			uncheckedReturnValue: true,
			wantReturn:           &Data{},
			wantError:            errors.New("unexpected type unknownType"),
		},
		"Error_on_missing_data_return": {
			value: &Data{
				Type: DataType_event,
				Event: &EventData{
					Type: EventType_brokerSelected,
					Data: nil,
				},
			},

			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
		"Error_on_wrong_data": {
			value: &Data{
				Type:    DataType_event,
				Request: &RequestData{},
			},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(pam.BinaryPointerConversationFunc(
				func(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
					convFuncCalled = true
					require.NotNil(t, ptr, "Binary data must not be nil")
					req, err := decodeJSONProtoMessage(ptr)
					require.NoError(t, err, "Incoming message JSON parsing failed")
					valueJSON, err := tc.value.JSON()
					require.NoError(t, err, "Value to JSON conversion failed")
					require.Equal(t, valueJSON, req)
					if tc.wantReturn != nil {
						var json []byte
						if tc.uncheckedReturnValue {
							json, err = protojson.Marshal(tc.wantReturn)
						} else {
							json, err = tc.wantReturn.JSON()
						}
						require.NoError(t, err, "Conversion to JSON failed")
						msg, err := newJSONProtoMessage(json)
						require.NoError(t, err, "Proto message conversion failed")
						return pam.BinaryPointer(msg), nil
					}
					msg, err := newJSONProtoMessage(req)
					require.NoError(t, err, "Proto message conversion failed")
					return pam.BinaryPointer(msg), nil
				}))

			data, err := SendData(mtx, tc.value)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled,
				"Conversation handler was not called")

			if tc.wantError != nil {
				require.Nil(t, data, "Unexpected SendData value: %#v", data)
				require.ErrorContains(t, err, tc.wantError.Error(),
					"Mismatching SendData error")
				return
			}
			require.NoError(t, err, "Unexpected SendData error")

			if tc.wantReturn != nil {
				requireEqualData(t, tc.wantReturn, data,
					"Unexpected SendData return value")
				return
			}
			require.Equal(t, tc.value, data)
		})
	}
}

func TestDataConversationFunc(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		inData   *Data
		inBinReq pam.BinaryConvRequester
		outData  *Data

		// Some tests may lead to some false-positive leak errors, however in TestMain
		// we have a final check for all tests ensuring this is not the case.
		mayHitLeakSanitizer bool

		wantReturn                   *Data
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Send_valid_data_and_return_it_back": {
			inData: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			outData:    &Data{Type: DataType_hello},
			wantReturn: &Data{Type: DataType_hello},
		},

		// Error cases
		"Error_on_invalid_protocol": {
			mayHitLeakSanitizer: true,
			inBinReq: func() pam.BinaryConvRequester {
				if pam_test.IsAddressSanitizerActive() {
					return nil
				}
				invalidData := allocateJSONProtoMessage()
				invalidData.initFull("testProto", 20, nil)
				return pam.NewBinaryConvRequest(invalidData.encode(),
					func(ptr pam.BinaryPointer) { (*jsonProtoMessage)(ptr).release() })
			}(),
			wantConvHandlerNotToBeCalled: true,
			wantError:                    ErrProtoNotSupported,
		},
		"Error_on_unexpected_JSON": {
			mayHitLeakSanitizer: true,
			inBinReq: func() pam.BinaryConvRequester {
				if pam_test.IsAddressSanitizerActive() {
					return nil
				}
				req, err := NewBinaryJSONProtoRequest([]byte("null"))
				require.NoError(t, err)
				return req
			}(),
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("syntax error"),
		},
		"Error_on_invalid_Returned_Data": {
			inData: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			outData:   &Data{},
			wantError: pam.ErrConv,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			if pam_test.IsAddressSanitizerActive() && tc.mayHitLeakSanitizer {
				t.Skip("This test may cause false positive detection of leaks, so we ignore it")
			}

			convFuncCalled := false
			var outData *Data
			var outErr error
			if tc.inBinReq != nil {
				defer tc.inBinReq.Release()
				outPtr, err := DataConversationFunc(func(d *Data) (*Data, error) {
					convFuncCalled = true
					requireEqualData(t, tc.inData, d)
					if tc.outData != nil {
						return tc.outData, nil
					}
					return nil, tc.wantError
				}).RespondPAMBinary(tc.inBinReq.Pointer())

				if err != nil {
					require.Nil(t, outPtr)
					outErr = err
				} else {
					json, err := decodeJSONProtoMessage(outPtr)
					require.NoError(t, err)
					defer (*jsonProtoMessage)(outPtr).release()
					outData, err = NewDataFromJSON(json)
					require.NoError(t, err)
				}
			} else {
				mtx := pam_test.NewModuleTransactionDummy(DataConversationFunc(
					func(data *Data) (*Data, error) {
						convFuncCalled = true
						requireEqualData(t, data, tc.inData)
						if tc.outData != nil {
							return tc.outData, nil
						}
						return nil, tc.wantError
					}))
				outData, outErr = SendData(mtx, tc.inData)
			}
			require.Equal(t, !convFuncCalled, tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.ErrorContains(t, outErr, tc.wantError.Error())
				return
			}
			require.NoError(t, outErr)
			requireEqualData(t, outData, tc.wantReturn)
		})
	}
}

func TestDataSendChecked(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		value *Data

		wantReturn                   *Data
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Can_send_and_receive_Hello_packet_data": {
			value: &Data{
				Type:  DataType_hello,
				Hello: &HelloData{Version: 12345},
			},
			wantReturn: &Data{Type: DataType_hello},
		},
		"Can_send_event_and_receive_an_event_ack": {
			value: &Data{
				Type: DataType_event,
				Event: &EventData{
					Type: EventType_brokerSelected,
					Data: &EventData_BrokerSelected{},
				},
			},

			wantReturn: &Data{Type: DataType_eventAck},
		},

		// Error cases
		"Error_on_empty_data": {
			value:                        &Data{},
			wantConvHandlerNotToBeCalled: true,
			wantReturn:                   nil,
			wantError:                    errors.New("unexpected type unknownType"),
		},
		"Error_on_missing_data_return": {
			value: &Data{
				Type: DataType_event,
				Event: &EventData{
					Type: EventType_brokerSelected,
					Data: nil,
				},
			},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
		"Error_on_wrong_data": {
			value: &Data{
				Type:    DataType_event,
				Request: &RequestData{},
			},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("missing event data"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(DataConversationFunc(
				func(req *Data) (*Data, error) {
					convFuncCalled = true
					requireEqualData(t, tc.value, req)

					if tc.wantReturn != nil {
						return tc.wantReturn, nil
					}
					return req, nil
				}))

			data, err := SendData(mtx, tc.value)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.Nil(t, data)
				require.ErrorContains(t, err, tc.wantError.Error())
				return
			}

			require.NoError(t, err)

			if tc.wantReturn != nil {
				require.Equal(t, tc.wantReturn, data)
			} else {
				require.Equal(t, tc.value, data)
			}
		})
	}
}

func TestDataSendPoll(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		wantReturn                   *Data
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Polling_handles_a_null_response": {
			wantReturn: &Data{
				Type: DataType_pollResponse,
			},
		},
		"Polling_handles_an_empty_response": {
			wantReturn: &Data{
				Type:         DataType_pollResponse,
				PollResponse: []*EventData{},
			},
		},
		"Polling_handles_multiple_event_events_response": {
			wantReturn: &Data{
				Type: DataType_pollResponse,
				PollResponse: []*EventData{
					{Type: EventType_authEvent, Data: &EventData_AuthEvent{}},
					{Type: EventType_authModeSelected, Data: &EventData_AuthModeSelected{}},
					{Type: EventType_uiLayoutReceived, Data: &EventData_UiLayoutReceived{}},
				},
			},
		},

		// Error cases
		"Error_on_nil_return": {
			wantReturn: nil,
			wantError:  errors.New("unexpected token null"),
		},
		"Error_on_unexpected_type": {
			wantReturn: &Data{Type: DataType_hello},
			wantError:  errors.New("gdm replied with an unexpected type: hello"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(DataConversationFunc(
				func(data *Data) (*Data, error) {
					convFuncCalled = true
					if tc.wantReturn != nil {
						return tc.wantReturn, nil
					}

					msg, err := newJSONProtoMessage([]byte("null"))
					require.NoError(t, err)
					defer msg.release()
					json, err := msg.JSON()
					require.NoError(t, err)
					return NewDataFromJSON(json)
				}))

			eventData, err := SendPoll(mtx)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.Nil(t, eventData)
				require.ErrorContains(t, err, tc.wantError.Error())
				return
			}
			require.NoError(t, err)
			requireEqualData(t, tc.wantReturn,
				&Data{Type: DataType_pollResponse, PollResponse: eventData})
		})
	}
}

func reformatJSONIndented(t *testing.T, input []byte) []byte {
	t.Helper()

	var indented bytes.Buffer
	err := json.Indent(&indented, input, "", "  ")
	require.NoError(t, err)
	return indented.Bytes()
}

func requireEqualData(t *testing.T, want *Data, actual *Data, args ...any) {
	t.Helper()

	// We can't compare data values as their content may contain elements
	// that may vary that are needed by protobuf implementation.
	// So let's compare the data JSON representation instead since that's what
	// we care about anyways.
	wantJSON, err := want.JSON()
	require.NoError(t, err, "Failed converting want value to JSON: %#v", want)
	actualJSON, err := actual.JSON()
	require.NoError(t, err, "Failed converting actual value to JSON: %#v", actual)

	require.Equal(t, string(reformatJSONIndented(t, wantJSON)),
		string(reformatJSONIndented(t, actualJSON)), args...)
}

type invalidRequest struct {
}

// Implement Request interface.
//
//nolint:revive // This is to implement Request interface defined by protobuf.
func (*invalidRequest) isRequestData_Data() {}

func TestDataSendRequestTyped(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		request Request

		wantData                     *Data
		wantError                    error
		wantConvHandlerNotToBeCalled bool
		wantReturnType               any
	}{
		"Request_change_state": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_changeStage,
					Data: &ResponseData_Ack{},
				},
			},
		},
		"Request_Ui_layout_capabilities": {
			request: &RequestData_UiLayoutCapabilities{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_uiLayoutCapabilities,
					Data: &ResponseData_UiLayoutCapabilities{},
				},
			},
		},
		"Request_change_state,_handles_nil_response_data": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_changeStage,
					Data: (*ResponseData_Ack)(nil),
				},
			},
		},
		"Request_Ui_layout_capabilities,_handles_nil_response_data": {
			request: &RequestData_UiLayoutCapabilities{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_uiLayoutCapabilities,
					Data: (*ResponseData_UiLayoutCapabilities)(nil),
				},
			},
		},
		"Request_change_state,_expecting_Ack_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_changeStage,
					Data: &ResponseData_Ack{},
				},
			},
			wantReturnType: &ResponseData_Ack{},
		},
		"Request_Ui_layout_capabilities,_expecting_Ack_type": {
			request: &RequestData_UiLayoutCapabilities{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_uiLayoutCapabilities,
					Data: &ResponseData_UiLayoutCapabilities{},
				},
			},
			wantError:      errors.New("impossible to convert"),
			wantReturnType: &ResponseData_Ack{},
		},
		"Request_change_state,_handles_nil_response_data,_expecting_Ack_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_changeStage,
					Data: (*ResponseData_Ack)(nil),
				},
			},
			wantReturnType: &ResponseData_Ack{},
		},
		"Request_change_state,_expecting_UiLayoutCapabilities_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_changeStage,
					Data: &ResponseData_Ack{},
				},
			},
			wantReturnType: &ResponseData_UiLayoutCapabilities{},
			wantError:      errors.New("impossible to convert"),
		},
		"Request_Ui_layout_capabilities,_handles_nil_response_data,_expecting_UiLayoutCapabilities_type": {
			request: &RequestData_UiLayoutCapabilities{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_uiLayoutCapabilities,
					Data: (*ResponseData_UiLayoutCapabilities)(nil),
				},
			},
			wantReturnType: &ResponseData_UiLayoutCapabilities{},
		},
		"Request_Ui_layout_capabilities,_expecting_UiLayoutCapabilities_type": {
			request: &RequestData_UiLayoutCapabilities{},
			wantData: &Data{
				Type: DataType_response,
				Response: &ResponseData{
					Type: RequestType_uiLayoutCapabilities,
					Data: &ResponseData_UiLayoutCapabilities{
						UiLayoutCapabilities: &Responses_UiLayoutCapabilities{
							SupportedUiLayouts: []*authd.UILayout{
								{
									Type: layouts.Form,
								},
							},
						},
					},
				},
			},
			wantReturnType: &ResponseData_UiLayoutCapabilities{},
		},

		// Error cases
		"Error_with_unknown_request": {
			request:                      &invalidRequest{},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("no known request type"),
		},
		// Error cases
		"Error_on_nil_return": {
			request:   &RequestData_ChangeStage{},
			wantData:  nil,
			wantError: errors.New("unexpected token null"),
		},
		"Error_with_mismatching_response_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type:     DataType_response,
				Response: &ResponseData{Type: RequestType_uiLayoutCapabilities},
			},
			wantError: errors.New("gdm replied with invalid response type"),
		},
		"Error_with_non-response_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_hello,
			},
			wantError: errors.New("gdm replied with an unexpected type: hello"),
		},
		"Error_with_unknown_request_expecting_Ack_type": {
			request:                      &invalidRequest{},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("no known request type"),
			wantReturnType:               &ResponseData_Ack{},
		},
		"Error_with_mismatching_response_type_expecting_Ack_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type:     DataType_response,
				Response: &ResponseData{Type: RequestType_uiLayoutCapabilities},
			},
			wantError:      errors.New("gdm replied with invalid response type"),
			wantReturnType: &ResponseData_Ack{},
		},
		"Error_with_non-response_type_expecting_Ack_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_hello,
			},
			wantError:      errors.New("gdm replied with an unexpected type: hello"),
			wantReturnType: &ResponseData_Ack{},
		},
		"Error_with_unknown_request_expecting_UiLayoutCapabilities_type": {
			request:                      &invalidRequest{},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("no known request type"),
			wantReturnType:               &ResponseData_UiLayoutCapabilities{},
		},
		"Error_with_mismatching_response_type_expecting_UiLayoutCapabilities_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type:     DataType_response,
				Response: &ResponseData{Type: RequestType_uiLayoutCapabilities},
			},
			wantError:      errors.New("gdm replied with invalid response type"),
			wantReturnType: &ResponseData_UiLayoutCapabilities{},
		},
		"Error_with_non-response_type_expecting_UiLayoutCapabilities_type": {
			request: &RequestData_ChangeStage{},
			wantData: &Data{
				Type: DataType_hello,
			},
			wantError:      errors.New("gdm replied with an unexpected type: hello"),
			wantReturnType: &ResponseData_UiLayoutCapabilities{},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(DataConversationFunc(
				func(data *Data) (*Data, error) {
					convFuncCalled = true
					if tc.wantData != nil {
						return tc.wantData, nil
					}

					msg, err := newJSONProtoMessage([]byte("null"))
					require.NoError(t, err)
					defer msg.release()
					json, err := msg.JSON()
					require.NoError(t, err)
					return NewDataFromJSON(json)
				}))
			var response Response
			var err error
			switch tc.wantReturnType.(type) {
			case *ResponseData_UiLayoutCapabilities:
				response, err = SendRequestTyped[*ResponseData_UiLayoutCapabilities](mtx, tc.request)
			case *ResponseData_Ack:
				response, err = SendRequestTyped[*ResponseData_Ack](mtx, tc.request)
			default:
				response, err = SendRequestTyped[Response](mtx, tc.request)
			}
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.Nil(t, response)
				require.ErrorContains(t, err, tc.wantError.Error())
				return
			}
			require.NoError(t, err)
			requireEqualData(t, tc.wantData, &Data{
				Type:     DataType_response,
				Response: &ResponseData{Type: tc.wantData.Response.Type, Data: response},
			})
		})
	}
}

type invalidEvent struct {
}

// Implement Event interface.
//
//nolint:revive // This is to implement Request interface defined by protobuf.
func (*invalidEvent) isEventData_Data() {}

func TestDataEmitEvent(t *testing.T) {
	t.Parallel()
	t.Cleanup(pam_test.MaybeDoLeakCheck)

	testCases := map[string]struct {
		event        Event
		returnedData []byte

		wantEventType                EventType
		wantError                    error
		wantConvHandlerNotToBeCalled bool
	}{
		"Emit_event_BrokersReceived": {
			event:         &EventData_BrokersReceived{},
			wantEventType: EventType_brokersReceived,
		},
		"Emit_event_BrokerSelected": {
			event:         &EventData_BrokerSelected{},
			wantEventType: EventType_brokerSelected,
		},
		"Emit_event_AuthModesReceived": {
			event:         &EventData_AuthModesReceived{},
			wantEventType: EventType_authModesReceived,
		},
		"Emit_event_AuthModeSelected": {
			event:         &EventData_AuthModeSelected{},
			wantEventType: EventType_authModeSelected,
		},
		"Emit_event_IsAuthenticatedRequested": {
			event:         &EventData_IsAuthenticatedRequested{},
			wantEventType: EventType_isAuthenticatedRequested,
		},
		"Emit_event_IsAuthenticatedCancelled": {
			event:         &EventData_IsAuthenticatedCancelled{},
			wantEventType: EventType_isAuthenticatedCancelled,
		},
		"Emit_event_StageChanged": {
			event:         &EventData_StageChanged{},
			wantEventType: EventType_stageChanged,
		},
		"Emit_event_UiLayoutReceived": {
			event:         &EventData_UiLayoutReceived{},
			wantEventType: EventType_uiLayoutReceived,
		},
		"Emit_event_AuthEvent": {
			event:         &EventData_AuthEvent{},
			wantEventType: EventType_authEvent,
		},
		"Emit_event_ReselectAuthMode": {
			event:         &EventData_ReselectAuthMode{},
			wantEventType: EventType_reselectAuthMode,
		},
		"Emit_event_UserSelected": {
			event:         &EventData_UserSelected{},
			wantEventType: EventType_userSelected,
		},
		"Emit_event_StartAuthentication": {
			event:         &EventData_StartAuthentication{},
			wantEventType: EventType_startAuthentication,
		},

		// Error cases
		"Error_on_nil_event": {
			wantConvHandlerNotToBeCalled: true,
			wantError:                    errors.New("no known event type"),
		},
		"Error_on_unexpected_event_type": {
			event:                        &invalidEvent{},
			wantConvHandlerNotToBeCalled: true,
			wantError:                    fmt.Errorf("no known event type %#v", &invalidEvent{}),
		},
		"Error_on_invalid_data": {
			event:        &EventData_ReselectAuthMode{},
			returnedData: []byte("null"),
			wantError:    errors.New("unexpected token null"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Cleanup(pam_test.MaybeDoLeakCheck)

			convFuncCalled := false
			mtx := pam_test.NewModuleTransactionDummy(DataConversationFunc(
				func(data *Data) (*Data, error) {
					convFuncCalled = true
					if tc.returnedData != nil {
						msg, err := newJSONProtoMessage(tc.returnedData)
						require.NoError(t, err)
						defer msg.release()
						json, err := msg.JSON()
						require.NoError(t, err)
						return NewDataFromJSON(json)
					}

					require.Equal(t, data.Type, DataType_event)
					require.Equal(t, data.Event.Type, tc.wantEventType)
					return &Data{Type: DataType_eventAck}, nil
				}))

			err := EmitEvent(mtx, tc.event)
			require.Equal(t, convFuncCalled, !tc.wantConvHandlerNotToBeCalled)

			if tc.wantError != nil {
				require.ErrorContains(t, err, tc.wantError.Error())
				return
			}
			require.NoError(t, err)
		})
	}
}
