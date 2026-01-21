package main_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/gdm_test"
	"github.com/canonical/authd/pam/internal/proto"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

type gdmTestModuleHandler struct {
	t  *testing.T
	tx *pam.Transaction

	protoVersion uint32

	supportedLayouts  []*authd.UILayout
	currentUILayout   *authd.UILayout
	selectedUILayouts []*authd.UILayout
	authResponses     []*authd.IAResponse

	currentStage  proto.Stage
	pollResponses []*gdm.EventData

	authModes           []*authd.GAMResponse_AuthenticationMode
	authModeID          string
	selectedAuthModeIDs []string

	brokersInfos       []*authd.ABResponse_BrokerInfo
	selectedBrokerName string
	selectedBrokerID   string
	brokerInfo         *authd.ABResponse_BrokerInfo

	eventPollResponses map[gdm.EventType][]*gdm.EventData
	stagePollResponses map[proto.Stage][]*gdm.EventData

	pamInfoMessages  []string
	pamErrorMessages []string
}

func (gh *gdmTestModuleHandler) exampleHandleGdmData(gdmData *gdm.Data) (*gdm.Data, error) {
	switch gdmData.Type {
	case gdm.DataType_hello:
		return &gdm.Data{
			Type:  gdm.DataType_hello,
			Hello: &gdm.HelloData{Version: gh.protoVersion},
		}, nil

	case gdm.DataType_request:
		return gh.exampleHandleAuthDRequest(gdmData)

	case gdm.DataType_poll:
		responses := gh.pollResponses
		gh.pollResponses = nil
		return &gdm.Data{
			Type:         gdm.DataType_pollResponse,
			PollResponse: responses,
		}, nil

	case gdm.DataType_event:
		err := gh.exampleHandleEvent(gdmData.Event)
		if err != nil {
			return nil, err
		}
		return &gdm.Data{
			Type: gdm.DataType_eventAck,
		}, nil
	}

	return nil, fmt.Errorf("unhandled protocol message %s",
		gdmData.Type.String())
}

func (gh gdmTestModuleHandler) tunePollEvents(events []*gdm.EventData) []*gdm.EventData {
	var sanitized []*gdm.EventData
	for _, e := range events {
		switch ev := e.Data.(type) {
		case *gdm.EventData_BrokerSelected:
			idx := slices.IndexFunc(gh.brokersInfos, func(broker *authd.ABResponse_BrokerInfo) bool {
				return broker.Id == ev.BrokerSelected.BrokerId || broker.Name == ev.BrokerSelected.BrokerId
			})
			require.GreaterOrEqual(gh.t, idx, 0, "Unknown broker: %s", ev.BrokerSelected.BrokerId)
			sanitized = append(sanitized, gdm_test.SelectBrokerEvent(gh.brokersInfos[idx].Id))
			continue
		}
		sanitized = append(sanitized, e)
	}
	return sanitized
}

func (gh *gdmTestModuleHandler) queueSelectBrokerEvent() error {
	if gh.currentStage != proto.Stage_brokerSelection {
		return nil
	}
	if gh.selectedBrokerName == ignoredBrokerName {
		return nil
	}
	if gh.selectedBrokerID != "" {
		return nil
	}

	idx := slices.IndexFunc(gh.brokersInfos, func(bi *authd.ABResponse_BrokerInfo) bool {
		return bi.Name == gh.selectedBrokerName
	})
	if idx < 0 {
		return fmt.Errorf("broker %q is not known", gh.selectedBrokerName)
	}

	gh.selectedBrokerID = gh.brokersInfos[idx].Id
	gh.pollResponses = append(gh.pollResponses,
		gdm_test.SelectBrokerEvent(gh.selectedBrokerID))
	return nil
}

