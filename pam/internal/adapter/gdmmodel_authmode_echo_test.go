package adapter

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
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

func containsAuthModeSelected(msgs []tea.Msg, id string) bool {
	for _, msg := range msgs {
		if m, ok := msg.(authModeSelected); ok && m.id == id {
			return true
		}
	}
	return false
}

func TestGdmModelIgnoresAuthModeSelectedEcho(t *testing.T) {
	t.Parallel()

	// After we select an auth mode, GDM echoes the selection back as a poll
	// event. Acting on that echo would re-run SelectAuthenticationMode and, for
	// device auth, mint a second device code while the poll is still on the
	// first one (https://github.com/canonical/authd/issues/1121).
	m := gdmModel{}
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})
	require.Equal(t, "device_auth_qr", m.pendingEchoAuthModeID,
		"selecting an auth mode should record the expected echo")

	echo := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	var cmd tea.Cmd
	m, cmd = m.handlePollResponse(echo)
	msgs := collectMessages(cmd)
	require.False(t, containsAuthModeSelected(msgs, "device_auth_qr"),
		"echo of the just-selected auth mode must not trigger a re-selection")
	require.Empty(t, m.pendingEchoAuthModeID,
		"consuming the echo should clear the expected echo")
}

func TestGdmModelActsOnAuthModeChange(t *testing.T) {
	t.Parallel()

	// A genuine change to a different auth mode must still be acted on.
	m := gdmModel{}
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})

	change := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("password")}
	m, cmd := m.handlePollResponse(change)
	msgs := collectMessages(cmd)
	require.True(t, containsAuthModeSelected(msgs, "password"),
		"selecting a different auth mode must trigger a re-selection")
	require.Empty(t, m.pendingEchoAuthModeID)

	_, cmd = m.handlePollResponse(
		[]*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")},
	)
	require.True(t, containsAuthModeSelected(collectMessages(cmd), "device_auth_qr"),
		"returning to the old mode must not be mistaken for an outstanding echo")
}

func TestGdmModelActsOnSameAuthModeReselection(t *testing.T) {
	t.Parallel()

	// Suppression is a one-shot: only the immediate echo of our own selection
	// is dropped. A later genuine re-selection of the same auth mode (the user
	// picking it again) must be honored, because the pending echo has already
	// been consumed.
	m := gdmModel{}
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})

	echo := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	m, cmd := m.handlePollResponse(echo)
	_ = collectMessages(cmd)
	require.Empty(t, m.pendingEchoAuthModeID,
		"the echo should have been consumed")

	reselect := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	_, cmd = m.handlePollResponse(reselect)
	msgs := collectMessages(cmd)
	require.True(t, containsAuthModeSelected(msgs, "device_auth_qr"),
		"a genuine re-selection of the same auth mode must be honored")
}

func TestGdmModelStageChangeHandlesPendingEcho(t *testing.T) {
	t.Parallel()

	// The normal transition to challenge must preserve a pending echo because
	// the asynchronous GDM poll may deliver it after the stage change.
	m := gdmModel{}
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})
	require.Equal(t, "device_auth_qr", m.pendingEchoAuthModeID)

	m, _ = m.Update(StageChanged{Stage: proto.Stage_challenge})
	require.Equal(t, "device_auth_qr", m.pendingEchoAuthModeID,
		"entering challenge must preserve a pending GDM echo")

	echo := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	var cmd tea.Cmd
	m, cmd = m.handlePollResponse(echo)
	msgs := collectMessages(cmd)
	require.False(t, containsAuthModeSelected(msgs, "device_auth_qr"),
		"a delayed echo after entering challenge must still be suppressed")
	require.Empty(t, m.pendingEchoAuthModeID,
		"consuming the delayed echo should clear the expected echo")

	// Entering auth mode selection permits a genuine re-selection, so discard
	// an echo that was not delivered before navigating back.
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})
	m, _ = m.Update(StageChanged{Stage: proto.Stage_authModeSelection})
	require.Empty(t, m.pendingEchoAuthModeID,
		"entering auth mode selection must clear a pending echo")

	reselect := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	_, cmd = m.handlePollResponse(reselect)
	msgs = collectMessages(cmd)
	require.True(t, containsAuthModeSelected(msgs, "device_auth_qr"),
		"re-selecting the same auth mode after a stage change must be honored")
}

