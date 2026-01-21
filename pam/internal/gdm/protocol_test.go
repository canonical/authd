package gdm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/stretchr/testify/require"
)

func reformatJSON(t *testing.T, input []byte) []byte {
	t.Helper()

	// We can't safely compare for JSON values when generated via protobuf
	// so we initially pass it to native implementation to make it rebuild
	// the JSON data so that the output is more reliable.
	// See: https://protobuf.dev/reference/go/faq/#unstable-json
	var js json.RawMessage
	err := json.Unmarshal(input, &js)
	require.NoError(t, err)
	out, err := json.Marshal(js)
	require.NoError(t, err)
	return out
}

func reformatJSONIndented(t *testing.T, input []byte) []byte {
	t.Helper()

	var indented bytes.Buffer
	err := json.Indent(&indented, input, "", "  ")
	require.NoError(t, err)
	return indented.Bytes()
}

func requireEqualData(t *testing.T, want *gdm.Data, actual *gdm.Data) {
	t.Helper()

	wantJSON, err := want.JSON()
	require.NoError(t, err)
	actualJSON, err := actual.JSON()
	require.NoError(t, err)

	require.Equal(t, string(reformatJSONIndented(t, wantJSON)),
		string(reformatJSONIndented(t, actualJSON)))
}

func TestGdmStructsMarshal(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		gdmData *gdm.Data

		wantJSON   string
		wantErrMsg string
	}{
		"Hello_packet": {
			gdmData: &gdm.Data{Type: gdm.DataType_hello},

			wantJSON: `{"type":"hello"}`,
		},
		"Hello_packet_with_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_hello, Hello: &gdm.HelloData{Version: 55}},

			wantJSON: `{"type":"hello","hello":{"version":55}}`,
		},
		"Event_packet": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_event,
				Event: &gdm.EventData{
					Type: gdm.EventType_brokerSelected,
					Data: &gdm.EventData_BrokerSelected{},
				},
			},

			wantJSON: `{"type":"event","event":{"type":"brokerSelected","brokerSelected":{}}}`,
		},
		"Event_ack_packet": {
			gdmData: &gdm.Data{Type: gdm.DataType_eventAck},

			wantJSON: `{"type":"eventAck"}`,
		},
		"Request_packet": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_request,
				Request: &gdm.RequestData{
					Type: gdm.RequestType_uiLayoutCapabilities,
					Data: &gdm.RequestData_UiLayoutCapabilities{},
				},
			},

			wantJSON: `{"type":"request","request":{"type":"uiLayoutCapabilities","uiLayoutCapabilities":{}}}`,
		},
		"Request_packet_with_missing_data": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_request,
				Request: &gdm.RequestData{
					Type: gdm.RequestType_updateBrokersList,
				},
			},

			wantJSON: `{"type":"request","request":{"type":"updateBrokersList"}}`,
		},
		"Response_packet": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_response,
				Response: &gdm.ResponseData{
					Type: gdm.RequestType_uiLayoutCapabilities,
					Data: &gdm.ResponseData_UiLayoutCapabilities{},
				},
			},

			wantJSON: `{"type":"response","response":{"type":"uiLayoutCapabilities","uiLayoutCapabilities":{}}}`,
		},
		"Response_packet_with_ack_data": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_response,
				Response: &gdm.ResponseData{
					Type: gdm.RequestType_changeStage,
					Data: &gdm.ResponseData_Ack{},
				},
			},

			wantJSON: `{"type":"response","response":{"type":"changeStage","ack":{}}}`,
		},
		"Poll_packet": {
			gdmData: &gdm.Data{Type: gdm.DataType_poll},

			wantJSON: `{"type":"poll"}`,
		},
		"PollResponse_packet": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{
					{
						Type: gdm.EventType_brokerSelected,
						Data: &gdm.EventData_BrokerSelected{
							BrokerSelected: &gdm.Events_BrokerSelected{BrokerId: "a broker"},
						},
					},
				},
			},

			wantJSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}}]}`,
		},
		"PollResponse_packet_with_multiple_results": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{
					{
						Type: gdm.EventType_brokerSelected,
						Data: &gdm.EventData_BrokerSelected{
							BrokerSelected: &gdm.Events_BrokerSelected{BrokerId: "a broker"},
						},
					},
					{
						Type: gdm.EventType_authModeSelected,
						Data: &gdm.EventData_AuthModeSelected{
							AuthModeSelected: &gdm.Events_AuthModeSelected{AuthModeId: "auth mode"},
						},
					},
				},
			},

			wantJSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}},` +
				`{"type":"authModeSelected","authModeSelected":{"authModeId":"auth mode"}}]}`,
		},
		"PollResponse_packet_with_nil_data": {
			gdmData: &gdm.Data{
				Type:         gdm.DataType_pollResponse,
				PollResponse: nil,
			},

			wantJSON: `{"type":"pollResponse"}`,
		},
		"PollResponse_packet_with_empty_data": {
			gdmData: &gdm.Data{
				Type:         gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{},
			},

			wantJSON: `{"type":"pollResponse"}`,
		},

		// Error cases
		"Error_empty_packet": {
			gdmData: &gdm.Data{},

			wantErrMsg: "unexpected type unknownType",
		},
		"Error_if_packet_has_invalid_type": {
			gdmData: &gdm.Data{Type: gdm.DataType(-1)},

			wantErrMsg: "unhandled type -1",
		},
		"Error_hello_packet_with_unexpected_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_hello, Request: &gdm.RequestData{}},

			wantErrMsg: "field Request should not be defined",
		},
		"Error_event_packet_with_unknown_type": {
			gdmData: &gdm.Data{
				Type:  gdm.DataType_event,
				Event: &gdm.EventData{Type: gdm.EventType_unknownEvent},
			},

			wantErrMsg: "missing event type",
		},
		"Error_event_packet_with_invalid_type": {
			gdmData: &gdm.Data{Type: gdm.DataType_event, Event: &gdm.EventData{Type: gdm.EventType(-1)}},

			wantErrMsg: "unexpected event type",
		},
		"Error_event_packet_with_missing_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_event, Event: nil},

			wantErrMsg: "missing event data",
		},
		"Error_event_packet_with_empty_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_event, Event: &gdm.EventData{}},

			wantErrMsg: "missing event type",
		},
		"Error_event_packet_with_missing_type": {
			gdmData: &gdm.Data{Type: gdm.DataType_event, Event: &gdm.EventData{Data: &gdm.EventData_AuthModeSelected{}}},

			wantErrMsg: "missing event type",
		},
		"Error_event_packet_with_unexpected_data": {
			gdmData: &gdm.Data{
				Type:  gdm.DataType_event,
				Event: &gdm.EventData{Type: gdm.EventType_authEvent, Data: &gdm.EventData_AuthModeSelected{}},
				Hello: &gdm.HelloData{},
			},

			wantErrMsg: "field Hello should not be defined",
		},
		"Error_event_ack_packet_with_unexpected_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_eventAck, Event: &gdm.EventData{}},

			wantErrMsg: "field Event should not be defined",
		},
		"Error_request_packet_with_unknown_type": {
			gdmData: &gdm.Data{Type: gdm.DataType_request, Request: &gdm.RequestData{Data: &gdm.RequestData_ChangeStage{}}},

			wantErrMsg: "missing request type",
		},
		"Error_request_packet_with_invalid_type": {
			gdmData: &gdm.Data{Type: gdm.DataType_request, Request: &gdm.RequestData{Type: gdm.RequestType(-1)}},

			wantErrMsg: "unexpected request type",
		},
		"Error_request_packet_with_missing_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_request, Request: nil},

			wantErrMsg: "missing request data",
		},
		"Error_request_packet_with_empty_data": {
			gdmData:    &gdm.Data{Type: gdm.DataType_request, Request: &gdm.RequestData{}},
			wantErrMsg: "missing request type",
		},
		"Error_request_packet_with_unexpected_data": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_request,
				Request: &gdm.RequestData{
					Type: gdm.RequestType_changeStage,
					Data: &gdm.RequestData_ChangeStage{},
				},
				Event: &gdm.EventData{},
			},

			wantErrMsg: "field Event should not be defined",
		},
		"Error_response_packet_with_missing_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_response},

			wantErrMsg: "missing response data",
		},
		"Error_response_packet_with_missing_type": {
			gdmData: &gdm.Data{
				Type:     gdm.DataType_response,
				Response: &gdm.ResponseData{Data: &gdm.ResponseData_Ack{}},
			},

			wantErrMsg: "missing response type",
		},
		"Error_response_packet_with_invalid_type": {
			gdmData: &gdm.Data{
				Type:     gdm.DataType_response,
				Response: &gdm.ResponseData{Type: gdm.RequestType(-1), Data: &gdm.ResponseData_Ack{}},
			},

			wantErrMsg: "unexpected request type -1",
		},
		"Error_response_packet_with_unexpected_data": {
			gdmData: &gdm.Data{
				Type:     gdm.DataType_response,
				Response: &gdm.ResponseData{Type: gdm.RequestType_changeStage, Data: &gdm.ResponseData_Ack{}},
				Event:    &gdm.EventData{},
			},

			wantErrMsg: "field Event should not be defined",
		},
		"Error_poll_packet_with_unexpected_data": {
			gdmData: &gdm.Data{Type: gdm.DataType_poll, Request: &gdm.RequestData{}},

			wantErrMsg: "field Request should not be defined",
		},
		"Error_pollResponse_packet_with_missing_event_type": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{
					{
						Type: gdm.EventType_brokerSelected,
						Data: &gdm.EventData_BrokerSelected{
							BrokerSelected: &gdm.Events_BrokerSelected{BrokerId: "a broker"},
						},
					},
					{
						Data: &gdm.EventData_AuthModeSelected{
							AuthModeSelected: &gdm.Events_AuthModeSelected{AuthModeId: "auth mode"},
						},
					},
				},
			},

			wantErrMsg: "poll response data member 1 invalid: missing event type",
		},
		"Error_pollResponse_packet_with_event_with_missing_type": {
			gdmData: &gdm.Data{
				Type: gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{
					{},
					{
						Type: gdm.EventType_authModeSelected,
						Data: &gdm.EventData_AuthModeSelected{
							AuthModeSelected: &gdm.Events_AuthModeSelected{AuthModeId: "auth mode"},
						},
					},
				},
			},

			wantErrMsg: "poll response data member 0 invalid: missing event type",
		},
		"Error_pollResponse_packet_with_unexpected_data": {
			gdmData: &gdm.Data{
				Type:         gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{},
				Event:        &gdm.EventData{},
			},

			wantErrMsg: "field Event should not be defined",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bytes, err := tc.gdmData.JSON()
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)

			formattedJSON := string(reformatJSON(t, bytes))
			require.Equal(t, tc.wantJSON, formattedJSON)

			// Now try to reconvert things back again
			gdmData, err := gdm.NewDataFromJSON(bytes)
			require.NoError(t, err)
			requireEqualData(t, tc.gdmData, gdmData)
		})
	}
}

