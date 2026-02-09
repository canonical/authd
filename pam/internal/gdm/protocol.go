//go:generate ../../../tools/generate-proto.sh -I../../../internal/proto/authd -I../proto gdm.proto

// Package gdm is the package for the GDM pam module handing.
package gdm

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/canonical/authd/internal/proto/authd"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// ProtoVersion is the version of the JSON protocol.
	ProtoVersion = uint32(1)
)

// Request is an interface implementing all the gdm requests.
type Request = isRequestData_Data

// Response is an interface implementing all the gdm responses.
type Response = isResponseData_Data

// Event is an interface implementing all the gdm events.
type Event = isEventData_Data

// NewDataFromJSON unmarshals data from json bytes.
func NewDataFromJSON(bytes []byte) (*Data, error) {
	var gdmData Data
	if err := protojson.Unmarshal(bytes, &gdmData); err != nil {
		return nil, err
	}

	if err := gdmData.Check(); err != nil {
		return nil, err
	}

	return &gdmData, nil
}

func checkMembersDebug(d *Data, acceptedMembers []string) error {
	//nolint:govet //We only redirect the value to figure out its type.
	val := reflect.ValueOf(*d)
	typ := val.Type()
	acceptedMembers = append(acceptedMembers, []string{
		"Type", "state", "sizeCache", "unknownFields",
	}...)

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		if slices.Contains(acceptedMembers, fieldType.Name) {
			continue
		}

		if !field.IsZero() {
			return fmt.Errorf("field %v should not be defined", fieldType.Name)
		}
	}

	return nil
}

func checkMembersDisabled(d *Data, acceptedMembers []string) error {
	return nil
}

var checkMembersFunc = checkMembersDisabled

// Check allows to check the sanity of a data value.
func (d *Data) Check() error {
	switch d.Type {
	case DataType_unknownType:
		return fmt.Errorf("unexpected type %v", d.Type.String())

	case DataType_hello:
		if err := checkMembersFunc(d, []string{"Hello"}); err != nil {
			return err
		}

	case DataType_event:
		if d.Event == nil {
			return errors.New("missing event data")
		}
		if d.Event.Type == EventType_unknownEvent {
			return errors.New("missing event type")
		}
		if _, ok := EventType_name[int32(d.Event.Type)]; !ok {
			return fmt.Errorf("unexpected event type %v", d.Event.Type)
		}
		if d.Event.Data == nil {
			return errors.New("missing event data")
		}
		if err := checkMembersFunc(d, []string{"Event"}); err != nil {
			return err
		}

	case DataType_eventAck:
		if err := checkMembersFunc(d, []string{}); err != nil {
			return err
		}

	case DataType_request:
		if d.Request == nil {
			return errors.New("missing request data")
		}
		if d.Request.Type == RequestType_unknownRequest {
			return errors.New("missing request type")
		}
		if _, ok := RequestType_name[int32(d.Request.Type)]; !ok {
			return fmt.Errorf("unexpected request type %v", d.Request.Type)
		}
		if err := checkMembersFunc(d, []string{"Request"}); err != nil {
			return err
		}

	case DataType_response:
		if d.Response == nil {
			return errors.New("missing response data")
		}
		if d.Response.Type == RequestType_unknownRequest {
			return errors.New("missing response type")
		}
		if _, ok := RequestType_name[int32(d.Response.Type)]; !ok {
			return fmt.Errorf("unexpected request type %v", d.Response.Type)
		}
		if err := checkMembersFunc(d, []string{"Response"}); err != nil {
			return err
		}

	case DataType_poll:
		if err := checkMembersFunc(d, []string{}); err != nil {
			return err
		}

	case DataType_pollResponse:
		if err := checkMembersFunc(d, []string{"PollResponse"}); err != nil {
			return err
		}
		for i, response := range d.PollResponse {
			data := &Data{Type: DataType_event, Event: response}
			if err := data.Check(); err != nil {
				return fmt.Errorf("poll response data member %v invalid: %v", i, err)
			}
		}

	default:
		return fmt.Errorf("unhandled type %v", d.Type)
	}

	return nil
}

// JSON returns the data object serialized as JSON bytes.
func (d *Data) JSON() ([]byte, error) {
	bytes, err := protojson.Marshal(d)
	if err != nil {
		return nil, err
	}

	if err = d.Check(); err != nil {
		return nil, err
	}

	return bytes, err
}

var stringifyEventDataFunc = stringifyEventDataFiltered

func stringifyEventDataDebug(ed *EventData) string {
	return ed.String()
}

func stringifyEventDataFiltered(ed *EventData) string {
	authReq, ok := ed.GetData().(*EventData_IsAuthenticatedRequested)
	if !ok {
		return ed.String()
	}

	authData := authReq.IsAuthenticatedRequested.GetAuthenticationData()
	if authData == nil {
		return ed.String()
	}
	if _, ok = authData.Item.(*authd.IARequest_AuthenticationData_Secret); !ok {
		return ed.String()
	}

	return (&EventData{
		Type: ed.Type,
		Data: &EventData_IsAuthenticatedRequested{
			IsAuthenticatedRequested: &Events_IsAuthenticatedRequested{
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: "**************",
					},
				},
			},
		},
	}).String()
}

// SafeString creates a string of EventData with confidential content removed.
func (ed *EventData) SafeString() string {
	return stringifyEventDataFunc(ed)
}