func TestUIModelClearsPendingEchoAcrossSessions(t *testing.T) {
	t.Parallel()

	const authModeID = "device_auth_qr"

	m := uiModel{clientType: Gdm}
	m.gdmModel.pendingEchoAuthModeID = authModeID

	// A new session must not inherit an echo from an earlier session.
	updated, _ := m.Update(sessionStartedForTest(t, "first-session"))
	m = convertTo[uiModel](updated)
	require.Empty(t, m.gdmModel.pendingEchoAuthModeID,
		"starting a session must clear a pending echo")

	// The first session arms the echo suppression for its own selection.
	updated, _ = m.Update(AuthModeSelected{ID: authModeID})
	m = convertTo[uiModel](updated)
	require.Equal(t, authModeID, m.gdmModel.pendingEchoAuthModeID)

	updated, _ = m.Update(SessionEnded{})
	m = convertTo[uiModel](updated)
	require.Empty(t, m.gdmModel.pendingEchoAuthModeID,
		"ending a session must clear a pending echo")

	// A same-ID selection in the next session is genuine and must be
	// forwarded, rather than mistaken for the previous session's echo.
	updated, _ = m.Update(sessionStartedForTest(t, "second-session"))
	m = convertTo[uiModel](updated)
	updated, cmd := m.Update(gdmPollResponse{
		pollResponse: []*gdm.EventData{gdm_test.AuthModeSelectedEvent(authModeID)},
	})
	m = convertTo[uiModel](updated)
	require.True(t, containsAuthModeSelected(collectMessages(cmd), authModeID),
		"a same-ID selection in a new session must not be suppressed")
}

func sessionStartedForTest(t *testing.T, sessionID string) SessionStarted {
	t.Helper()

	pubASN1, err := x509.MarshalPKIXPublicKey(&gdmTestPrivateKey.PublicKey)
	require.NoError(t, err)

	return SessionStarted{
		brokerID:      "test-broker",
		sessionID:     sessionID,
		encryptionKey: base64.StdEncoding.EncodeToString(pubASN1),
	}
}

type countingPAMClient struct {
	*pam_test.DummyClient

	selectAuthenticationModeCalls atomic.Int32
}

type orderedModesPAMClient struct {
	*pam_test.DummyClient

	onGetModes func()
}

func (c *orderedModesPAMClient) GetAuthenticationModes(ctx context.Context,
	in *authd.GAMRequest, opts ...grpc.CallOption,
) (*authd.GAMResponse, error) {
	c.onGetModes()
	return c.DummyClient.GetAuthenticationModes(ctx, in, opts...)
}

func (c *countingPAMClient) SelectAuthenticationMode(ctx context.Context,
	in *authd.SAMRequest, opts ...grpc.CallOption) (*authd.SAMResponse, error) {
	c.selectAuthenticationModeCalls.Add(1)
	return c.DummyClient.SelectAuthenticationMode(ctx, in, opts...)
}

func TestGdmAuthModeEchoDoesNotSelectAuthenticationModeTwice(t *testing.T) {
	t.Parallel()

	const authModeID = "device_auth_qr"

	client := &countingPAMClient{
		DummyClient: pam_test.NewDummyClient(nil,
			pam_test.WithIgnoreSessionIDChecks(),
			pam_test.WithUILayout(authModeID, "Device authentication", pam_test.QrCodeUILayout()),
		),
	}
	mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
		func(*gdm.Data) (*gdm.Data, error) {
			return &gdm.Data{Type: gdm.DataType_eventAck}, nil
		},
	))
	m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
	m.currentSession = &sessionInfo{sessionID: "session"}
	m.authModeSelectionModel.availableAuthModes = []*authd.GAMResponse_AuthenticationMode{
		{Id: authModeID},
	}

	updated, cmd := m.Update(AuthModeSelected{ID: authModeID})
	m = convertTo[uiModel](updated)
	_ = collectMessages(cmd)
	require.Equal(t, int32(1), client.selectAuthenticationModeCalls.Load(),
		"the initial auth mode selection should call the broker once")

	updated, cmd = m.Update(gdmPollResponse{
		pollResponse: []*gdm.EventData{gdm_test.AuthModeSelectedEvent(authModeID)},
	})
	m = convertTo[uiModel](updated)

	// Feed any selection generated by the poll response through the same
	// sub-model path as Bubble Tea. A duplicate selection would result in a
	// second SelectAuthenticationMode call here.
	for _, msg := range collectMessages(cmd) {
		selected, ok := msg.(authModeSelected)
		if !ok {
			continue
		}

		var selectionCmd tea.Cmd
		m.authModeSelectionModel, selectionCmd = m.authModeSelectionModel.Update(selected)
		for _, selectionMsg := range collectMessages(selectionCmd) {
			authMode, ok := selectionMsg.(AuthModeSelected)
			if !ok {
				continue
			}

			updated, layoutCmd := m.Update(authMode)
			m = convertTo[uiModel](updated)
			_ = collectMessages(layoutCmd)
		}
	}

	require.Equal(t, int32(1), client.selectAuthenticationModeCalls.Load(),
		"GDM's echo must not select the auth mode again")
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

