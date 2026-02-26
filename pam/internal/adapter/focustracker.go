package adapter

import (
	"context"

	"github.com/canonical/authd/log"
	tea "github.com/charmbracelet/bubbletea"
)

type focusTrackerModel struct {
	focused bool
}

// Init initializes the model.
func (m focusTrackerModel) Init() tea.Cmd {
	return nil
}

// Update handles events and actions.
func (m focusTrackerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

// View renders the model.
func (m focusTrackerModel) View() string {
	return ""
}

// Focus focuses the model.
func (m *focusTrackerModel) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus", m)
	m.focused = true
	return nil
}

// Focused returns whether the model is focused.
func (m focusTrackerModel) Focused() bool {
	return m.focused
}

// Blur blurs the model.
func (m *focusTrackerModel) Blur() {
	log.Debugf(context.TODO(), "%T: Blur", m)
	m.focused = false
}
