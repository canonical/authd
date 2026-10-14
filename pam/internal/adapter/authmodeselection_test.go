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

func TestGdmSelectionSupersedesQueuedAutomaticSelection(t *testing.T) {
	t.Parallel()

	m := newAuthModeSelectionModel(Gdm)
	m.availableAuthModes = []*authd.GAMResponse_AuthenticationMode{
		{Id: "password"},
		{Id: "device_auth_qr"},
	}
	m.autoSelectedAuthModeID = "password"
	m.autoSelectionGeneration = 1

	m, cmd := m.Update(authModeSelected{id: "password", fromGDM: true})
	require.NotNil(t, cmd)
	require.Empty(t, m.autoSelectedAuthModeID)

	m, cmd = m.Update(authModeSelected{
		id:                      "password",
		autoSelectionGeneration: 1,
	})
	require.Nil(t, cmd, "a queued automatic selection must not follow a GDM selection")
	require.Equal(t, "password", m.currentAuthModeSelectedID)
}

func TestAuthModeSelectionPreservesDeferredGdmSelection(t *testing.T) {
	t.Parallel()

	m := newAuthModeSelectionModel(Gdm)
	m, _ = m.Update(authModeSelected{id: "device_auth_qr", fromGDM: true})
	require.Equal(t, "device_auth_qr", m.autoSelectedAuthModeID)
	require.True(t, m.autoSelectedAuthModeFromGDM)

	m, _ = m.Update(authModesReceived{
		authModes: []*authd.GAMResponse_AuthenticationMode{
			{Id: "password"},
			{Id: "device_auth_qr"},
		},
	})
	require.Equal(t, "device_auth_qr", m.autoSelectedAuthModeID)
	require.True(t, m.autoSelectedAuthModeFromGDM)

	m, cmd := m.Update(listFocused{id: m.id})
	var selected authModeSelected
	for _, msg := range collectMessages(cmd) {
		if msg, ok := msg.(authModeSelected); ok {
			selected = msg
			break
		}
	}
	require.Equal(t, "device_auth_qr", selected.id)
	require.True(t, selected.fromGDM,
		"the deferred selection must still reset the prior GDM challenge")

	m, cmd = m.Update(selected)
	for _, msg := range collectMessages(cmd) {
		if selected, ok := msg.(AuthModeSelected); ok {
			require.True(t, selected.fromGDM)
			return
		}
	}
	t.Fatal("deferred GDM selection was not forwarded")
}
