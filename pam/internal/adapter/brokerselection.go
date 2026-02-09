package adapter

import (
	"context"
	"fmt"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	tea_list "github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
)

// brokerSelectionModel is the model list selection layout to allow authenticating and return a password.
type brokerSelectionModel struct {
	List

	client authd.PAMClient

	availableBrokers []*authd.ABResponse_BrokerInfo
}

// brokersListReceived signals that the broker list from authd has been received.
type brokersListReceived struct {
	brokers []*authd.ABResponse_BrokerInfo
}

// brokerSelected is the internal event that a broker has been selected.
type brokerSelected struct {
	brokerID string
}

// brokerSelectionRequired is the internal event that a broker needs to be selected.
type brokerSelectionRequired struct{}

// selectBroker selects a given broker.
func selectBroker(brokerID string) tea.Cmd {
	return func() tea.Msg {
		return brokerSelected{
			brokerID: brokerID,
		}
	}
}

// newBrokerSelectionModel initializes an empty list with default options of brokerSelectionModel.
func newBrokerSelectionModel(client authd.PAMClient, clientType PamClientType) brokerSelectionModel {
	return brokerSelectionModel{
		List:   NewList(clientType, "Select your provider"),
		client: client,
	}
}

// Init initializes brokerSelectionModel by requesting the available brokers.
func (m brokerSelectionModel) Init() tea.Cmd {
	return nil
}

// Update handles events and actions.
func (m brokerSelectionModel) Update(msg tea.Msg) (brokerSelectionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case supportedUILayoutsSet:
		return m, getAvailableBrokers(m.client)

	case brokersListReceived:
		safeMessageDebug(msg)
		if len(msg.brokers) == 0 {
			return m, sendEvent(pamError{
				status: pam.ErrAuthinfoUnavail,
				msg:    "No brokers available",
			})
		}
		m.availableBrokers = msg.brokers

		var allBrokers []tea_list.Item
		for _, b := range m.availableBrokers {
			allBrokers = append(allBrokers, brokerItem{
				id:   b.Id,
				name: b.Name,
			})
		}
		var cmds []tea.Cmd
		cmds = append(cmds, m.SetItems(allBrokers))
		cmds = append(cmds, sendEvent(BrokerListReceived{}))

		return m, tea.Batch(cmds...)

	case brokerSelectionRequired:
		safeMessageDebug(msg)
		return m, sendEvent(ChangeStage{Stage: proto.Stage_brokerSelection})

	case listItemSelected:
		if !m.Focused() {
			return m, nil
		}

		safeMessageDebug(msg)
		broker := convertTo[brokerItem](msg.item)
		return m, selectBroker(broker.id)

	case brokerSelected:
		safeMessageDebug(msg)
		broker := brokerFromID(msg.brokerID, m.availableBrokers)
		if broker == nil {
			log.Infof(context.TODO(), "broker %q is not part of current active brokers", msg.brokerID)
			return m, nil
		}
		// Select correct line to ensure model is synchronised
		for i, b := range m.Items() {
			b := convertTo[brokerItem](b)
			if b.id != broker.Id {
				continue
			}
			m.Select(i)
		}

		return m, sendEvent(BrokerSelected{
			BrokerID: broker.Id,
		})
	}

	var cmd tea.Cmd
	m.List, cmd = m.List.Update(msg)
	return m, cmd
}

// AutoSelectForUser requests if any previous broker was used by this user to automatically selects it.
func AutoSelectForUser(client authd.PAMClient, username string) tea.Cmd {
	return func() tea.Msg {
		r, err := client.GetPreviousBroker(context.TODO(),
			&authd.GPBRequest{
				Username: username,
			})
		// We keep a chance to manually select the broker, not a blocker issue.
		if err != nil {
			log.Infof(context.TODO(), "can't get previous broker for %q", username)
			return brokerSelectionRequired{}
		}
		brokerID := r.GetPreviousBroker()
		if brokerID == "" {
			return brokerSelectionRequired{}
		}

		return selectBroker(brokerID)()
	}
}

// brokerItem is the list item corresponding to a broker.
type brokerItem struct {
	id   string
	name string
}

// FilterValue allows filtering the list items.
func (i brokerItem) FilterValue() string { return "" }

// getAvailableBrokers returns available broker list from authd.
func getAvailableBrokers(client authd.PAMClient) tea.Cmd {
	return func() tea.Msg {
		brokersInfo, err := client.AvailableBrokers(context.TODO(), &authd.Empty{})
		if err != nil {
			return pamError{
				status: pam.ErrSystem,
				msg:    fmt.Sprintf("could not get current available brokers: %v", err),
			}
		}

		return brokersListReceived{
			brokers: brokersInfo.BrokersInfos,
		}
	}
}

// brokerFromID return a broker matching brokerID if available, nil otherwise.
func brokerFromID(brokerID string, brokers []*authd.ABResponse_BrokerInfo) *authd.ABResponse_BrokerInfo {
	if brokerID == "" {
		return nil
	}

	for _, b := range brokers {
		if b.Id != brokerID {
			continue
		}
		return b
	}
	return nil
}
