package gdm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync/atomic"

	"github.com/canonical/authd/log"
	"github.com/msteinert/pam/v2"
)

var conversations atomic.Int32
var secretRegex = regexp.MustCompile(`"secret"\s*:\s*"(?:[^"\\]|\\.)*"`)

// ConversationInProgress checks if conversations are currently active.
func ConversationInProgress() bool {
	return conversations.Load() > 0
}

func sendToGdm(pamMTx pam.ModuleTransaction, data []byte) ([]byte, error) {
	conversations.Add(1)
	defer conversations.Add(-1)
	binReq, err := NewBinaryJSONProtoRequest(data)
	if err != nil {
		return nil, err
	}
	defer binReq.Release()
	res, err := pamMTx.StartConv(binReq)
	if err != nil {
		return nil, err
	}

	binRes, ok := res.(pam.BinaryConvResponse)
	if !ok {
		return nil, errors.New("returned value is not in binary form")
	}
	defer binRes.Release()
	return binRes.Decode(decodeJSONProtoMessage)
}

// sendData sends the data to the PAM Module, returning the JSON data.
func sendData(pamMTx pam.ModuleTransaction, d *Data) ([]byte, error) {
	bytes, err := d.JSON()
	if err != nil {
		return nil, err
	}

	// Log unless it's a poll, which are so frequently that it would be
	// too verbose to log them.
	if d.Type != DataType_poll {
		log.Debugf(context.TODO(), "Sending to GDM: %s", bytes)
	}
	return sendToGdm(pamMTx, bytes)
}

// SendData sends the data to the PAM Module and returns the parsed Data.
func SendData(pamMTx pam.ModuleTransaction, d *Data) (*Data, error) {
	jsonValue, err := sendData(pamMTx, d)
	if err != nil {
		return nil, err
	}

	gdmData, err := NewDataFromJSON(jsonValue)
	if err != nil {
		// TODO: Improve handling of these error messages in gdm model itself.
		log.Warningf(context.TODO(), "Failed parsing GDM result %q: %v", jsonValue, err)
		return nil, fmt.Errorf("failed parsing GDM result: %w", err)
	}

	// Log unless it's an empty poll, which are so frequently that it would be
	// too verbose to log them.
	if gdmData.Type == DataType_pollResponse && len(gdmData.GetPollResponse()) == 0 {
		jsonValue = nil
	}
	if log.IsLevelEnabled(log.DebugLevel) && jsonValue != nil &&
		gdmData.Type == DataType_pollResponse {
		maskedValue := []byte(`"secret":"**************"`)
		jsonValue = secretRegex.ReplaceAllLiteral(jsonValue, maskedValue)
	}
	if jsonValue != nil {
		log.Debugf(context.TODO(), "Got from GDM: %s", jsonValue)
	}
	return gdmData, nil
}

// SendPoll sends a PollEvent to Gdm.
func SendPoll(pamMTx pam.ModuleTransaction) ([]*EventData, error) {
	gdmData, err := SendData(pamMTx, &Data{Type: DataType_poll})
	if err != nil {
		return nil, err
	}

	if gdmData.Type != DataType_pollResponse {
		return nil, fmt.Errorf("gdm replied with an unexpected type: %v",
			gdmData.Type.String())
	}
	return gdmData.GetPollResponse(), nil
}

// SendRequest sends a Request to Gdm.
func SendRequest(pamMTx pam.ModuleTransaction, req Request) (Response, error) {
	var reqType RequestType
	switch req.(type) {
	case *RequestData_UiLayoutCapabilities:
		reqType = RequestType_uiLayoutCapabilities
	case *RequestData_ChangeStage:
		reqType = RequestType_changeStage
	default:
		return nil, fmt.Errorf("no known request type %#v", req)
	}

	gdmData, err := SendData(pamMTx, &Data{
		Type:    DataType_request,
		Request: &RequestData{Type: reqType, Data: req},
	})
	if err != nil {
		return nil, err
	}

	if gdmData.Type != DataType_response {
		return nil, fmt.Errorf("gdm replied with an unexpected type: %v",
			gdmData.Type.String())
	}
	if gdmData.Response == nil {
		return nil, errors.New("gdm replied with no response")
	}
	if gdmData.Response.Type != reqType {
		return nil, fmt.Errorf("gdm replied with invalid response type: %v for %v request",
			gdmData.Response.Type, reqType)
	}
	return gdmData.Response.GetData(), nil
}

// SendRequestTyped allows to parse an object value into a parsed structure.
func SendRequestTyped[T Response](pamMTx pam.ModuleTransaction, req Request) (T, error) {
	res, err := SendRequest(pamMTx, req)
	if err != nil {
		return *new(T), err
	}

	switch r := res.(type) {
	case T:
		return r, nil
	case nil:
		return *new(T), nil
	}

	return *new(T), fmt.Errorf("impossible to convert %#v", res)
}

// EmitEvent sends an Event to Gdm.
func EmitEvent(pamMTx pam.ModuleTransaction, event Event) error {
	var evType EventType
	switch event.(type) {
	case *EventData_BrokersReceived:
		evType = EventType_brokersReceived
	case *EventData_BrokerSelected:
		evType = EventType_brokerSelected
	case *EventData_AuthModesReceived:
		evType = EventType_authModesReceived
	case *EventData_AuthModeSelected:
		evType = EventType_authModeSelected
	case *EventData_IsAuthenticatedRequested:
		evType = EventType_isAuthenticatedRequested
	case *EventData_IsAuthenticatedCancelled:
		evType = EventType_isAuthenticatedCancelled
	case *EventData_StageChanged:
		evType = EventType_stageChanged
	case *EventData_UiLayoutReceived:
		evType = EventType_uiLayoutReceived
	case *EventData_AuthEvent:
		evType = EventType_authEvent
	case *EventData_ReselectAuthMode:
		evType = EventType_reselectAuthMode
	case *EventData_UserSelected:
		evType = EventType_userSelected
	case *EventData_StartAuthentication:
		evType = EventType_startAuthentication
	default:
		return fmt.Errorf("no known event type %#v", event)
	}

	// We don't mind checking the result content, we only care it being well formatted.
	_, err := SendData(pamMTx, &Data{
		Type:  DataType_event,
		Event: &EventData{Type: evType, Data: event},
	})

	if err != nil {
		return err
	}

	return nil
}

// DataConversationFunc is an adapter to allow the use of ordinary
// functions as gdm conversation callbacks.
type DataConversationFunc func(*Data) (*Data, error)

// RespondPAMBinary is a conversation callback adapter.
func (f DataConversationFunc) RespondPAMBinary(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
	json, err := decodeJSONProtoMessage(ptr)
	if err != nil {
		return nil, err
	}
	gdmData, err := NewDataFromJSON(json)
	if err != nil {
		return nil, err
	}
	retData, err := f(gdmData)
	if err != nil {
		return nil, err
	}
	json, err = retData.JSON()
	if err != nil {
		return nil, err
	}
	msg, err := newJSONProtoMessage(json)
	if err != nil {
		return nil, err
	}
	return pam.BinaryPointer(msg), nil
}

// RespondPAM is a dummy conversation callback adapter to implement pam.BinaryPointerConversationFunc.
func (f DataConversationFunc) RespondPAM(pam.Style, string) (string, error) {
	return "", pam.ErrConv
}
