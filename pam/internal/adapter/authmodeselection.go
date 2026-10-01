package adapter

import (
	"context"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	tea_list "github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
)

// authModeSelectionModel is the model list to select supported authentication modes.
type authModeSelectionModel struct {
	List

	supportedUILayouts          []*authd.UILayout
	availableAuthModes          []*authd.GAMResponse_AuthenticationMode
	autoSelectedAuthModeID      string
	autoSelectedAuthModeFromGDM bool
	autoSelectionGeneration     uint64
	currentAuthModeSelectedID   string
}

// supportedUILayoutsReceived is the internal event signalling that the current supported ui layout in the context have been received.
type supportedUILayoutsReceived struct {
	layouts []*authd.UILayout
}

// supportedUILayoutsSet is the event signalling that the current supported ui layout in the context have been set.
type supportedUILayoutsSet struct{}

// authModesReceived is the internal event signalling that the supported authentication modes have been received.
type authModesReceived struct {
	authModes []*authd.GAMResponse_AuthenticationMode
}

// authModeSelected is the internal event signalling that the an authentication mode has been selected.
type authModeSelected struct {
	id                      string
	fromGDM                 bool
	autoSelectionGeneration uint64
}

// selectAuthMode selects current authentication mode.
func selectAuthMode(id string) tea.Cmd {
	return func() tea.Msg {
		return authModeSelected{
			id: id,
		}
	}
}

func selectAutoAuthMode(id string, generation uint64, fromGDM bool) tea.Cmd {
	return func() tea.Msg {
		return authModeSelected{
			id:                      id,
			fromGDM:                 fromGDM,
			autoSelectionGeneration: generation,
		}
	}
}

func selectGdmAuthMode(id string) tea.Cmd {
	return func() tea.Msg {
		return authModeSelected{
			id:      id,
			fromGDM: true,
		}
	}
}

// newAuthModeSelectionModel initializes an empty list with default options of authModeSelectionModel.
func newAuthModeSelectionModel(clientType PamClientType) authModeSelectionModel {
	return authModeSelectionModel{
		List: NewList(clientType, "Select the authentication flow:"),
	}
}

// Init initializes authModeSelectionModel.
func (m authModeSelectionModel) Init() tea.Cmd {
	if m.clientType != InteractiveTerminal {
		// This is handled by the GDM or Native model!
		return nil
	}
	return func() tea.Msg {
		required, optional := layouts.Required, layouts.Optional
		supportedEntries := layouts.OptionalItems(
			entries.Chars,
			entries.CharsPassword,
		)
		rendersQrCode := true

		return supportedUILayoutsReceived{
			layouts: []*authd.UILayout{
				{
					Type:   layouts.Form,
					Label:  &required,
					Entry:  &supportedEntries,
					Wait:   &layouts.OptionalWithBooleans,
					Button: &optional,
				},
				{
					Type:          layouts.QrCode,
					Content:       &required,
					Code:          &optional,
					Wait:          &layouts.RequiredWithBooleans,
					Label:         &optional,
					Button:        &optional,
					RendersQrcode: &rendersQrCode,
				},
				{
					Type:   layouts.NewPassword,
					Label:  &required,
					Entry:  &supportedEntries,
					Button: &optional,
				},
			},
		}
	}
}

