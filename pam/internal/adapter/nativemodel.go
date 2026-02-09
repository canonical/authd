package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
	"github.com/muesli/termenv"
	"github.com/skip2/go-qrcode"
)

type nativeModel struct {
	pamMTx            pam.ModuleTransaction
	userServiceClient authd.UserServiceClient

	availableBrokers []*authd.ABResponse_BrokerInfo
	authModes        []*authd.GAMResponse_AuthenticationMode
	selectedAuthMode string
	uiLayout         *authd.UILayout

	serviceName          string
	interactive          bool
	currentStage         proto.Stage
	busy                 bool
	userSelectionAllowed bool
}

const (
	nativeCancelKey = "r"

	polkitServiceName = "polkit-1"
)

type inputPromptStyle int

const (
	inputPromptStyleInline inputPromptStyle = iota
	inputPromptStyleMultiLine
)

// nativeStageChangeRequest is the internal event to request that a stage change.
type nativeStageChangeRequest ChangeStage

// nativeUserSelection is the internal event that a user needs to be (re)set.
type nativeUserSelection struct{}

// nativeBrokerSelection is the internal event that a broker needs to be (re)selected.
type nativeBrokerSelection struct{}

// nativeAuthSelection is used to require the user input for auth selection.
type nativeAuthSelection struct{}

// nativeChallengeRequested is used to require the user input for password.
type nativeChallengeRequested struct{}

// nativeAsyncOperationCompleted is a message to tell we're done with an async operation.
type nativeAsyncOperationCompleted struct{}

// nativeGoBack is a message to require to go back to previous stage.
type nativeGoBack struct{}

var errGoBack = errors.New("request to go back")
var errEmptyResponse = errors.New("empty response received")
var errNotAnInteger = errors.New("parsed value is not an integer")

func newNativeModel(mTx pam.ModuleTransaction, userServiceClient authd.UserServiceClient) nativeModel {
	m := nativeModel{pamMTx: mTx, userServiceClient: userServiceClient}

	var err error
	m.serviceName, err = m.pamMTx.GetItem(pam.Service)
	if err != nil {
		log.Errorf(context.TODO(), "failed to get the PAM service: %v", err)
	}

	m.interactive = isSSHSession(m.pamMTx) || IsTerminalTTY(m.pamMTx)

	return m
}

// Init initializes the native model orchestrator.
func (m nativeModel) Init() tea.Cmd {
	rendersQrCode := m.isQrcodeRenderingSupported()
	supportsQrCode := m.serviceName != polkitServiceName

	return func() tea.Msg {
		required, optional := layouts.Required, layouts.Optional
		supportedEntries := layouts.OptionalItems(
			entries.Chars,
			entries.CharsPassword,
			entries.Digits,
			entries.DigitsPassword,
		)

		supportedLayouts := supportedUILayoutsReceived{
			layouts: []*authd.UILayout{
				{
					Type:   layouts.Form,
					Label:  &required,
					Entry:  &supportedEntries,
					Wait:   &layouts.OptionalWithBooleans,
					Button: &optional,
				},
				{
					Type:   layouts.NewPassword,
					Label:  &required,
					Entry:  &supportedEntries,
					Button: &optional,
				},
			},
		}

		if supportsQrCode {
			supportedLayouts.layouts = append(supportedLayouts.layouts, &authd.UILayout{
				Type:          layouts.QrCode,
				Content:       &required,
				Code:          &optional,
				Wait:          &layouts.RequiredWithBooleans,
				Label:         &optional,
				Button:        &optional,
				RendersQrcode: &rendersQrCode,
			})
		}

		return supportedLayouts
	}
}

func (m nativeModel) checkStage(expected proto.Stage) bool {
	if m.currentStage != expected {
		log.Debugf(context.Background(),
			"Current stage %q is not matching expected %q", m.currentStage, expected)
		return false
	}
	return true
}

func (m nativeModel) requestStageChange(stage proto.Stage) tea.Cmd {
	return sendEvent(nativeStageChangeRequest{stage})
}

