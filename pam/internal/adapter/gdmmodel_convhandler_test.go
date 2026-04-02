package adapter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/proto"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

type gdmConvHandler struct {
	mu           *sync.Mutex
	t            *testing.T
	protoVersion uint32
	convError    map[string]error

	wantRequests        []gdm.RequestType
	handledRequests     []gdm.RequestType
	allRequestsReceived chan struct{}

	receivedEvents    []*gdm.EventData
	wantEvents        []gdm.EventType
	allEventsReceived chan struct{}

	pendingEvents        []*gdm.EventData
	pendingEventsFlushed chan struct{}

	supportedLayouts []*authd.UILayout
	receivedBrokers  []*authd.ABResponse_BrokerInfo
	selectedBrokerID string

	currentStageChanged sync.Cond
	currentStage        proto.Stage
	stageChanges        []proto.Stage
	lastNotifiedStage   *proto.Stage

	startAuthRequested chan struct{}
	authEvents         []*authd.IAResponse
}

func (h *gdmConvHandler) checkAllEventsHaveBeenEmitted() bool {
	receivedEventTypes := []gdm.EventType{}
	for _, e := range h.receivedEvents {
		receivedEventTypes = append(receivedEventTypes, e.Type)
	}

	return isSupersetOf(receivedEventTypes, h.wantEvents)
}

func (h *gdmConvHandler) checkAllRequestsHaveBeenHandled() bool {
	return isSupersetOf(h.handledRequests, h.wantRequests)
}

func (h *gdmConvHandler) RespondPAM(style pam.Style, prompt string) (string, error) {
	switch style {
	case pam.TextInfo:
		h.t.Logf("GDM PAM Info Message: %s", prompt)
	case pam.ErrorMsg:
		h.t.Logf("GDM PAM Error Message: %s", prompt)
	default:
		return "", fmt.Errorf("PAM style %d not implemented", style)
	}
	return "", nil
}

func (h *gdmConvHandler) RespondPAMBinary(ptr pam.BinaryPointer) (pam.BinaryPointer, error) {
	return gdm.DataConversationFunc(func(inData *gdm.Data) (*gdm.Data, error) {
		h.mu.Lock()
		defer h.mu.Unlock()

		var json []byte

		if len(h.convError) > 0 {
			var err error
			json, err = inData.JSON()
			if err != nil {
				return nil, err
			}

			err, ok := h.convError[string(json)]
			if ok {
				return nil, err
			}
		}

		outData, err := h.handleGdmData(inData)
		if err != nil {
			return nil, err
		}
		if inData.Type == gdm.DataType_poll && len(outData.PollResponse) == 0 {
			return outData, err
		}
		if json == nil {
			json, err = inData.JSON()
			if err != nil {
				return nil, err
			}
		}
		h.t.Log("->", string(json))
		json, err = outData.JSON()
		if err != nil {
			return nil, err
		}
		h.t.Log("<-", string(json))
		return outData, nil
	}).RespondPAMBinary(ptr)
}

func (h *gdmConvHandler) handleGdmData(gdmData *gdm.Data) (*gdm.Data, error) {
	log.Debugf(context.TODO(), "Handling authd protocol: %#v", gdmData)

	switch gdmData.Type {
	case gdm.DataType_hello:
		return &gdm.Data{
			Type:  gdm.DataType_hello,
			Hello: &gdm.HelloData{Version: h.protoVersion},
		}, nil

	case gdm.DataType_request:
		return h.handleAuthDRequest(gdmData)

	case gdm.DataType_poll:
		events := h.pendingEvents
		h.pendingEvents = nil
		if events != nil {
			go func() {
				// Ensure we mark the events as flushed only after we've returned.
				time.Sleep(gdmPollFrequency * 2)
				h.pendingEventsFlushed <- struct{}{}
			}()
		}
		return &gdm.Data{
			Type:         gdm.DataType_pollResponse,
			PollResponse: events,
		}, nil

	case gdm.DataType_event:
		if err := h.handleEvent(gdmData.Event); err != nil {
			return nil, err
		}
		return &gdm.Data{
			Type: gdm.DataType_eventAck,
		}, nil
	}

	return nil, fmt.Errorf("unhandled protocol message %s",
		gdmData.Type.String())
}