func TestGdmStructsUnMarshal(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		JSON string

		wantData   *gdm.Data
		wantErrMsg string
	}{
		"hello_packet": {
			JSON: `{"type":"hello"}`,

			wantData: &gdm.Data{Type: gdm.DataType_hello},
		},
		"Hello_packet_with_data": {
			JSON: `{"type":"hello","hello":{"version":55}}`,

			wantData: &gdm.Data{Type: gdm.DataType_hello, Hello: &gdm.HelloData{Version: 55}},
		},
		"Event_packet": {
			JSON: `{"type":"event","event":{"type":"brokerSelected","brokerSelected":{}}}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_event,
				Event: &gdm.EventData{
					Type: gdm.EventType_brokerSelected,
					Data: &gdm.EventData_BrokerSelected{},
				},
			},
		},
		"Event_ack_packet": {
			JSON: `{"type":"eventAck"}`,

			wantData: &gdm.Data{Type: gdm.DataType_eventAck},
		},
		"Request_packet": {
			JSON: `{"type":"request","request":{"type":"uiLayoutCapabilities","uiLayoutCapabilities":{}}}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_request,
				Request: &gdm.RequestData{
					Type: gdm.RequestType_uiLayoutCapabilities,
					Data: &gdm.RequestData_UiLayoutCapabilities{},
				},
			},
		},
		"Request_packet_with_missing_data": {
			JSON: `{"type":"request","request":{"type":"updateBrokersList"}}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_request,
				Request: &gdm.RequestData{
					Type: gdm.RequestType_updateBrokersList,
				},
			},
		},
		"Response_packet": {
			JSON: `{"type":"response","response":{"type":"uiLayoutCapabilities","uiLayoutCapabilities":{}}}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_response,
				Response: &gdm.ResponseData{
					Type: gdm.RequestType_uiLayoutCapabilities,
					Data: &gdm.ResponseData_UiLayoutCapabilities{},
				},
			},
		},
		"Response_packet_with_ack_data": {
			JSON: `{"type":"response","response":{"type":"changeStage","ack":{}}}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_response,
				Response: &gdm.ResponseData{
					Type: gdm.RequestType_changeStage,
					Data: &gdm.ResponseData_Ack{},
				},
			},
		},
		"Poll_packet": {
			JSON: `{"type":"poll"}`,

			wantData: &gdm.Data{Type: gdm.DataType_poll},
		},
		"PollResponse_packet": {
			JSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}}]}`,

			wantData: &gdm.Data{
				Type: gdm.DataType_pollResponse,
				PollResponse: []*gdm.EventData{
					{
						Type: gdm.EventType_brokerSelected,
						Data: &gdm.EventData_BrokerSelected{
							BrokerSelected: &gdm.Events_BrokerSelected{BrokerId: "a broker"},
						},
					},
				},
			},
		},
		"PollResponse_packet_with_missing_data": {
			JSON: `{"type":"pollResponse"}`,

			wantData: &gdm.Data{
				Type:         gdm.DataType_pollResponse,
				PollResponse: nil,
			},
		},

		// Error cases
		"Error_empty_packet": {
			wantErrMsg: "syntax error",
		},
		"Error_empty_packet_object": {
			JSON: `{}`,

			wantErrMsg: "unexpected type unknownType",
		},
		"Error_packet_with_invalid_type": {
			JSON: `{"type":"invalidType"}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_packet_with_invalid_value_type": {
			JSON: `{"type":[]}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_hello_packet_with_unexpected_data": {
			JSON: `{"type":"hello","request":{}}`,

			wantErrMsg: "field Request should not be defined",
		},
		"Error_event_packet_with_invalid_data": {
			JSON: `{"type":"event","fooEvent":null}`,

			wantErrMsg: `unknown field "fooEvent"`,
		},
		"Error_event_packet_with_missing_type": {
			JSON:       `{"type":"event","event":{}}`,
			wantErrMsg: "missing event type",
		},
		"Error_event_packet_with_unknown_type": {
			JSON: `{"type":"event","event":{"type":"someType"}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_event_packet_with_invalid_value_type": {
			JSON: `{"type":"event","event":{"brokerSelected":{},"type":{}}}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_event_packet_with_missing_data": {
			JSON: `{"type":"event","event":{"type":"brokerSelected"}}`,

			wantErrMsg: "missing event data",
		},
		"Error_event_packet_with_unexpected_data": {
			JSON: `{"type":"event","event":{"type":"brokerSelected",` +
				`"brokerSelected":{}},"request":{}}`,

			wantErrMsg: "field Request should not be defined",
		},
		"Error_event_ack_packet_with_unexpected_member": {
			JSON: `{"type":"eventAck","event":{}}`,

			wantErrMsg: "field Event should not be defined",
		},
		"Error_request_packet_with_missing_type": {
			JSON: `{"type":"request","request":{"uiLayoutCapabilities":{}}}`,

			wantErrMsg: "missing request type",
		},
		"Error_request_packet_with_unknown_type": {
			JSON: `{"type":"request","request":{"type":true,"uiLayoutCapabilities":{}}}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_request_packet_with_unknown_value_type": {
			JSON: `{"type":"request","request":{"type":"someUnknownRequest",` +
				`"uiLayoutCapabilities":{}}}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_request_packet_with_unexpected_data": {
			JSON: `{"type":"request","request":{"type": "uiLayoutCapabilities",` +
				`"uiLayoutCapabilities":{}}, "event":{}}`,

			wantErrMsg: "field Event should not be defined",
		},
		"Error_response_packet_with_missing_data": {
			JSON: `{"type":"response"}`,

			wantErrMsg: "missing response data",
		},
		"Error_response_packet_with_unexpected_data": {
			JSON: `{"type":"response","response":{"type":"changeStage","ack":{}}, "event":{}}`,

			wantErrMsg: "field Event should not be defined",
		},
		"Error_poll_packet_with_unexpected_data": {
			JSON: `{"type":"poll", "response": {}}`,

			wantErrMsg: "field Response should not be defined",
		},
		"Error_pollResponse_packet_with_missing_event_type": {
			JSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}},` +
				`{"authModeSelected":{"authModeId":"auth mode"}}]}`,

			wantErrMsg: "poll response data member 1 invalid: missing event type",
		},
		"Error_pollResponse_packet_with_unsupported_event_type": {
			JSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}},` +
				`{"type":"invalidEvent"}]}`,

			wantErrMsg: "invalid value for enum field type",
		},
		"Error_pollResponse_packet_with_unexpected_data": {
			JSON: `{"type":"pollResponse","pollResponse":` +
				`[{"type":"brokerSelected","brokerSelected":{"brokerId":"a broker"}},` +
				`{"type":"authModeSelected","authModeSelected":{"authModeId":"auth mode"}}],` +
				`"response":{}}`,

			wantErrMsg: "field Response should not be defined",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gdmData, err := gdm.NewDataFromJSON([]byte(tc.JSON))
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)

			requireEqualData(t, tc.wantData, gdmData)

			// Convert back the data to JSON and check it's still matching.
			json, err := gdmData.JSON()
			require.NoError(t, err)
			require.Equal(t, tc.JSON, string(reformatJSON(t, json)))
		})
	}
}