func (m nativeModel) Update(msg tea.Msg) (nativeModel, tea.Cmd) {
	safeMessageDebugWithPrefix("Native model update", msg)

	switch msg := msg.(type) {
	case StageChanged:
		m.currentStage = msg.Stage

	case nativeStageChangeRequest:
		if m.currentStage != msg.Stage {
			// Stage is not matching yet, ask for stage change first and repeat.
			return m, tea.Sequence(sendEvent(ChangeStage(msg)), sendEvent(msg))
		}

		switch m.currentStage {
		case proto.Stage_userSelection:
			return m, sendEvent(nativeUserSelection{})
		case proto.Stage_brokerSelection:
			return m, sendEvent(nativeBrokerSelection{})
		case proto.Stage_authModeSelection:
			return m, sendEvent(nativeAuthSelection{})
		case proto.Stage_challenge:
			return m, sendEvent(nativeChallengeRequested{})
		}

	case nativeAsyncOperationCompleted:
		m.busy = false

	case nativeGoBack:
		return m.goBackCommand()

	case userRequired:
		m.userSelectionAllowed = true
		return m, m.requestStageChange(proto.Stage_userSelection)

	case nativeUserSelection:
		if !m.checkStage(proto.Stage_userSelection) {
			return m, nil
		}
		if m.busy {
			// We may receive multiple concurrent requests, but due to the sync nature
			// of this model, we can't just accept them once we've one in progress already
			log.Debug(context.TODO(), "User selection already in progress")
			return m, nil
		}

		if cmd := maybeSendPamError(m.pamMTx.SetItem(pam.User, "")); cmd != nil {
			return m, cmd
		}

		return m.startAsyncOp(m.userSelection)

	case brokersListReceived:
		m.availableBrokers = msg.brokers

		// We should only handle this special case if there's more than one broker available.
		// Otherwise, we will break polkit for local users.
		if m.serviceName == polkitServiceName && len(msg.brokers) > 1 {
			// Do not support using local broker in the polkit case.
			// FIXME: This should be up to authd to keep a list of brokers based on service.
			m.availableBrokers = slices.DeleteFunc(slices.Clone(m.availableBrokers), func(b *authd.ABResponse_BrokerInfo) bool {
				return b.Id == brokers.LocalBrokerName
			})
		}

	case authModesReceived:
		m.authModes = msg.authModes

	case brokerSelectionRequired:
		if m.busy {
			// We may receive multiple concurrent requests, but due to the sync nature
			// of this model, we can't just accept them once we've one in progress already
			log.Debug(context.TODO(), "Broker selection already in progress")
			return m, nil
		}

		user, err := m.pamMTx.GetItem(pam.User)
		if err != nil {
			return m, maybeSendPamError(err)
		}
		return m.startAsyncOp(func() tea.Cmd {
			return m.maybePreCheckUser(user,
				m.requestStageChange(proto.Stage_brokerSelection))
		})

	case nativeBrokerSelection:
		if !m.checkStage(proto.Stage_brokerSelection) {
			return m, nil
		}
		if m.busy {
			// We may receive multiple concurrent requests, but due to the sync nature
			// of this model, we can't just accept them once we've one in progress already
			log.Debug(context.TODO(), "Broker selection already in progress")
			return m, nil
		}

		if len(m.availableBrokers) < 1 {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    "No brokers available to select",
			})
		}

		if len(m.availableBrokers) == 1 {
			return m, sendEvent(brokerSelected{brokerID: m.availableBrokers[0].Id})
		}

		return m.startAsyncOp(m.brokerSelection)

	case nativeAuthSelection:
		if !m.checkStage(proto.Stage_authModeSelection) {
			return m, nil
		}
		if m.busy {
			// We may receive multiple concurrent requests, but due to the sync nature
			// of this model, we can't just accept them once we've one in progress already
			log.Debug(context.TODO(), "Authentication selection already in progress")
			return m, nil
		}
		if m.selectedAuthMode != "" {
			return m, nil
		}
		if len(m.authModes) < 1 {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    "Can't authenticate without authentication modes",
			})
		}

		if len(m.authModes) == 1 {
			return m, selectAuthMode(m.authModes[0].Id)
		}

		return m.startAsyncOp(m.authModeSelection)

	case authModeSelected:
		m.selectedAuthMode = msg.id

	case UILayoutReceived:
		m.uiLayout = msg.layout

	case startAuthentication:
		return m, m.requestStageChange(proto.Stage_challenge)

	case nativeChallengeRequested:
		if !m.checkStage(proto.Stage_challenge) {
			return m, nil
		}
		if m.busy {
			// We may receive multiple concurrent requests, but due to the sync nature
			// of this model, we can't just accept them once we've one in progress already
			log.Debug(context.TODO(), "Challenge already in progress")
			return m, nil
		}
		return m.startAsyncOp(m.startChallenge)

	case newPasswordCheckResult:
		if msg.msg != "" {
			if cmd := maybeSendPamError(m.sendError(msg.msg)); cmd != nil {
				return m, cmd
			}
			return m, m.newPasswordChallenge(nil)
		}
		return m, m.newPasswordChallenge(&msg.password)

	case isAuthenticatedResultReceived:
		access := msg.access
		authMsg, err := dataToMsg(msg.msg)
		if cmd := maybeSendPamError(err); cmd != nil {
			return m, cmd
		}

		switch access {
		case auth.Granted:
			return m, maybeSendPamError(m.sendInfo(authMsg))
		case auth.Next:
			m.uiLayout = nil
			return m, maybeSendPamError(m.sendInfo(authMsg))
		case auth.Retry:
			return m, maybeSendPamError(m.sendError(authMsg))
		case auth.Denied:
			// This is handled by the main authentication model
			return m, nil
		case auth.Cancelled:
			return m, nil
		default:
			return m, maybeSendPamError(m.sendError("Access %q is not valid", access))
		}
	}

	return m, nil
}