// Update handles events and actions.
func (m authModeSelectionModel) Update(msg tea.Msg) (authModeSelectionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case listFocused:
		cmd := m.updateListModel(msg)
		if m.id != msg.id {
			return m, cmd
		}

		safeMessageDebug(msg, "autoselect: %q", m.autoSelectedAuthModeID)

		if m.autoSelectedAuthModeID != "" {
			authMode := m.autoSelectedAuthModeID
			fromGDM := m.autoSelectedAuthModeFromGDM
			m.autoSelectedAuthModeID = ""
			m.autoSelectedAuthModeFromGDM = false
			m.autoSelectionGeneration++
			return m, tea.Sequence(cmd,
				selectAutoAuthMode(authMode, m.autoSelectionGeneration, fromGDM))
		}

		return m, cmd

	case supportedUILayoutsReceived:
		safeMessageDebug(msg)
		if len(msg.layouts) == 0 {
			return m, sendEvent(pamError{
				status: pam.ErrCredUnavail,
				msg:    "UI does not support any layouts",
			})
		}
		m.supportedUILayouts = msg.layouts
		return m, sendEvent(supportedUILayoutsSet{})

	case authModesReceived:
		safeMessageDebug(msg)
		m.availableAuthModes = msg.authModes

		var allAuthModes []tea_list.Item
		for _, a := range m.availableAuthModes {
			allAuthModes = append(allAuthModes, authModeItem{
				id:    a.Id,
				label: a.Label,
			})
		}

		cmd := m.SetItems(allAuthModes)

		// Auto-select the first auth mode when available.
		// For InteractiveTerminal, always select immediately because the
		// authModeSelection stage is skipped entirely (see model.go).
		// For GDM/Native, defer the selection until the auth mode selection
		// stage is active (list is focused), so the stage transition completes
		// before we request the corresponding UI layout.
		if len(m.availableAuthModes) == 0 {
			return m, cmd
		}

		firstAuthModeID := m.availableAuthModes[0].Id
		selectedID := firstAuthModeID
		pendingSelectionFromGDM := m.autoSelectedAuthModeFromGDM &&
			validAuthModeID(m.autoSelectedAuthModeID, m.availableAuthModes)
		if validAuthModeID(m.autoSelectedAuthModeID, m.availableAuthModes) {
			selectedID = m.autoSelectedAuthModeID
		}

		if m.clientType != InteractiveTerminal && !m.Focused() {
			m.autoSelectedAuthModeID = selectedID
			m.autoSelectedAuthModeFromGDM = pendingSelectionFromGDM
			return m, cmd
		}

		// When already focused, honor a pending selection from before modes
		// arrived (e.g. GDM client sent AuthModeSelected while stage change
		// arrived before the mode list was fetched). Fall back to the first
		// available mode.
		m.autoSelectedAuthModeID = ""
		m.autoSelectedAuthModeFromGDM = false
		m.autoSelectionGeneration++

		return m, tea.Sequence(cmd,
			selectAutoAuthMode(selectedID, m.autoSelectionGeneration, pendingSelectionFromGDM))

	case listItemSelected:
		if !m.Focused() {
			return m, m.updateListModel(msg)
		}

		safeMessageDebug(msg)
		authMode := convertTo[authModeItem](msg.item)
		m.autoSelectionGeneration++
		return m, tea.Sequence(m.updateListModel(msg),
			selectAuthMode(authMode.id))

	case authModeSelected:
		safeMessageDebug(msg)
		if msg.autoSelectionGeneration != 0 &&
			msg.autoSelectionGeneration != m.autoSelectionGeneration {
			log.Debugf(context.TODO(),
				"Ignoring stale automatic authentication mode selection %q", msg.id)
			return m, nil
		}
		if msg.fromGDM {
			m.autoSelectionGeneration++
			m.autoSelectedAuthModeID = ""
			m.autoSelectedAuthModeFromGDM = false
		}
		// Ensure auth mode id is valid
		if !validAuthModeID(msg.id, m.availableAuthModes) {
			if len(m.availableAuthModes) == 0 {
				// Modes not yet available; the stage change arrived before the
				// mode list was fetched (e.g. GDM/Native MFA path in model.go
				// where changeStageCmd runs before getModesCmd). Defer this
				// selection; it will be applied when authModesReceived fires.
				m.autoSelectedAuthModeID = msg.id
				m.autoSelectedAuthModeFromGDM = msg.fromGDM
				return m, nil
			}
			log.Infof(context.TODO(), "authentication mode %q is not part of currently available authentication mode", msg.id)
			return m, nil
		}
		m.currentAuthModeSelectedID = msg.id

		// Select correct line to ensure model is synchronised
		for i, a := range m.Items() {
			a := convertTo[authModeItem](a)
			if a.id != msg.id {
				continue
			}
			m.Select(i)
		}

		return m, sendEvent(AuthModeSelected{
			ID:      msg.id,
			fromGDM: msg.fromGDM,
		})
	}

	return m, m.updateListModel(msg)
}

func (m *authModeSelectionModel) updateListModel(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.List, cmd = m.List.Update(msg)
	return cmd
}

// authModeItem is the list item corresponding to an authentication mode.
type authModeItem struct {
	id    string
	label string
}

// FilterValue allows filtering the list items.
func (i authModeItem) FilterValue() string { return "" }

// validAuthModeID returns if a authmode ID exists in the available list.
func validAuthModeID(id string, authModes []*authd.GAMResponse_AuthenticationMode) bool {
	for _, a := range authModes {
		if a.Id != id {
			continue
		}
		return true
	}
	return false
}

// getAuthenticationModes returns available authentication mode for this broker from authd.
func getAuthenticationModes(client authd.PAMClient, sessionID string, uiLayouts []*authd.UILayout) tea.Cmd {
	return func() tea.Msg {
		gamReq := &authd.GAMRequest{
			SessionId:          sessionID,
			SupportedUiLayouts: uiLayouts,
		}

		gamResp, err := client.GetAuthenticationModes(context.Background(), gamReq)
		if err != nil {
			return pamError{
				status: pam.ErrSystem,
				msg:    err.Error(),
			}
		}

		authModes := gamResp.GetAuthenticationModes()
		if len(authModes) == 0 {
			return pamError{
				status: pam.ErrCredUnavail,
				msg:    "no supported authentication mode available for this provider",
			}
		}
		log.Debug(context.TODO(), "authModes", authModes)

		return authModesReceived{
			authModes: authModes,
		}
	}
}

// Reset clears the current selection and invalidates queued automatic selections.
func (m *authModeSelectionModel) Reset() {
	log.Debugf(context.TODO(), "%T: Reset", m)
	m.autoSelectionGeneration++
	m.currentAuthModeSelectedID = ""
	m.autoSelectedAuthModeID = ""
	m.autoSelectedAuthModeFromGDM = false
}

// SupportedUILayouts returns safely currently loaded supported ui layouts.
func (m authModeSelectionModel) SupportedUILayouts() []*authd.UILayout {
	return m.supportedUILayouts
}
