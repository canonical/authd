package adapter

import (
	"context"
	"slices"
	"strings"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// formModel is the form layout type to allow authentication and return a password.
type formModel struct {
	label string

	focusableModels []authenticationComponent
	focusIndex      int

	wait bool
}

// newFormModel initializes and return a new formModel.
func newFormModel(label, entryType, buttonLabel string, wait bool) formModel {
	var focusableModels []authenticationComponent

	// TODO: add digits and force validation.
	switch entryType {
	case entries.Chars, entries.CharsPassword:
		entry := newTextInputModel(entryType)
		focusableModels = append(focusableModels, &entry)
		label = strings.TrimSuffix(label, ":") + ":"
	}
	if buttonLabel != "" {
		button := newAuthReselectionButtonModel(buttonLabel)
		focusableModels = append(focusableModels, button)
	}

	return formModel{
		label: label,
		wait:  wait,

		focusableModels: focusableModels,
	}
}

// Init initializes formModel.
func (m formModel) Init() tea.Cmd {
	var commands []tea.Cmd
	for _, c := range m.focusableModels {
		commands = append(commands, c.Init())
	}
	return tea.Batch(commands...)
}

// Update handles events and actions.
func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case startAuthentication:
		// Reset the entry.
		for _, fm := range m.focusableModels {
			switch entry := fm.(type) {
			case *textinputModel:
				entry.SetValue("")
			}
		}

		if !m.wait {
			return m, nil
		}
		return m, tea.Sequence(m.updateFocusModel(msg), sendEvent(isAuthenticatedRequested{
			item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
		}))
	}

	switch msg := msg.(type) {
	// Key presses
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.focusIndex >= len(m.focusableModels) {
				return m, nil
			}
			entry := m.focusableModels[m.focusIndex]
			switch entry := entry.(type) {
			case *textinputModel:
				return m, sendEvent(isAuthenticatedRequested{
					item: &authd.IARequest_AuthenticationData_Secret{
						Secret: entry.Value(),
					},
				})
			}

		case "tab":
			m.focusIndex++
			if m.focusIndex == len(m.focusableModels) {
				m.focusIndex = 0
			}
			var cmd tea.Cmd
			for i, fm := range m.focusableModels {
				if i != m.focusIndex {
					fm.Blur()
					continue
				}
				cmd = fm.Focus()
			}
			return m, cmd
		}
	}

	return m, m.updateFocusModel(msg)
}

func (m *formModel) updateFocusModel(msg tea.Msg) tea.Cmd {
	if m.focusIndex >= len(m.focusableModels) {
		return nil
	}
	model, cmd := m.focusableModels[m.focusIndex].Update(msg)
	m.focusableModels[m.focusIndex] = convertTo[authenticationComponent](model)

	return cmd
}

// View renders a text view of the form.
func (m formModel) View() string {
	var fields []string

	if m.label != "" {
		fields = append(fields, m.label)
	}

	for _, fm := range m.focusableModels {
		fields = append(fields, fm.View())
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		fields...,
	)
}

// Focus focuses this model.
func (m formModel) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus", m)
	if m.focusIndex >= len(m.focusableModels) {
		return nil
	}
	return m.focusableModels[m.focusIndex].Focus()
}

// Blur releases the focus from this model.
func (m formModel) Blur() {
	log.Debugf(context.TODO(), "%T: Blur", m)
	if m.focusIndex >= len(m.focusableModels) {
		return
	}
	m.focusableModels[m.focusIndex].Blur()
}

// Focused returns whether this model is focused.
func (m formModel) Focused() bool {
	if len(m.focusableModels) == 0 {
		// We consider the model being focused in this case, since there's nothing
		// to interact with, but we want to be able to draw.
		return true
	}
	return slices.ContainsFunc(m.focusableModels, func(ac authenticationComponent) bool {
		return ac.Focused()
	})
}