func (m nativeModel) checkForPromptReplyValidity(reply string) error {
	switch reply {
	case nativeCancelKey:
		if m.canGoBack() {
			return errGoBack
		}
	case "", "\n":
		return errEmptyResponse
	}
	return nil
}

func (m nativeModel) promptForInput(style pam.Style, inputStyle inputPromptStyle, prompt string) (string, error) {
	format := "%s"
	if m.interactive {
		switch inputStyle {
		case inputPromptStyleInline:
			format = "%s: "
		case inputPromptStyleMultiLine:
			format = "%s:\n> "
		}
	}

	resp, err := m.pamMTx.StartStringConvf(style, format, prompt)
	if err != nil {
		return "", err
	}
	return resp.Response(), m.checkForPromptReplyValidity(resp.Response())
}

func (m nativeModel) promptForNumericInput(style pam.Style, prompt string) (int, error) {
	out, err := m.promptForInput(style, inputPromptStyleMultiLine, prompt)
	if err != nil {
		return -1, err
	}

	intOut, err := strconv.Atoi(out)
	if err != nil {
		return intOut, fmt.Errorf("%w: %w", errNotAnInteger, err)
	}

	return intOut, err
}

func (m nativeModel) promptForNumericInputUntilValid(style pam.Style, prompt string) (int, error) {
	value, err := m.promptForNumericInput(style, prompt)
	if !errors.Is(err, errNotAnInteger) {
		return value, err
	}

	err = m.sendError("Unsupported input")
	if err != nil {
		return -1, err
	}

	return m.promptForNumericInputUntilValid(style, prompt)
}

func (m nativeModel) promptForNumericInputAsString(style pam.Style, prompt string) (string, error) {
	input, err := m.promptForNumericInputUntilValid(style, prompt)
	return fmt.Sprint(input), err
}

