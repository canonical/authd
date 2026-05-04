package adapter

import (
	"testing"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/proto/authd"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestBrokerSelectionModelUpdate(t *testing.T) {
	t.Parallel()

	visibleBrokerInfo := &authd.ABResponse_BrokerInfo{
		Id:   "visible-broker",
		Name: "Visible Broker",
	}

	tests := map[string]struct {
		availableBrokers []*authd.ABResponse_BrokerInfo
		selectedBrokerID string

		wantMsg tea.Msg
	}{
		"Visible_broker_is_selected": {
			availableBrokers: []*authd.ABResponse_BrokerInfo{visibleBrokerInfo},
			selectedBrokerID: visibleBrokerInfo.Id,
			wantMsg:          BrokerSelected{BrokerID: visibleBrokerInfo.Id},
		},
		"Hidden_local_broker_returned_by_GetPreviousBroker_is_still_selected": {
			// Simulates hide_local_broker=true: AvailableBrokers does not include
			// the local broker, but GetPreviousBroker returns "local" for NSS-only
			// users (those provided by /etc/passwd or another NSS source).
			availableBrokers: []*authd.ABResponse_BrokerInfo{visibleBrokerInfo},
			selectedBrokerID: brokers.LocalBrokerName,
			wantMsg:          BrokerSelected{BrokerID: brokers.LocalBrokerName},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newBrokerSelectionModel(nil, Native)
			m.availableBrokers = tc.availableBrokers

			_, cmd := m.Update(brokerSelected{brokerID: tc.selectedBrokerID})
			require.NotNil(t, cmd, "expected a command to be returned")

			got := cmd()
			require.Equal(t, tc.wantMsg, got, "returned message does not match expected")
		})
	}
}
