package adapter

import (
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// textinputModel is a base block for handling textinput.Model, delegating to a tea.Model approach.
type textinputModel struct {
	textinput.Model
}

func newTextInputModel(entryType string) textinputModel {
	inputModel := textinputModel{Model: textinput.New()}

	switch entryType {
	case entries.CharsPassword, entries.DigitsPassword:
		inputModel.EchoMode = textinput.EchoPassword
	}

	return inputModel
}

// Init initializes textinputModel.
func (m textinputModel) Init() tea.Cmd {
	return nil
}

// Update handles events and actions.
func (m textinputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	return &m, cmd
}