func (m nativeModel) sendError(errorMsg string, args ...any) error {
	if errorMsg == "" {
		return nil
	}
	_, err := m.pamMTx.StartStringConvf(pam.ErrorMsg, errorMsg, args...)
	return err
}

func (m nativeModel) sendInfo(infoMsg string, args ...any) error {
	if infoMsg == "" {
		return nil
	}
	_, err := m.pamMTx.StartStringConvf(pam.TextInfo, infoMsg, args...)
	return err
}

type choicePair struct {
	id    string
	label string
}

func (m nativeModel) promptForChoiceWithMessage(title string, message string, choices []choicePair, prompt string) (string, error) {
	msg := fmt.Sprintf("== %s ==\n", title)
	if message != "" {
		msg += message + "\n"
	}

	for i, choice := range choices {
		msg += fmt.Sprintf("  %d. %s", i+1, choice.label)
		if i < len(choices)-1 {
			msg += "\n"
		}
	}

	if goBackLabel := m.goBackActionLabel(); goBackLabel != "" {
		msg += fmt.Sprintf("\nOr enter '%s' to %s", nativeCancelKey, goBackLabel)
	}

	for {
		if err := m.sendInfo(msg); err != nil {
			return "", err
		}
		idx, err := m.promptForNumericInputUntilValid(pam.PromptEchoOn, prompt)
		if err != nil {
			return "", err
		}
		// TODO: Maybe add support for default selection...

		if idx < 1 || idx > len(choices) {
			if err := m.sendError("Invalid selection"); err != nil {
				return "", err
			}
			continue
		}

		return choices[idx-1].id, nil
	}
}

func (m nativeModel) promptForChoice(title string, choices []choicePair, prompt string) (string, error) {
	return m.promptForChoiceWithMessage(title, "", choices, prompt)
}

func (m nativeModel) startAsyncOp(cmd func() tea.Cmd) (nativeModel, tea.Cmd) {
	m.busy = true
	return m, func() tea.Msg {
		ret := cmd()
		return tea.Sequence(
			sendEvent(nativeAsyncOperationCompleted{}),
			ret,
		)()
	}
}

func (m nativeModel) userSelection() tea.Cmd {
	user, err := m.promptForInput(pam.PromptEchoOn, inputPromptStyleInline, "Username")
	if errors.Is(err, errEmptyResponse) {
		return sendEvent(nativeUserSelection{})
	}
	if err != nil {
		return maybeSendPamError(err)
	}

	return m.maybePreCheckUser(user, sendEvent(userSelected{user}))
}

func (m nativeModel) maybePreCheckUser(user string, nextCmd tea.Cmd) tea.Cmd {
	if m.userServiceClient == nil {
		return nextCmd
	}

	// When the user service client is defined (i.e. under SSH for now) we want also
	// repeat the user pre-check, to ensure that the user is handled by at least
	// one broker, or we may end up leaking such infos.
	// We don't care about the content, we only care if the user is known by some broker.
	_, err := m.userServiceClient.GetUserByName(context.TODO(), &authd.GetUserByNameRequest{
		Name:           user,
		ShouldPreCheck: true,
	})
	if err != nil {
		log.Infof(context.TODO(), "can't get user info for %q: %v", user, err)
		return sendEvent(brokerSelected{brokerID: brokers.LocalBrokerName})
	}
	return nextCmd
}

func (m nativeModel) brokerSelection() tea.Cmd {
	var choices []choicePair
	for _, b := range m.availableBrokers {
		choices = append(choices, choicePair{id: b.Id, label: b.Name})
	}

	id, err := m.promptForChoice("Provider selection", choices, "Choose your provider")
	if errors.Is(err, errGoBack) {
		return sendEvent(nativeGoBack{})
	}
	if err != nil {
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("Provider selection error: %v", err),
		})
	}
	return sendEvent(brokerSelected{brokerID: id})
}