func (h *gdmConvHandler) handleAuthDRequest(gdmData *gdm.Data) (ret *gdm.Data, err error) {
	defer func() {
		h.handledRequests = append(h.handledRequests, gdmData.Request.Type)
		if h.wantRequests == nil {
			return
		}
		if !h.checkAllRequestsHaveBeenHandled() {
			return
		}

		h.wantRequests = nil
		go func() {
			// Mark the events received after or while we're returning.
			close(h.allRequestsReceived)
		}()
	}()

	switch req := gdmData.Request.Data.(type) {
	case *gdm.RequestData_UiLayoutCapabilities:
		return &gdm.Data{
			Type: gdm.DataType_response,
			Response: &gdm.ResponseData{
				Type: gdmData.Request.Type,
				Data: &gdm.ResponseData_UiLayoutCapabilities{
					UiLayoutCapabilities: &gdm.Responses_UiLayoutCapabilities{
						SupportedUiLayouts: h.supportedLayouts,
					},
				},
			},
		}, nil

	case *gdm.RequestData_ChangeStage:
		h.t.Logf("Switching to stage %s", req.ChangeStage.Stage)
		h.stageChanges = append(h.stageChanges, req.ChangeStage.Stage)

		h.currentStage = req.ChangeStage.Stage
		h.currentStageChanged.Broadcast()

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

func (h *gdmConvHandler) handleEvent(event *gdm.EventData) error {
	defer func() {
		h.receivedEvents = append(h.receivedEvents, event)

		if h.wantEvents == nil {
			return
		}
		if !h.checkAllEventsHaveBeenEmitted() {
			return
		}

		h.wantEvents = nil
		go func() {
			// Mark the events received after or while we're returning.
			close(h.allEventsReceived)
		}()
	}()

	switch ev := event.Data.(type) {
	case *gdm.EventData_BrokersReceived:
		h.receivedBrokers = ev.BrokersReceived.BrokersInfos

	case *gdm.EventData_BrokerSelected:
		h.selectedBrokerID = ev.BrokerSelected.BrokerId

	case *gdm.EventData_AuthModesReceived:
		// TODO: Check the auth modes are matching.

	case *gdm.EventData_UiLayoutReceived:
		if !slices.ContainsFunc(h.supportedLayouts, func(layout *authd.UILayout) bool {
			return layout.Type == ev.UiLayoutReceived.UiLayout.Type
		}) {
			return fmt.Errorf(`unknown layout type: "%s"`, ev.UiLayoutReceived.UiLayout.Type)
		}

	case *gdm.EventData_StartAuthentication:
		require.Equal(h.t, proto.Stage_challenge, h.currentStage,
			"Authentication started when we're not in challenge phase but in %s",
			h.currentStage)

		go func() {
			// Mark the events received after or while we're returning but not when locked.
			h.startAuthRequested <- struct{}{}
		}()

	case *gdm.EventData_AuthEvent:
		h.authEvents = append(h.authEvents, ev.AuthEvent.Response)
	}

	return nil
}

func (h *gdmConvHandler) waitForStageChange(stage proto.Stage) func() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.currentStage == stage && (h.lastNotifiedStage == nil || *h.lastNotifiedStage != stage) {
		h.lastNotifiedStage = &stage
		return nil
	}

	return func() {
		h.currentStageChanged.L.Lock()
		defer h.currentStageChanged.L.Unlock()

		for {
			// We just got notified for a stage change but we should not notify all the waiting
			// requests all together, each request to this function should be queued.
			// So the goroutine that won the lock is the one that will be unblocked if the stage
			// matches and if that's the first one noticing such change.
			if h.currentStage == stage && (h.lastNotifiedStage == nil || *h.lastNotifiedStage != stage) {
				h.lastNotifiedStage = &stage
				return
			}

			h.currentStageChanged.Wait()
		}
	}
}

func (h *gdmConvHandler) waitForAuthenticationStarted() {
	<-h.startAuthRequested
}

func (h *gdmConvHandler) consumeAuthenticationStartedEvents() {
	select {
	case <-h.startAuthRequested:
		h.t.Logf("Ignore pending authentication request")
	default:
		return
	}
}

func (h *gdmConvHandler) appendPollResultEvents(events ...*gdm.EventData) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pendingEvents = append(h.pendingEvents, events...)
}
