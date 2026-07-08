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
