package adapter

import (
	"testing"

	"github.com/canonical/authd/internal/brokers/auth"
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

func TestUIModelDoesNotForwardStaleStopAuthenticationToGDM(t *testing.T) {
	t.Parallel()

	authenticationModel := newAuthenticationModel(nil, Gdm, authd.SessionMode_LOGIN)
	currentModel := &focusTrackerModel{}
	currentModel.Focus()
	authenticationModel.currentModel = currentModel

	model := uiModel{
		clientType:          Gdm,
		authenticationModel: authenticationModel,
	}

	updated, _ := model.Update(startAuthentication{})
	model = convertTo[uiModel](updated)
	require.True(t, model.authenticationModel.inProgress)
	require.True(t, model.gdmModel.waitingAuth)

	stopPreviousChallenge := model.authenticationModel.Reset()

	currentModel = &focusTrackerModel{}
	currentModel.Focus()
	model.authenticationModel.currentModel = currentModel
	updated, _ = model.Update(startAuthentication{})
	model = convertTo[uiModel](updated)
	require.True(t, model.authenticationModel.inProgress)
	require.True(t, model.gdmModel.waitingAuth)

	updated, _ = model.Update(stopPreviousChallenge())
	model = convertTo[uiModel](updated)
	require.True(t, model.authenticationModel.inProgress,
		"a stale stop must not stop the current authentication")
	require.True(t, model.gdmModel.waitingAuth,
		"a stale stop must not stop GDM from accepting the current request")

	updated, _ = model.Update(model.authenticationModel.cancelIsAuthenticated()())
	model = convertTo[uiModel](updated)
	require.False(t, model.authenticationModel.inProgress)
	require.False(t, model.gdmModel.waitingAuth,
		"a stop from the current challenge must stop GDM authentication")
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

func TestFormModelSubmitsEmptyConfirmation(t *testing.T) {
	t.Parallel()

	entry := newTextInputModel(entries.CharsPassword)
	form := formModel{focusableModels: []authenticationComponent{&entry}}
	form.Focus()

	updated, cmd := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updatedForm, ok := updated.(formModel)
	require.True(t, ok)
	require.True(t, updatedForm.submitting)

	request, ok := cmd().(isAuthenticatedRequested)
	require.True(t, ok)
	secret, ok := request.item.(*authd.IARequest_AuthenticationData_Secret)
	require.True(t, ok)
	require.Empty(t, secret.Secret)
}

func TestAuthenticationModelPreservesEntraPasswordAcrossMFA(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthModeID
	entraPassword := "entra-password"

	updated, _ := model.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &entraPassword,
	})
	require.Equal(t, entraPassword, updated.entraAuthSecret)

	updated.currentAuthModeID = entraMFACodeModeID
	mfaCode := "123456"
	updated, cmd := updated.Update(isAuthenticatedResultReceived{
		access: auth.Granted,
		secret: &mfaCode,
		msg:    "{}",
	})

	success, ok := cmd().(PamSuccess)
	require.True(t, ok)
	require.Equal(t, entraPassword, success.AuthTok)
	require.Empty(t, success.OldAuthTok)
	require.Empty(t, updated.entraAuthSecret)
}

func TestAuthenticationModelPreservesEntraPasswordAcrossFidoPIN(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthModeID
	entraPassword := "entra-password"

	updated, _ := model.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &entraPassword,
	})
	updated.currentAuthModeID = entraAuthFidoPinModeID
	fidoPIN := "123456"
	updated, _ = updated.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &fidoPIN,
	})
	require.Equal(t, entraPassword, updated.entraAuthSecret)

	updated.currentAuthModeID = entraAuthFidoModeID
	updated, cmd := updated.Update(isAuthenticatedResultReceived{
		access: auth.Granted,
		msg:    "{}",
	})
	success, ok := cmd().(PamSuccess)
	require.True(t, ok)
	require.Equal(t, entraPassword, success.AuthTok)
	require.Empty(t, success.OldAuthTok)
	require.Empty(t, updated.entraAuthSecret)
}

func TestAuthenticationModelReturnsOldTokenForEntraPasswordReplacement(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthPasswordConfirmationModeID
	model.entraAuthSecret = "entra-password"

	localPassword := "local-password"
	updated, cmd := model.Update(isAuthenticatedResultReceived{
		access: auth.Granted,
		secret: &localPassword,
		msg:    "{}",
	})

	success, ok := cmd().(PamSuccess)
	require.True(t, ok)
	require.Equal(t, "entra-password", success.AuthTok)
	require.Equal(t, localPassword, success.OldAuthTok)
	require.Empty(t, updated.entraAuthSecret)
}

func TestAuthenticationModelDoesNotReturnEntraTokenWhenKeepingLocalPassword(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthPasswordConfirmationModeID
	model.entraAuthSecret = "entra-password"
	emptyPassword := ""

	updated, cmd := model.Update(isAuthenticatedResultReceived{
		access: auth.Granted,
		secret: &emptyPassword,
		msg:    "{}",
	})

	success, ok := cmd().(PamSuccess)
	require.True(t, ok)
	require.Empty(t, success.AuthTok)
	require.Empty(t, success.OldAuthTok)
	require.Empty(t, updated.entraAuthSecret)
}

func TestAuthenticationModelDropsUnverifiedEntraPassword(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthModeID
	entraPassword := "temporary-password"

	updated, _ := model.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    `{"message":"","entra_password_unverified":true}`,
		secret: &entraPassword,
	})
	require.Empty(t, updated.entraAuthSecret)
	require.Empty(t, updated.currentSecret)
}

func TestAuthenticationModelDoesNotExposeEntraFactorsAfterFallback(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthModeID
	entraPassword := "entra-password"

	updated, _ := model.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &entraPassword,
	})
	updated.currentAuthModeID = entraAuthFidoPinModeID
	fidoPIN := "123456"
	updated, _ = updated.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &fidoPIN,
	})
	require.Equal(t, entraPassword, updated.entraAuthSecret)

	updated.setAuthMode("device_auth")
	require.Empty(t, updated.entraAuthSecret)
	require.Empty(t, updated.currentSecret)
}

func TestAuthenticationModelClearsEntraPasswordOnDenial(t *testing.T) {
	t.Parallel()

	model := newAuthenticationModel(nil, InteractiveTerminal, authd.SessionMode_LOGIN)
	model.currentAuthModeID = entraAuthModeID
	entraPassword := "entra-password"
	model, _ = model.Update(isAuthenticatedResultReceived{
		access: auth.Next,
		msg:    "{}",
		secret: &entraPassword,
	})

	updated, cmd := model.Update(isAuthenticatedResultReceived{
		access: auth.Denied,
		msg:    `{"message":"denied"}`,
	})
	_, ok := cmd().(pamError)
	require.True(t, ok)
	require.Empty(t, updated.entraAuthSecret)
	require.Empty(t, updated.currentSecret)
}
