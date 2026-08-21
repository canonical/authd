package adapter

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

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
	_, cmd := m.handlePollResponse(change)
	msgs := collectMessages(cmd)
	require.True(t, containsAuthModeSelected(msgs, "password"),
		"selecting a different auth mode must trigger a re-selection")
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

	// Entering authModeSelection permits a genuine same-mode re-selection, so
	// discard an echo that was not delivered before navigating back.
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

type countingPAMClient struct {
	*pam_test.DummyClient

	selectAuthenticationModeCalls atomic.Int32
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