func (m nativeModel) authModeSelection() tea.Cmd {
	var choices []choicePair
	for _, am := range m.authModes {
		choices = append(choices, choicePair{id: am.Id, label: am.Label})
	}

	id, err := m.promptForChoice("Authentication method selection", choices,
		"Choose your authentication method")
	if errors.Is(err, errGoBack) {
		return sendEvent(nativeGoBack{})
	}
	if errors.Is(err, errEmptyResponse) {
		return m.requestStageChange(proto.Stage_challenge)
	}
	if err != nil {
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("Authentication method selection error: %v", err),
		})
	}

	return selectAuthMode(id)
}

func (m nativeModel) startChallenge() tea.Cmd {
	if m.uiLayout == nil {
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    "Can't authenticate without ui layout selected",
		})
	}

	hasWait := m.uiLayout.GetWait() == layouts.True

	switch m.uiLayout.Type {
	case layouts.Form:
		return m.handleFormChallenge(hasWait)

	case layouts.QrCode:
		if !hasWait {
			return sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    "Can't handle qrcode without waiting",
			})
		}
		return m.handleQrCode()

	case layouts.NewPassword:
		return m.handleNewPassword()

	default:
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("Unknown layout type: %q", m.uiLayout.Type),
		})
	}
}

func (m nativeModel) selectedAuthModeLabel(fallback string) string {
	authModeIdx := slices.IndexFunc(m.authModes, func(mode *authd.GAMResponse_AuthenticationMode) bool {
		return mode.Id == m.selectedAuthMode
	})
	if authModeIdx < 0 {
		return fallback
	}
	return m.authModes[authModeIdx].Label
}

func (m nativeModel) handleFormChallenge(hasWait bool) tea.Cmd {
	authMode := m.selectedAuthModeLabel("Authentication")

	if buttonLabel := m.uiLayout.GetButton(); buttonLabel != "" {
		choices := []choicePair{
			{id: "continue", label: fmt.Sprintf("Proceed with %s", authMode)},
		}
		if buttonLabel := m.uiLayout.GetButton(); buttonLabel != "" {
			choices = append(choices, choicePair{id: layouts.Button, label: buttonLabel})
		}

		id, err := m.promptForChoice(authMode, choices, "Choose action")
		if errors.Is(err, errGoBack) {
			return sendEvent(nativeGoBack{})
		}
		if errors.Is(err, errEmptyResponse) {
			return sendEvent(nativeChallengeRequested{})
		}
		if err != nil {
			return maybeSendPamError(err)
		}
		if id == layouts.Button {
			return sendEvent(reselectAuthMode{})
		}
	}

	var prompt string
	if m.uiLayout.Label != nil {
		prompt = strings.TrimSuffix(*m.uiLayout.Label, ":")
	}
	if prompt == "" {
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("No label provided for entry %q", m.uiLayout.GetEntry()),
		})
	}

	var instructions string
	if m.canGoBack() {
		instructions = "Enter '%[1]s' to cancel the request and %[2]s"
	}

	if hasWait {
		// Duplicating some contents here, as it will be better for translators once we've them
		instructions = "Leave the input field empty to wait for the alternative authentication method"
		if m.uiLayout.GetEntry() == "" {
			instructions = "Press Enter to wait for authentication"
		}

		if m.canGoBack() {
			instructions += " or enter '%[1]s' to %[2]s"
		}
	}

	if goBackLabel := m.goBackActionLabel(); goBackLabel != "" {
		instructions = "\n" + fmt.Sprintf(instructions, nativeCancelKey, m.goBackActionLabel())
	}
	if cmd := maybeSendPamError(m.sendInfo("== %s ==%s", authMode, instructions)); cmd != nil {
		return cmd
	}

	secret, err := m.promptForSecret(prompt)
	if errors.Is(err, errGoBack) {
		return sendEvent(nativeGoBack{})
	}
	if errors.Is(err, errEmptyResponse) {
		if hasWait {
			return sendAuthWaitCommand()
		}
		err = nil
	}
	if err != nil {
		return maybeSendPamError(err)
	}

	return sendEvent(isAuthenticatedRequested{
		item: &authd.IARequest_AuthenticationData_Secret{Secret: secret},
	})
}

