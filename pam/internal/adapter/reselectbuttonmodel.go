package adapter

import (
	tea "github.com/charmbracelet/bubbletea"
)

type authReselectButtonModel struct {
	*buttonModel
}

func newAuthReselectionButtonModel(label string) *authReselectButtonModel {
	return &authReselectButtonModel{
		buttonModel: &buttonModel{
			label: label,
		},
	}
}

// Init initializes the [reselectionButtonModel].
func (b authReselectButtonModel) Init() tea.Cmd {
	return b.buttonModel.Init()
}

// Update handles events and actions.
func (b authReselectButtonModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case buttonSelectionEvent:
		safeMessageDebug(msg, "button: %#v", b)
		if msg.model == b.buttonModel {
			return b, sendEvent(reselectAuthMode{})
		}
	}

	model, cmd := b.buttonModel.Update(msg)
	b.buttonModel = convertTo[*buttonModel](model)

	return b, cmd
}
