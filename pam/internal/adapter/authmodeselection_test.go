package adapter

import (
	"testing"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/stretchr/testify/require"
)

func TestAuthModeSelectionDeferredSelectionUsesAvailableMode(t *testing.T) {
	t.Parallel()

	authModes := []*authd.GAMResponse_AuthenticationMode{
		{Id: "first", Label: "First"},
		{Id: "second", Label: "Second"},
	}

	tests := map[string]struct {
		pendingID string
		wantID    string
	}{
		"Falls_back_from_invalid_pending_mode": {
			pendingID: "invalid",
			wantID:    "first",
		},
		"Preserves_valid_pending_mode": {
			pendingID: "second",
			wantID:    "second",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newAuthModeSelectionModel(Gdm)
			m.autoSelectedAuthModeID = tc.pendingID

			m, cmd := m.Update(authModesReceived{authModes: authModes})
			require.Equal(t, tc.wantID, m.autoSelectedAuthModeID)
			require.Nil(t, cmd)
		})
	}
}

func TestAuthModeSelectionPreservesGdmSelectionBeforeModesArrive(t *testing.T) {
	t.Parallel()

	modes := []*authd.GAMResponse_AuthenticationMode{
		{Id: "first", Label: "First"},
		{Id: "second", Label: "Second"},
	}
	m := newAuthModeSelectionModel(Gdm)

	m, cmd := m.Update(authModeSelected{id: "second", fromGDM: true})
	require.Nil(t, cmd)
	m, cmd = m.Update(authModesReceived{authModes: modes})
	require.Equal(t, "second", m.autoSelectedAuthModeID)
	require.True(t, m.autoSelectedAuthModeFromGDM)
	require.Nil(t, cmd)

	updated, cmd := m.Update(m.Focus()())
	m = updated
	require.NotNil(t, cmd)
	var selected authModeSelected
	foundSelected := false
	for _, msg := range collectMessages(cmd) {
		if selectedMode, ok := msg.(authModeSelected); ok {
			selected = selectedMode
			foundSelected = true
		}
	}
	require.True(t, foundSelected)
	require.Equal(t, "second", selected.id)
	require.True(t, selected.fromGDM)
}

func TestAuthModeSelectionIgnoresGdmEchoWhileAutoSelectionIsPending(t *testing.T) {
	t.Parallel()

	m := newAuthModeSelectionModel(Gdm)
	updated, cmd := m.Update(m.Focus()())
	m = updated
	require.Nil(t, cmd)

	modes := []*authd.GAMResponse_AuthenticationMode{
		{Id: "password", Label: "Password"},
	}
	m, autoSelectCmd := m.Update(authModesReceived{authModes: modes})
	require.Equal(t, "password", m.pendingAutoSelectedAuthModeID)

	m, cmd = m.Update(authModeSelected{id: "password", fromGDM: true})
	require.Nil(t, cmd)
	require.Equal(t, "password", m.pendingAutoSelectedAuthModeID)

	var selected authModeSelected
	foundSelected := false
	for _, msg := range collectMessages(autoSelectCmd) {
		if selectedMode, ok := msg.(authModeSelected); ok {
			selected = selectedMode
			foundSelected = true
		}
	}
	require.True(t, foundSelected)
	require.False(t, selected.fromGDM)

	m, cmd = m.Update(selected)
	require.Equal(t, "password", m.pendingAutoSelectedAuthModeID)
	foundSelectedEvent := false
	for _, msg := range collectMessages(cmd) {
		if _, ok := msg.(AuthModeSelected); ok {
			foundSelectedEvent = true
		}
	}
	require.True(t, foundSelectedEvent)

	m, cmd = m.Update(authModeSelected{id: "password", fromGDM: true})
	require.Nil(t, cmd, "the GDM echo must stay suppressed until selection is dispatched")
	m.Reset()
	require.Empty(t, m.pendingAutoSelectedAuthModeID)
}
