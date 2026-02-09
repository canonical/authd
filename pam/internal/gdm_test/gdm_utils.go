package gdm_test

import (
	"encoding/json"
	"testing"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/proto"
	"github.com/stretchr/testify/require"
)

// RequireEqualData ensures that data is equal by checking the marshalled values.
func RequireEqualData(t *testing.T, want any, actual any, args ...any) {
	t.Helper()

	wantJSON, err := json.MarshalIndent(want, "", "  ")
	require.NoError(t, err)
	actualJSON, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)

	require.Equal(t, string(wantJSON), string(actualJSON), args...)
}

// DataToJSON is a test helper function to convert GDM data to JSON.
func DataToJSON(t *testing.T, data *gdm.Data) string {
	t.Helper()

	json, err := data.JSON()
	require.NoError(t, err)
	return string(json)
}

// EventsGroupBegin returns a fake [gdm.EventData] that allows to begin a group multiple events
// so that it's possible to use this as an header to tell the test module handler that we should
// respond to an event with multiple events starting from the next one.
func EventsGroupBegin() *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType(-1000),
	}
}

// EventsGroupEnd returns a fake [gdm.EventData] that allows to end a group multiple events
// so that it's possible to use this as a footer to tell the test module handler that we should
// respond to an event with multiple events finishing with the previous one.
func EventsGroupEnd() *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType(-1001),
	}
}

// IgnoredEvent allows to ignore an event.
func IgnoredEvent() *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType(-1002),
	}
}

// SelectUserEvent generates a SelectUser event.
func SelectUserEvent(username string) *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_userSelected,
		Data: &gdm.EventData_UserSelected{
			UserSelected: &gdm.Events_UserSelected{UserId: username},
		},
	}
}

// SelectBrokerEvent generates a SelectBroker event.
func SelectBrokerEvent(brokerID string) *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_brokerSelected,
		Data: &gdm.EventData_BrokerSelected{
			BrokerSelected: &gdm.Events_BrokerSelected{BrokerId: brokerID},
		},
	}
}

// ChangeStageEvent generates a ChangeStage event.
func ChangeStageEvent(stage proto.Stage) *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_stageChanged,
		Data: &gdm.EventData_StageChanged{
			StageChanged: &gdm.Events_StageChanged{Stage: stage},
		},
	}
}

// AuthModeSelectedEvent generates a AuthModeSelected event.
func AuthModeSelectedEvent(authModeID string) *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_authModeSelected,
		Data: &gdm.EventData_AuthModeSelected{
			AuthModeSelected: &gdm.Events_AuthModeSelected{
				AuthModeId: authModeID,
			},
		},
	}
}

// ReselectAuthMode generates a ReselectAuthMode event.
func ReselectAuthMode() *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_reselectAuthMode,
		Data: &gdm.EventData_ReselectAuthMode{
			ReselectAuthMode: &gdm.Events_ReselectAuthMode{},
		},
	}
}

// IsAuthenticatedEvent generates a IsAuthenticated event.
func IsAuthenticatedEvent(item authd.IARequestAuthenticationDataItem) *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_isAuthenticatedRequested,
		Data: &gdm.EventData_IsAuthenticatedRequested{
			IsAuthenticatedRequested: &gdm.Events_IsAuthenticatedRequested{
				AuthenticationData: &authd.IARequest_AuthenticationData{Item: item},
			},
		},
	}
}

// IsAuthenticatedCancelledEvent generates a IsAuthenticated event.
func IsAuthenticatedCancelledEvent() *gdm.EventData {
	return &gdm.EventData{
		Type: gdm.EventType_isAuthenticatedCancelled,
		Data: &gdm.EventData_IsAuthenticatedCancelled{},
	}
}