func (m nativeModel) promptForSecret(prompt string) (string, error) {
	switch m.uiLayout.GetEntry() {
	case entries.Chars, "":
		return m.promptForInput(pam.PromptEchoOn, inputPromptStyleMultiLine, prompt)
	case entries.CharsPassword:
		return m.promptForInput(pam.PromptEchoOff, inputPromptStyleMultiLine, prompt)
	case entries.Digits:
		return m.promptForNumericInputAsString(pam.PromptEchoOn, prompt)
	case entries.DigitsPassword:
		return m.promptForNumericInputAsString(pam.PromptEchoOff, prompt)
	default:
		return "", fmt.Errorf("unhandled entry %q", m.uiLayout.GetEntry())
	}
}

func (m nativeModel) renderQrCode(qrCode *qrcode.QRCode) (qr string) {
	defer func() { qr = strings.TrimRight(qr, "\n") }()

	if os.Getenv("XDG_SESSION_TYPE") == "tty" {
		return qrCode.ToString(false)
	}

	switch termenv.DefaultOutput().Profile {
	case termenv.ANSI, termenv.Ascii:
		return qrCode.ToString(false)
	default:
		return qrCode.ToSmallString(false)
	}
}

func (m nativeModel) handleQrCode() tea.Cmd {
	qrCode, err := qrcode.New(m.uiLayout.GetContent(), qrcode.Medium)
	if err != nil {
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("Can't generate qrcode: %v", err),
		})
	}

	var qrcodeView []string
	qrcodeView = append(qrcodeView, m.uiLayout.GetLabel())

	var firstQrCodeLine string
	if m.isQrcodeRenderingSupported() {
		qrcode := m.renderQrCode(qrCode)
		qrcodeView = append(qrcodeView, qrcode)
		firstQrCodeLine = strings.SplitN(qrcode, "\n", 2)[0]
	}
	if firstQrCodeLine == "" {
		firstQrCodeLine = m.uiLayout.GetContent()
	}

	centeredContent := centerString(m.uiLayout.GetContent(), firstQrCodeLine)
	qrcodeView = append(qrcodeView, centeredContent)

	if code := m.uiLayout.GetCode(); code != "" {
		qrcodeView = append(qrcodeView, centerString(code, firstQrCodeLine))
	}

	// Add some extra vertical space to improve readability
	qrcodeView = append(qrcodeView, " ")

	choices := []choicePair{
		{id: layouts.Wait, label: "Wait for authentication result"},
	}
	if buttonLabel := m.uiLayout.GetButton(); buttonLabel != "" {
		choices = append(choices, choicePair{id: layouts.Button, label: buttonLabel})
	}

	id, err := m.promptForChoiceWithMessage(m.selectedAuthModeLabel("QR code"),
		strings.Join(qrcodeView, "\n"), choices, "Choose action")
	if errors.Is(err, errGoBack) {
		return sendEvent(nativeGoBack{})
	}
	if errors.Is(err, errEmptyResponse) {
		return sendAuthWaitCommand()
	}
	if err != nil {
		return maybeSendPamError(err)
	}

	switch id {
	case layouts.Button:
		return sendEvent(reselectAuthMode{})
	case layouts.Wait:
		return sendAuthWaitCommand()
	default:
		return nil
	}
}

func (m nativeModel) isQrcodeRenderingSupported() bool {
	switch m.serviceName {
	case polkitServiceName:
		return false
	default:
		if isSSHSession(m.pamMTx) {
			return false
		}
		return IsTerminalTTY(m.pamMTx)
	}
}

func centerString(s string, reference string) string {
	sizeDiff := len([]rune(reference)) - len(s)
	if sizeDiff <= 0 {
		return s
	}

	// We put padding in both sides, so that it's respected also by non-terminal UIs
	padding := strings.Repeat(" ", sizeDiff/2)
	return padding + s + padding
}

