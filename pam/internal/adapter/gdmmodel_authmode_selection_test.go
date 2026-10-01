package adapter

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/gdm_test"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// collectMessages runs a command and recursively flattens the batch/sequence
// messages it produces into the concrete messages they ultimately deliver.
// tea.Batch and tea.Sequence return []tea.Cmd-shaped messages whose concrete
// types are unexported, so they are detected structurally via reflection.
func collectMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if cmds, ok := asCmdSlice(msg); ok {
		var msgs []tea.Msg
		for _, c := range cmds {
			msgs = append(msgs, collectMessages(c)...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

// asCmdSlice reports whether msg is a []tea.Cmd-shaped batch/sequence message
// and, if so, returns its commands.
func asCmdSlice(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	cmdType := reflect.TypeOf(tea.Cmd(nil))
	cmds := make([]tea.Cmd, v.Len())
	for i := range cmds {
		cmd, ok := v.Index(i).Convert(cmdType).Interface().(tea.Cmd)
		if !ok {
			return nil, false
		}
		cmds[i] = cmd
	}
	return cmds, true
}

type countingPAMClient struct {
	*pam_test.DummyClient

	selectAuthenticationModeCalls atomic.Int32
}

func (c *countingPAMClient) SelectAuthenticationMode(ctx context.Context,
	in *authd.SAMRequest, opts ...grpc.CallOption) (*authd.SAMResponse, error) {
	c.selectAuthenticationModeCalls.Add(1)
	return c.DummyClient.SelectAuthenticationMode(ctx, in, opts...)
}

func TestGdmAuthModeSelectionsCallSelectAuthenticationModeOnceEach(t *testing.T) {
	t.Parallel()

	const (
		passwordModeID = "password"
		deviceModeID   = "device_auth_qr"
	)

	client := &countingPAMClient{
		DummyClient: pam_test.NewDummyClient(nil,
			pam_test.WithIgnoreSessionIDChecks(),
			pam_test.WithUILayout(passwordModeID, "Password", pam_test.FormUILayout()),
			pam_test.WithUILayout(deviceModeID, "Device authentication", pam_test.QrCodeUILayout()),
		),
	}
	mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
		func(*gdm.Data) (*gdm.Data, error) {
			return &gdm.Data{Type: gdm.DataType_eventAck}, nil
		},
	))
	m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
	m.currentSession = &sessionInfo{brokerID: "broker", sessionID: "session"}
	m.authModeSelectionModel.availableAuthModes = []*authd.GAMResponse_AuthenticationMode{
		{Id: passwordModeID},
		{Id: deviceModeID},
	}
	m.authModeSelectionModel.autoSelectedAuthModeID = passwordModeID

	// Switching back to a previously selected mode is a genuine selection and
	// must make another RPC.
	for i, authModeID := range []string{passwordModeID, deviceModeID, passwordModeID} {
		m = selectGdmAuthenticationMode(t, m, authModeID)
		require.Equal(t, int32(i+1), client.selectAuthenticationModeCalls.Load(),
			"each genuine GDM selection should make exactly one selection RPC")
		require.Empty(t, m.authModeSelectionModel.autoSelectedAuthModeID,
			"a GDM selection must supersede any deferred automatic selection")
	}
}

func selectGdmAuthenticationMode(t *testing.T, m uiModel, authModeID string) uiModel {
	t.Helper()

	updated, cmd := m.Update(gdmPollResponse{
		pollResponse: []*gdm.EventData{gdm_test.AuthModeSelectedEvent(authModeID)},
	})
	m = convertTo[uiModel](updated)

	var selectedIDs []string
	for _, msg := range collectMessages(cmd) {
		selected, ok := msg.(authModeSelected)
		if !ok {
			continue
		}
		selectedIDs = append(selectedIDs, selected.id)

		m.authModeSelectionModel, cmd = m.authModeSelectionModel.Update(selected)
		for _, selectionMsg := range collectMessages(cmd) {
			authMode, ok := selectionMsg.(AuthModeSelected)
			if !ok {
				continue
			}
			updated, cmd = m.Update(authMode)
			m = convertTo[uiModel](updated)
			_ = collectMessages(cmd)
		}
	}
	require.Equal(t, []string{authModeID}, selectedIDs)
	return m
}

func TestUIModelDropsResultFromSupersededGdmChallenge(t *testing.T) {
	t.Parallel()

	var sentEvents []gdm.EventType
	mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
		func(data *gdm.Data) (*gdm.Data, error) {
			if data.Type == gdm.DataType_event && data.Event != nil {
				sentEvents = append(sentEvents, data.Event.Type)
			}
			return &gdm.Data{Type: gdm.DataType_eventAck}, nil
		},
	))
	client := pam_test.NewDummyClient(nil, pam_test.WithIgnoreSessionIDChecks())
	m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
	m.authenticationModel.authGen = 1
	done := make(chan struct{})
	m.authenticationModel.authTracker.done = done
	_ = m.authenticationModel.ResetForAuthModeSwitch()
	require.Equal(t, uint64(2), m.authenticationModel.authGen)

	updated, cmd := m.Update(isAuthenticatedResultReceived{
		authGen: 1,
		access:  auth.Granted,
	})
	m = convertTo[uiModel](updated)

	require.Nil(t, cmd, "a stale result must not complete PAM authentication")
	require.Empty(t, sentEvents, "a stale result must not be sent to GDM")
	select {
	case <-done:
	default:
		t.Fatal("dropping a stale result must release the active authentication")
	}
}