func TestSafeString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		eventData *gdm.EventData

		wantString     string
		wantSafeString string
	}{
		"Empty_gdm_data_is_stringified": {
			eventData: &gdm.EventData{},
		},
		"Non-AuthenticatedRequest_is_fully_stringified": {
			eventData: &gdm.EventData{
				Type: gdm.EventType_authEvent,
				Data: &gdm.EventData_AuthEvent{
					AuthEvent: &gdm.Events_AuthEvent{
						Response: &authd.IAResponse{
							Access: auth.Granted,
							Msg:    "Hello!",
						},
					},
				},
			},
			wantString: `type:authEvent authEvent:{response:{access:"granted" msg:"Hello!"}}`,
		},
		"AuthenticatedRequest_with_wait_is_fully_stringified": {
			eventData: &gdm.EventData{
				Type: gdm.EventType_isAuthenticatedRequested,
				Data: &gdm.EventData_IsAuthenticatedRequested{
					&gdm.Events_IsAuthenticatedRequested{
						AuthenticationData: &authd.IARequest_AuthenticationData{
							Item: &authd.IARequest_AuthenticationData_Wait{
								Wait: "wait-value",
							},
						},
					},
				},
			},
			wantString: `type:isAuthenticatedRequested isAuthenticatedRequested:{authentication_data:{wait:"wait-value"}}`,
		},
		"AuthenticatedRequest_with_skip_is_fully_stringified": {
			eventData: &gdm.EventData{
				Type: gdm.EventType_isAuthenticatedRequested,
				Data: &gdm.EventData_IsAuthenticatedRequested{
					&gdm.Events_IsAuthenticatedRequested{
						AuthenticationData: &authd.IARequest_AuthenticationData{
							Item: &authd.IARequest_AuthenticationData_Skip{
								Skip: "skip-value",
							},
						},
					},
				},
			},
			wantString: `type:isAuthenticatedRequested isAuthenticatedRequested:{authentication_data:{skip:"skip-value"}}`,
		},
		"AuthenticatedRequest_with_nil_data_is_fully_stringified": {
			eventData: &gdm.EventData{
				Type: gdm.EventType_isAuthenticatedRequested,
				Data: &gdm.EventData_IsAuthenticatedRequested{
					IsAuthenticatedRequested: &gdm.Events_IsAuthenticatedRequested{
						AuthenticationData: nil,
					},
				},
			},
			wantString: `type:isAuthenticatedRequested isAuthenticatedRequested:{}`,
		},
		"AuthenticatedRequest_without_secret_is_fully_stringified": {
			eventData: &gdm.EventData{
				Type: gdm.EventType_isAuthenticatedRequested,
				Data: &gdm.EventData_IsAuthenticatedRequested{
					&gdm.Events_IsAuthenticatedRequested{
						AuthenticationData: &authd.IARequest_AuthenticationData{
							Item: &authd.IARequest_AuthenticationData_Secret{
								Secret: "SuperSecretValue!#DON'T SHARE!",
							},
						},
					},
				},
			},
			wantString:     `type:isAuthenticatedRequested isAuthenticatedRequested:{authentication_data:{secret:"SuperSecretValue!#DON'T SHARE!"}}`,
			wantSafeString: `type:isAuthenticatedRequested isAuthenticatedRequested:{authentication_data:{secret:"**************"}}`,
		},
	}

	for name, tc := range tests {
		t.Run(fmt.Sprintf("%s_debug_mode", name), func(t *testing.T) {
			t.Parallel()

			// String method may add extra white spaces at times, let's ignore them.
			safeString := strings.ReplaceAll(tc.eventData.SafeString(), "  ", " ")
			require.Equal(t, tc.wantString, safeString,
				"SaveString result mismatches expected")
		})
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// THIS CANNOT BE PARALLEL!
			gdm.SetDebuggingSafeEventDataFunc(false)
			t.Cleanup(func() { gdm.SetDebuggingSafeEventDataFunc(true) })

			if tc.wantSafeString == "" {
				tc.wantSafeString = tc.wantString
			}

			// String method may add extra white spaces at times, let's ignore them.
			safeString := strings.ReplaceAll(tc.eventData.SafeString(), "  ", " ")
			require.Equal(t, tc.wantSafeString, safeString,
				"SaveString result mismatches expected")
		})
	}
}
