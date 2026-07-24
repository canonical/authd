package adapter

import (
	"reflect"
	"testing"

	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/gdm_test"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
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
	// first one (UDENG-8799).
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

func TestGdmModelStageChangeClearsPendingEcho(t *testing.T) {
	t.Parallel()

	// A genuine re-selection always follows a stage change back into
	// authModeSelection. The stage change must drop any echo we were still
	// expecting, so that the re-selection is acted on instead of being
	// mistaken for the (never-delivered) echo of the previous selection.
	m := gdmModel{}
	m, _ = m.Update(AuthModeSelected{ID: "device_auth_qr"})
	require.Equal(t, "device_auth_qr", m.pendingEchoAuthModeID)

	m, _ = m.Update(StageChanged{Stage: proto.Stage_challenge})
	m, _ = m.Update(StageChanged{Stage: proto.Stage_authModeSelection})
	require.Empty(t, m.pendingEchoAuthModeID,
		"a stage change must drop a still-pending echo")

	reselect := []*gdm.EventData{gdm_test.AuthModeSelectedEvent("device_auth_qr")}
	_, cmd := m.handlePollResponse(reselect)
	msgs := collectMessages(cmd)
	require.True(t, containsAuthModeSelected(msgs, "device_auth_qr"),
		"re-selecting the same auth mode after a stage change must be honored")
}