func TestGdmChallengeProtocolEventsAreOrdered(t *testing.T) {
	t.Parallel()

	var protocolEvents []string
	mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
		func(data *gdm.Data) (*gdm.Data, error) {
			switch data.Type {
			case gdm.DataType_event:
				switch data.Event.Type {
				case gdm.EventType_uiLayoutReceived:
					protocolEvents = append(protocolEvents, "layout")
				case gdm.EventType_startAuthentication:
					protocolEvents = append(protocolEvents, "start")
				}
				return &gdm.Data{Type: gdm.DataType_eventAck}, nil

			case gdm.DataType_request:
				protocolEvents = append(protocolEvents, "stage")
				return &gdm.Data{
					Type: gdm.DataType_response,
					Response: &gdm.ResponseData{
						Type: data.Request.Type,
						Data: &gdm.ResponseData_Ack{},
					},
				}, nil
			}

			return &gdm.Data{Type: gdm.DataType_eventAck}, nil
		},
	))

	client := pam_test.NewDummyClient(nil,
		pam_test.WithIgnoreSessionIDChecks(),
		pam_test.WithUILayout(layouts.QrCode, "Device authentication", pam_test.QrCodeUILayout()),
	)
	m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
	m.currentSession = &sessionInfo{brokerID: "broker", sessionID: "session"}
	label := "Device authentication"

	updated, cmd := m.Update(UILayoutReceived{
		layout: &authd.UILayout{
			Type:  layouts.QrCode,
			Label: &label,
		},
	})
	m = convertTo[uiModel](updated)
	layoutCommands, ok := asCmdSlice(cmd())
	require.True(t, ok)
	require.Len(t, layoutCommands, 2)
	require.Nil(t, layoutCommands[0]())
	require.Equal(t, []string{"layout"}, protocolEvents)

	stageRequest, ok := layoutCommands[1]().(ChangeStage)
	require.True(t, ok)
	require.Equal(t, proto.Stage_challenge, stageRequest.Stage)

	updated, cmd = m.Update(stageRequest)
	m = convertTo[uiModel](updated)
	msgs := collectMessages(cmd)

	var stageChanged StageChanged
	for _, msg := range msgs {
		if msg, ok := msg.(StageChanged); ok {
			stageChanged = msg
			break
		}
	}
	require.Equal(t, proto.Stage_challenge, stageChanged.Stage)

	updated, cmd = m.Update(stageChanged)
	m = convertTo[uiModel](updated)
	stageCommands, ok := asCmdSlice(cmd())
	require.True(t, ok)
	require.Len(t, stageCommands, 2)
	require.Nil(t, stageCommands[0]())
	require.Equal(t, []string{"layout", "stage"}, protocolEvents)

	start, ok := stageCommands[1]().(startAuthentication)
	require.True(t, ok)
	updated, cmd = m.Update(start)
	m = convertTo[uiModel](updated)
	_ = collectMessages(cmd)

	require.Equal(t, []string{"layout", "stage", "start"}, protocolEvents)
}

func TestAuthTrackerRejectsRequestScheduledBeforeCancellation(t *testing.T) {
	t.Parallel()

	tracker := &authTracker{}
	expectedGeneration := tracker.currentGeneration()
	tracker.cancelAndWait()

	require.False(t, tracker.waitForSlot(func() {}, expectedGeneration))
	require.Nil(t, tracker.done)
}
