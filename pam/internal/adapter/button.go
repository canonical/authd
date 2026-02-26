package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/authd/log"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// reselectionWaitTime is the amount of time that we wait before emitting buttonSelected event.
const reselectionWaitTime = 500 * time.Millisecond

type buttonSelectionEvent struct {
	model *buttonModel
}

// buttonModel creates a virtual button model which can be focused.
type buttonModel struct {
	label         string
	selectionTime time.Time

	focused bool
}

// Init initializes buttonModel.
func (b buttonModel) Init() tea.Cmd {
	return nil
}

// Update handles events and actions.
func (b buttonModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case startAuthentication:
		b.selectionTime = time.Now()

	// Key presses
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			now := time.Now()
			if now.Sub(b.selectionTime) < reselectionWaitTime {
				log.Debug(context.TODO(), "Button press ignored, too fast!")
				return &b, nil
			}
			b.selectionTime = now
			return &b, sendEvent(buttonSelectionEvent{&b})
		}
	}

	return &b, nil
}

var (
	buttonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"}).
			MarginLeft(2).
			MarginTop(1)

	activeButtonStyle = buttonStyle.
				Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#000000"}).
				Background(lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"})
)

// View renders a text view of the button.
func (b buttonModel) View() string {
	content := fmt.Sprintf("[ %s ]", b.label)
	if b.focused {
		return activeButtonStyle.Render(content)
	}
	return buttonStyle.Render(content)
}

// Focus focuses this model.
func (b *buttonModel) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus", b)
	b.focused = true
	return nil
}

// Blur releases the focus from this model.
func (b *buttonModel) Blur() {
	log.Debugf(context.TODO(), "%T: Blur", b)
	b.focused = false
}

// Focused returns whether this model is focused.
func (b buttonModel) Focused() bool {
	return b.focused
}
