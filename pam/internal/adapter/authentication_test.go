package adapter

import (
	"testing"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationModelLocksTerminalInputWhileAuthenticating(t *testing.T) {
	t.Parallel()

	entry := newTextInputModel(entries.CharsPassword)
	entry.SetValue("password")
	form := formModel{focusableModels: []authenticationComponent{&entry}}

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentModel = form
	model.currentModel.Focus()

	updated, _ := model.Update(isAuthenticatedRequested{
		item: &authd.IARequest_AuthenticationData_Secret{Secret: "password"},
	})
	require.True(t, updated.inputLocked)
	require.False(t, updated.Focused())

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	require.False(t, updated.Focused())
	require.Equal(t, "password", entry.Value())

	updated, _ = updated.Update(startAuthentication{})
	require.False(t, updated.inputLocked)
	require.True(t, updated.Focused())
}

func TestAuthenticationModelIgnoresStaleStopAuthentication(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentModel = newFormModel("", entries.CharsPassword, "", false)
	model.currentModel.Focus()

	model, _ = model.Update(startAuthentication{})
	require.True(t, model.inProgress)
	require.Equal(t, uint64(1), model.authGen)

	stopPreviousChallenge := model.Reset()

	model.currentModel = newFormModel("", entries.CharsPassword, "", false)
	model.currentModel.Focus()
	model, _ = model.Update(startAuthentication{})
	require.True(t, model.inProgress)
	require.Equal(t, uint64(2), model.authGen)

	updated, _ := model.Update(stopPreviousChallenge())
	require.True(t, updated.inProgress,
		"a stop from a previous challenge must not stop the current one")

	updated, _ = updated.Update(updated.cancelIsAuthenticated()())
	require.False(t, updated.inProgress,
		"a stop from the current challenge must still stop authentication")
}

func TestAuthenticationModelKeepsWaitLayoutVisibleWhileAuthenticating(t *testing.T) {
	t.Parallel()

	entry := newTextInputModel(entries.CharsPassword)
	form := formModel{focusableModels: []authenticationComponent{&entry}}

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentModel = form
	model.currentModel.Focus()

	updated, _ := model.Update(isAuthenticatedRequested{
		item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
	})
	require.False(t, updated.inputLocked)
	require.True(t, updated.Focused())
}

func TestFormModelLocksInputOnSubmission(t *testing.T) {
	t.Parallel()

	entry := newTextInputModel(entries.CharsPassword)
	entry.SetValue("password")
	form := formModel{focusableModels: []authenticationComponent{&entry}}
	form.Focus()

	updated, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updatedForm, ok := updated.(formModel)
	require.True(t, ok)
	require.True(t, updatedForm.submitting)

	updated, _ = updatedForm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updatedForm, ok = updated.(formModel)
	require.True(t, ok)
	require.True(t, updatedForm.submitting)
	require.Equal(t, "password", entry.Value())
}