func (gh *gdmTestModuleHandler) getPollResponses(events []*gdm.EventData) []*gdm.EventData {
	if len(events) == 0 {
		return nil
	}

	numEvents := 1
	if events[0].Type == gdm_test.EventsGroupBegin().Type {
		numEvents = slices.IndexFunc(events, func(ev *gdm.EventData) bool {
			return ev.Type == gdm_test.EventsGroupEnd().Type
		})
		require.Greater(gh.t, numEvents, 1, "No valid events group found")
		events = slices.Delete(events, numEvents, numEvents+1)
		events = slices.Delete(events, 0, 1)
		numEvents--
	}

	require.NotEqual(gh.t, events[0].Type, gdm_test.EventsGroupBegin().Type,
		"Unsupported event type")
	require.NotEqual(gh.t, events[0].Type, gdm_test.EventsGroupEnd().Type,
		"Unsupported event type")

	var pollEvents []*gdm.EventData
	if events[0].Type != gdm_test.IgnoredEvent().Type {
		pollEvents = gh.tunePollEvents(events[0:numEvents])
	}
	events = slices.Delete(events, 0, numEvents)
	gh.pollResponses = append(gh.pollResponses, pollEvents...)
	return events
}

func (gh *gdmTestModuleHandler) exampleHandleEvent(event *gdm.EventData) error {
	defer func() {
		events, ok := gh.eventPollResponses[event.Type]
		if !ok {
			return
		}
		gh.eventPollResponses[event.Type] = gh.getPollResponses(events)
	}()

	switch ev := event.Data.(type) {
	case *gdm.EventData_BrokersReceived:
		if len(ev.BrokersReceived.BrokersInfos) == 0 {
			return errors.New("no brokers available")
		}
		gh.brokersInfos = ev.BrokersReceived.BrokersInfos

		err := gh.queueSelectBrokerEvent()
		if err != nil {
			return err
		}

	case *gdm.EventData_BrokerSelected:
		idx := slices.IndexFunc(gh.brokersInfos, func(broker *authd.ABResponse_BrokerInfo) bool {
			return broker.Id == ev.BrokerSelected.BrokerId
		})
		if idx < 0 {
			return fmt.Errorf("unknown broker: %s", ev.BrokerSelected.BrokerId)
		}
		gh.brokerInfo = gh.brokersInfos[idx]
		gh.t.Logf("Using broker '%s'", gh.brokerInfo.Name)
		require.Equal(gh.t, gh.selectedBrokerName, gh.brokerInfo.Name,
			"Selected broker name does not match expected one")

	case *gdm.EventData_AuthModesReceived:
		gh.authModes = ev.AuthModesReceived.AuthModes

	case *gdm.EventData_AuthModeSelected:
		gh.authModeID = ev.AuthModeSelected.AuthModeId

	case *gdm.EventData_UiLayoutReceived:
		layout := ev.UiLayoutReceived.UiLayout
		if layout.Label != nil {
			gh.t.Logf("%s:", *layout.Label)
		}
		if layout.Content != nil {
			gh.t.Logf("%s:", *layout.Content)
		}

		gh.currentUILayout = layout

	case *gdm.EventData_StartAuthentication:
		idx := slices.IndexFunc(gh.authModes, func(mode *authd.GAMResponse_AuthenticationMode) bool {
			return mode.Id == gh.authModeID
		})
		if idx < 0 {
			return fmt.Errorf("unknown auth mode type: %q", gh.authModeID)
		}
		if len(gh.selectedAuthModeIDs) < 1 {
			return fmt.Errorf("unexpected authentication started with mode '%s', we've nothing to reply",
				gh.authModeID)
		}
		require.Equal(gh.t, gh.selectedAuthModeIDs[0], gh.authModes[idx].Id,
			"Selected authentication mode ID does not match expected one")
		gh.selectedAuthModeIDs = slices.Delete(gh.selectedAuthModeIDs, 0, 1)

		if len(gh.selectedUILayouts) < 1 {
			// TODO: Make this an error but we don't support checking the layout in all tests yet.
			return nil
		}

		gdm_test.RequireEqualData(gh.t, gh.selectedUILayouts[0], gh.currentUILayout,
			"Selected UI layout does not match expected one")
		gh.selectedUILayouts = slices.Delete(gh.selectedUILayouts, 0, 1)

	case *gdm.EventData_AuthEvent:
		gh.t.Logf("Authentication event: %s", ev.AuthEvent.Response)
		if msg := ev.AuthEvent.Response.Msg; msg != "" {
			gh.t.Logf("Got message: %s", msg)
		}
		gh.authResponses = append(gh.authResponses, ev.AuthEvent.Response)
	}
	return nil
}

