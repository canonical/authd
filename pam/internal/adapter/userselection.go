package adapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
)

// userSelectionModel allows selecting from PAM or interactively a user.
type userSelectionModel struct {
	textinput.Model

	pamMTx     pam.ModuleTransaction
	clientType PamClientType
	enabled    bool
	selected   bool
}

// userSelected events to report that a new username has been selected.
type userSelected struct {
	username string
}

// userRequired events to pick a new username.
type userRequired struct{}

// sendUserSelected sends the event to select a new username.
func sendUserSelected(username string) tea.Cmd {
	return func() tea.Msg {
		return userSelected{username}
	}
}

// newUserSelectionModel returns an initialized userSelectionModel.
func newUserSelectionModel(pamMTx pam.ModuleTransaction, clientType PamClientType) userSelectionModel {
	u := textinput.New()
	if clientType != InteractiveTerminal {
		// Cursor events are racy: https://github.com/charmbracelet/bubbletea/issues/909.
		// FIXME: Avoid initializing the text input Model at all.
		u.Cursor.SetMode(cursor.CursorHide)
	}
	u.Prompt = "Username: " // TODO: i18n
	u.Placeholder = "user name"

	//TODO: u.Validate
	return userSelectionModel{
		Model: u,

		pamMTx:     pamMTx,
		clientType: clientType,
	}
}

// Init initializes userSelectionModel.
func (m userSelectionModel) Init() tea.Cmd {
	return nil
}

// SelectUser selects the user name to be used, by getting it from PAM if prefilled.
func (m userSelectionModel) SelectUser() tea.Cmd {
	pamUser, err := m.pamMTx.GetItem(pam.User)
	if cmd := maybeSendPamError(err); cmd != nil {
		return cmd
	}
	if pamUser != "" {
		return sendUserSelected(pamUser)
	}
	return sendEvent(userRequired{})
}

// Update handles events and actions.
func (m userSelectionModel) Update(msg tea.Msg) (userSelectionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case userSelected:
		safeMessageDebug(msg)
		currentUser, err := m.pamMTx.GetItem(pam.User)
		if cmd := maybeSendPamError(err); cmd != nil {
			return m, cmd
		}

		// authd uses lowercase usernames
		selectedUser := strings.ToLower(msg.username)
		isDifferentUser := selectedUser != strings.ToLower(currentUser)

		if !m.enabled && currentUser != "" && isDifferentUser {
			return m, sendEvent(pamError{
				status: pam.ErrPermDenied,
				msg: fmt.Sprintf("Changing username %q to %q is not allowed",
					currentUser, selectedUser),
			})
		}
		if selectedUser != currentUser {
			err := m.pamMTx.SetItem(pam.User, selectedUser)
			if cmd := maybeSendPamError(err); cmd != nil {
				return m, cmd
			}
		}
		m.selected = selectedUser != ""
		// synchronise our internal validated field and the text one.
		m.SetValue(msg.username)
		if !m.selected {
			return m, nil
		}
		return m, sendEvent(UsernameSelected{})

	case userRequired:
		safeMessageDebug(msg)
		m.enabled = true
		return m, sendEvent(ChangeStage{Stage: proto.Stage_userSelection})
	}

	if !m.enabled {
		return m, nil
	}

	if m.clientType != InteractiveTerminal {
		return m, nil
	}

	// interaction events
	if !m.Focused() {
		return m, nil
	}
	switch msg := msg.(type) {
	// Key presses
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			cmd := sendUserSelected(m.Value())
			return m, cmd
		}
	}

	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	return m, cmd
}

// Enabled returns whether the interactive user selection is enabled.
func (m userSelectionModel) Enabled() bool {
	return m.enabled
}

// Username returns the approved value of the text input.
func (m userSelectionModel) Username() string {
	if m.clientType == InteractiveTerminal && !m.selected {
		return ""
	}
	// authd uses lowercase usernames
	return strings.ToLower(m.Value())
}

// Focus sets the focus state on the model. We also mark as the user is not
// selected so that the returned value won't be valid until the user did an
// explicit ack.
func (m *userSelectionModel) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus", m)
	m.selected = false
	return m.Model.Focus()
}

// View renders a text view of the user selection UI.
func (m userSelectionModel) View() string {
	if !m.enabled {
		return ""
	}
	if !m.Focused() {
		return ""
	}
	return m.Model.View()
}
