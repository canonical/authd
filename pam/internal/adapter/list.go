package adapter

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/canonical/authd/log"
	tea_list "github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var listIDs atomic.Uint32

// List is a [tea_list.Model] implementation for authd list views.
type List struct {
	tea_list.Model

	clientType PamClientType
	id         uint32
	focused    bool
}

// listFocused is the internal event to signal that the list view is focused.
type listFocused struct {
	id uint32
}

// listItemSelected is the internal event to signal that the a list item has been selected.
type listItemSelected struct {
	item tea_list.Item
}

// itemLayout is the rendering delegation of [brokerItem] and [authModeItem].
type itemLayout struct{}

// Height returns height of the items.
func (d itemLayout) Height() int { return 1 }

// Spacing returns the spacing needed between the items.
func (d itemLayout) Spacing() int { return 0 }

// Update triggers the update of each item.
func (d itemLayout) Update(_ tea.Msg, _ *tea_list.Model) tea.Cmd { return nil }

// Render writes to w the rendering of the items based on its selection and type.
func (d itemLayout) Render(w io.Writer, m tea_list.Model, index int, item tea_list.Item) {
	var label string
	switch item := item.(type) {
	case brokerItem:
		label = item.name
	case authModeItem:
		label = item.label
	default:
		log.Errorf(context.TODO(), "Unexpected item element type: %t", item)
		return
	}

	line := fmt.Sprintf("%d. %s", index+1, label)

	if index == m.Index() {
		line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"}).Render("> " + line)
	} else {
		line = lipgloss.NewStyle().PaddingLeft(2).Render(line)
	}
	fmt.Fprint(w, line)
}

// NewList creates a new [list] model.
func NewList(clientType PamClientType, title string) List {
	l := tea_list.New(nil, itemLayout{}, 0, 0)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()

	list := List{
		Model:      l,
		clientType: clientType,
		id:         listIDs.Add(1),
	}

	// FIXME: decouple UI from data model.
	if clientType != InteractiveTerminal {
		return list
	}

	l.Title = title
	l.Styles.Title = lipgloss.NewStyle()

	l.SetShowTitle(title != "")
	l.SetShowPagination(true)
	l.SetSize(80, 24)
	list.Model = l

	return list
}

// Update handles events and actions.
func (m List) Update(msg tea.Msg) (List, tea.Cmd) {
	switch msg := msg.(type) {
	case listFocused:
		if msg.id != m.id {
			return m, nil
		}

		safeMessageDebug(msg)
		m.focused = true
		return m, nil
	}

	if m.clientType != InteractiveTerminal {
		return m, nil
	}

	// interaction events
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	// Key presses
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			item := m.SelectedItem()
			if item == nil {
				return m, nil
			}
			return m, sendEvent(listItemSelected{item})

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// This is necessarily an integer, so above
			choice, _ := strconv.Atoi(msg.String())
			items := m.Items()
			if choice > len(items) {
				return m, nil
			}
			item := items[choice-1]
			return m, sendEvent(listItemSelected{item})
		}
	}

	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	return m, cmd
}

// View renders a text view of the model.
func (m List) View() string {
	if !m.Focused() {
		return ""
	}

	return strings.TrimRightFunc(m.Model.View(), unicode.IsSpace)
}

// Focus focuses this model.
func (m List) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus", m)
	return sendEvent(listFocused{m.id})
}

// Focused returns if this model is focused.
func (m List) Focused() bool {
	return m.focused
}

// Blur releases the focus from this model.
func (m *List) Blur() {
	log.Debugf(context.TODO(), "%T: Blur", m)
	m.focused = false
}