func (gh *gdmTestModuleHandler) exampleHandleAuthDRequest(gdmData *gdm.Data) (*gdm.Data, error) {
	switch req := gdmData.Request.Data.(type) {
	case *gdm.RequestData_UiLayoutCapabilities:
		return &gdm.Data{
			Type: gdm.DataType_response,
			Response: &gdm.ResponseData{
				Type: gdmData.Request.Type,
				Data: &gdm.ResponseData_UiLayoutCapabilities{
					UiLayoutCapabilities: &gdm.Responses_UiLayoutCapabilities{
						SupportedUiLayouts: gh.supportedLayouts,
					},
				},
			},
		}, nil

	case *gdm.RequestData_ChangeStage:
		if gdmData.Request.Data == nil {
			return nil, errors.New("missing stage data")
		}
		gh.currentStage = req.ChangeStage.Stage
		log.Debugf(context.TODO(), "Switching to stage %d", gh.currentStage)

		defer func() {
			events, ok := gh.stagePollResponses[gh.currentStage]
			if !ok {
				return
			}
			gh.stagePollResponses[gh.currentStage] = gh.getPollResponses(events)
		}()

		switch req.ChangeStage.Stage {
		case proto.Stage_brokerSelection:
			gh.authModes = nil
			gh.brokerInfo = nil
			gh.currentUILayout = nil

			err := gh.queueSelectBrokerEvent()
			if err != nil && len(gh.brokersInfos) > 0 {
				return nil, err
			}

		case proto.Stage_authModeSelection:
			gh.currentUILayout = nil
		}

		return &gdm.Data{
			Type: gdm.DataType_response,
			Response: &gdm.ResponseData{
				Type: gdmData.Request.Type,
				Data: &gdm.ResponseData_Ack{},
			},
		}, nil

	default:
		return nil, errors.New("unknown request type")
	}
}

// RespondPAMBinary is a dummy conversation callback adapter to implement [pam.BinaryPointerConversationFunc].
func (gh *gdmTestModuleHandler) RespondPAMBinary(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
	return gdm.DataConversationFunc(func(inData *gdm.Data) (*gdm.Data, error) {
		outData, err := gh.exampleHandleGdmData(inData)
		if err != nil {
			json, jsonErr := inData.JSON()
			require.NoError(gh.t, jsonErr, "Binary conversation: Invalid JSON received as input data")
			gh.t.Log("->", string(json))
			gh.t.Logf("Binary conversation: Error handling data: %v", err)
			return nil, err
		}
		if inData.Type == gdm.DataType_poll && len(outData.PollResponse) == 0 {
			return outData, err
		}
		json, err := inData.JSON()
		require.NoError(gh.t, err, "Binary conversation: Invalid JSON received as input data: %v", inData)
		gh.t.Log("->", string(json))
		json, err = outData.JSON()
		require.NoError(gh.t, err, "Binary conversation: Can't convert output data to JSON: %v", outData)
		gh.t.Log("<-", string(json))
		return outData, nil
	}).RespondPAMBinary(ptr)
}

// RespondPAM is a dummy conversation callback adapter to implement [pam.ConversationFunc].
func (gh *gdmTestModuleHandler) RespondPAM(style pam.Style, prompt string) (string, error) {
	switch style {
	case pam.TextInfo:
		gh.t.Logf("GDM PAM Info Message: %s", prompt)
		gh.pamInfoMessages = append(gh.pamInfoMessages, prompt)
	case pam.ErrorMsg:
		gh.t.Logf("GDM PAM Error Message: %s", prompt)
		gh.pamErrorMessages = append(gh.pamInfoMessages, prompt)
	default:
		return "", fmt.Errorf("PAM style %d not implemented", style)
	}
	return "", nil
}

func newGdmTestModuleHandler(t *testing.T, serviceFile string, userName string) *gdmTestModuleHandler {
	t.Helper()

	gh := &gdmTestModuleHandler{t: t}
	tx, err := pam.StartConfDir(filepath.Base(serviceFile), userName, gh, filepath.Dir(serviceFile))
	require.NoError(t, err, "PAM: Error to initialize module")
	require.NotNil(t, tx, "PAM: Transaction is not set")

	gh.tx = tx

	return gh
}