func (m nativeModel) handleNewPassword() tea.Cmd {
	if buttonLabel := m.uiLayout.GetButton(); buttonLabel != "" {
		choices := []choicePair{
			{id: "continue", label: "Proceed with password update"},
		}
		if buttonLabel := m.uiLayout.GetButton(); buttonLabel != "" {
			choices = append(choices, choicePair{id: layouts.Button, label: buttonLabel})
		}

		label := m.selectedAuthModeLabel("Password Update")
		id, err := m.promptForChoice(label, choices, "Choose action")
		if errors.Is(err, errGoBack) {
			return sendEvent(nativeGoBack{})
		}
		if errors.Is(err, errEmptyResponse) {
			return sendEvent(nativeChallengeRequested{})
		}
		if err != nil {
			return maybeSendPamError(err)
		}
		if id == layouts.Button {
			return sendEvent(isAuthenticatedRequested{
				item: &authd.IARequest_AuthenticationData_Skip{Skip: layouts.True},
			})
		}
	}

	return m.newPasswordChallenge(nil)
}

func (m nativeModel) newPasswordChallenge(previousPassword *string) tea.Cmd {
	if previousPassword == nil {
		var instructions string
		if goBackLabel := m.goBackActionLabel(); goBackLabel != "" {
			instructions = fmt.Sprintf("\nEnter '%[1]s' to cancel the request and %[2]s",
				nativeCancelKey, goBackLabel)
		}
		title := m.selectedAuthModeLabel("Password Update")
		if cmd := maybeSendPamError(m.sendInfo("== %s ==%s", title, instructions)); cmd != nil {
			return cmd
		}
	}

	prompt := m.uiLayout.GetLabel()
	if previousPassword != nil {
		prompt = "Confirm Password"
	}

	password, err := m.promptForSecret(prompt)
	if errors.Is(err, errGoBack) {
		return sendEvent(nativeGoBack{})
	}
	if err != nil && !errors.Is(err, errEmptyResponse) {
		return maybeSendPamError(err)
	}

	if previousPassword == nil {
		return sendEvent(newPasswordCheck{password: password})
	}
	if password != *previousPassword {
		err := m.sendError("Password entries don't match")
		if err != nil {
			return maybeSendPamError(err)
		}
		return m.newPasswordChallenge(nil)
	}
	return sendEvent(isAuthenticatedRequested{
		item: &authd.IARequest_AuthenticationData_Secret{Secret: password},
	})
}

func (m nativeModel) goBackCommand() (nativeModel, tea.Cmd) {
	if m.currentStage >= proto.Stage_challenge && m.uiLayout != nil {
		m.uiLayout = nil
	}
	if m.currentStage >= proto.Stage_authModeSelection {
		m.selectedAuthMode = ""
	}
	if m.currentStage == proto.Stage_authModeSelection {
		m.authModes = nil
	}

	return m, func() tea.Cmd {
		if !m.canGoBack() {
			return nil
		}
		return m.requestStageChange(m.previousStage())
	}()
}

func (m nativeModel) canGoBack() bool {
	if m.userSelectionAllowed {
		return m.currentStage > proto.Stage_userSelection
	}
	return m.previousStage() > proto.Stage_userSelection
}

func (m nativeModel) previousStage() proto.Stage {
	if m.currentStage > proto.Stage_authModeSelection && len(m.authModes) > 1 {
		return proto.Stage_authModeSelection
	}
	if m.currentStage > proto.Stage_brokerSelection && len(m.availableBrokers) > 1 {
		return proto.Stage_brokerSelection
	}
	return proto.Stage_userSelection
}

func (m nativeModel) goBackActionLabel() string {
	if !m.canGoBack() {
		return ""
	}

	return goBackLabel(m.previousStage())
}

func sendAuthWaitCommand() tea.Cmd {
	return sendEvent(isAuthenticatedRequested{
		item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
	})
}