func TestGdmMFARequestsAuthenticationModesAfterEnteringSelectionStage(t *testing.T) {
	t.Parallel()

	var protocolEvents []string
	mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
		func(data *gdm.Data) (*gdm.Data, error) {
			if data.Type == gdm.DataType_request {
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
	client := &orderedModesPAMClient{
		DummyClient: pam_test.NewDummyClient(nil,
			pam_test.WithIgnoreSessionIDChecks(),
			pam_test.WithGetAuthenticationModesReturn([]*authd.GAMResponse_AuthenticationMode{
				{Id: "password"},
			}, nil),
		),
		onGetModes: func() {
			protocolEvents = append(protocolEvents, "modes")
		},
	}
	m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
	m.currentSession = &sessionInfo{sessionID: "session"}
	m.authenticationModel.inProgress = true
	m.gdmModel.pendingEchoAuthModeID = "password"

	updated, cmd := m.Update(GetAuthenticationModesRequested{})
	m = convertTo[uiModel](updated)
	msgs := collectMessages(cmd)

	require.Len(t, msgs, 1)
	changeStage, ok := msgs[0].(ChangeStage)
	require.True(t, ok)
	require.Equal(t, proto.Stage_authModeSelection, changeStage.Stage)

	updated, cmd = m.Update(changeStage)
	m = convertTo[uiModel](updated)
	msgs = collectMessages(cmd)
	var stageChanged StageChanged
	for _, msg := range msgs {
		stageChanged, ok = msg.(StageChanged)
		if ok {
			break
		}
	}
	require.Equal(t, proto.Stage_authModeSelection, stageChanged.Stage)

	updated, cmd = m.Update(stageChanged)
	m = convertTo[uiModel](updated)
	msgs = collectMessages(cmd)
	require.Equal(t, []string{"stage", "modes"}, protocolEvents)
	require.Equal(t, "password", m.gdmModel.pendingEchoAuthModeID)
	modesReceived, ok := msgs[len(msgs)-1].(authModesReceived)
	require.True(t, ok)
	require.Equal(t, "password", modesReceived.authModes[0].GetId())

	m.gdmModel, cmd = m.gdmModel.handlePollResponse(
		[]*gdm.EventData{gdm_test.AuthModeSelectedEvent("password")},
	)
	require.False(t, containsAuthModeSelected(collectMessages(cmd), "password"),
		"a delayed echo must not trigger a duplicate auth mode selection")
	require.Empty(t, m.gdmModel.pendingEchoAuthModeID)
}

func TestGdmMFAEmptyPollPreservesPendingEcho(t *testing.T) {
	t.Parallel()

	for _, nextMode := range []string{"", "password", "otp"} {
		t.Run("next_mode_"+nextMode, func(t *testing.T) {
			t.Parallel()

			m := gdmModel{}
			m, _ = m.Update(AuthModeSelected{ID: "password"})
			m.preservePendingEchoOnNextAuthModeStage = true
			m, _ = m.Update(StageChanged{Stage: proto.Stage_authModeSelection})

			expectedEcho := "password"
			if nextMode != "" {
				m, _ = m.Update(AuthModeSelected{ID: nextMode})
				expectedEcho = nextMode
			}

			// A poll sent before the selection may return after it.
			m, _ = m.handlePollResponse(nil)
			require.Equal(t, expectedEcho, m.pendingEchoAuthModeID)

			echo := []*gdm.EventData{gdm_test.AuthModeSelectedEvent(expectedEcho)}
			m, cmd := m.handlePollResponse(echo)
			require.False(t, containsAuthModeSelected(collectMessages(cmd), expectedEcho),
				"a delayed echo must not trigger a duplicate selection")
			require.Empty(t, m.pendingEchoAuthModeID)

			_, cmd = m.handlePollResponse(echo)
			require.True(t, containsAuthModeSelected(collectMessages(cmd), expectedEcho),
				"a later genuine same-mode re-selection must be honored")
		})
	}
}

func TestGdmMFAProtocolOrdering(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		modeID string
		failAt string
	}{
		"same mode in next step":      {modeID: "entra_auth"},
		"different mode in next step": {modeID: "otp"},
		"next event fails":            {modeID: "entra_auth", failAt: "next"},
		"stage request fails":         {modeID: "entra_auth", failAt: "authModeSelection"},
		"mode list event fails":       {modeID: "entra_auth", failAt: "modes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var protocolEvents []string
			mTx := pam_test.NewModuleTransactionDummy(gdm.DataConversationFunc(
				func(data *gdm.Data) (*gdm.Data, error) {
					var event string
					switch data.Type {
					case gdm.DataType_request:
						event = data.Request.GetChangeStage().Stage.String()
					case gdm.DataType_event:
						switch data.Event.Type {
						case gdm.EventType_authEvent:
							event = data.Event.GetAuthEvent().Response.Access
						case gdm.EventType_authModesReceived:
							event = "modes"
						case gdm.EventType_authModeSelected:
							event = "selection"
						case gdm.EventType_uiLayoutReceived:
							event = "layout"
						case gdm.EventType_startAuthentication:
							event = "start"
						}
					}
					require.NotEmpty(t, event, "unexpected protocol message: %v", data)
					protocolEvents = append(protocolEvents, event)
					if event == tc.failAt {
						return nil, errors.New("GDM acknowledgement failed")
					}
					if data.Type == gdm.DataType_request {
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
			client := &orderedModesPAMClient{
				DummyClient: pam_test.NewDummyClient(nil,
					pam_test.WithIgnoreSessionIDChecks(),
					pam_test.WithUILayout(tc.modeID, "Next challenge", pam_test.FormUILayout()),
				),
				onGetModes: func() {
					protocolEvents = append(protocolEvents, "get-modes")
				},
			}
			m := newUIModelForClients(mTx, Gdm, authd.SessionMode_LOGIN, client, nil, nil)
			m.currentSession = &sessionInfo{brokerID: "broker", sessionID: "session"}
			m.authenticationModel.inProgress = true
			m.authModeSelectionModel.currentAuthModeSelectedID = "entra_auth"

			// Process each message before running sibling commands. This lets
			// a fast broker overtake any GDM command left in a concurrent batch.
			var runMessage func(tea.Msg)
			var gotErrors []pamError
			var messages int
			runMessage = func(msg tea.Msg) {
				if msg == nil {
					return
				}
				messages++
				require.Less(t, messages, 100, "unexpected message loop")
				if cmds, ok := asCmdSlice(msg); ok {
					for _, cmd := range cmds {
						if cmd != nil {
							runMessage(cmd())
						}
					}
					return
				}
				if err, ok := msg.(pamError); ok {
					gotErrors = append(gotErrors, err)
					return
				}
				updated, cmd := m.Update(msg)
				m = convertTo[uiModel](updated)
				if cmd != nil {
					runMessage(cmd())
				}
			}
			runMessage(isAuthenticatedResultReceived{access: auth.Next})

			wantEvents := []string{
				"next", "authModeSelection", "get-modes", "modes",
				"selection", "layout", "challenge", "start",
			}
			if tc.failAt != "" {
				for i, event := range wantEvents {
					if event == tc.failAt {
						wantEvents = wantEvents[:i+1]
						break
					}
				}
				require.Len(t, gotErrors, 1)
				require.Contains(t, gotErrors[0].msg, "GDM acknowledgement failed")
			} else {
				require.Empty(t, gotErrors)
				require.Equal(t, proto.Stage_challenge, m.currentStage())
				require.Equal(t, tc.modeID, m.authModeSelectionModel.currentAuthModeSelectedID)
			}
			require.Equal(t, wantEvents, protocolEvents)
		})
	}
}
