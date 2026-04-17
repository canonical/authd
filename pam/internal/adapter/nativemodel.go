package adapter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
	"github.com/muesli/termenv"
	"github.com/skip2/go-qrcode"
)

const (
	nativeCancelKey = "r"

	polkitServiceName = "polkit-1"
)

type inputPromptStyle int

const (
	inputPromptStyleInline inputPromptStyle = iota
	inputPromptStyleMultiLine
)

var errGoBack = errors.New("request to go back")
var errEmptyResponse = errors.New("empty response received")
var errNotAnInteger = errors.New("parsed value is not an integer")

// nativeClient is the Native PAM client. It runs the entire authentication
// flow as a single sequential goroutine: prompt -> RPC -> check result -> repeat.
// It integrates with bubbletea only at the boundary: Init() returns a tea.Cmd
// that runs the goroutine and returns the final PamReturnStatus when done.
type nativeClient struct {
	pamMTx            pam.ModuleTransaction
	client            authd.PAMClient
	userServiceClient authd.UserServiceClient
	mode              authd.SessionMode

	serviceName string
	interactive bool
}

func newNativeModel(mTx pam.ModuleTransaction, client authd.PAMClient, mode authd.SessionMode, userServiceClient authd.UserServiceClient) nativeClient {
	m := nativeClient{pamMTx: mTx, client: client, mode: mode, userServiceClient: userServiceClient}

	var err error
	m.serviceName, err = m.pamMTx.GetItem(pam.Service)
	if err != nil {
		log.Errorf(context.TODO(), "failed to get the PAM service: %v", err)
	}

	m.interactive = isSSHSession(m.pamMTx) || IsTerminalTTY(m.pamMTx)

	return m
}

// Init returns a tea.Cmd that runs the full sequential authentication flow.
// When done, it emits a PamReturnStatus to end the bubbletea program.
func (m nativeClient) Init() tea.Cmd {
	return func() tea.Msg {
		return m.run()
	}
}

// Update is a no-op: the nativeClient handles everything in its goroutine.
func (m nativeClient) Update(_ tea.Msg) (nativeClient, tea.Cmd) {
	return m, nil
}

// run executes the full authentication flow sequentially and returns the result.
func (m nativeClient) run() PamReturnStatus {
	// User selection.
	username, err := m.selectUser()
	if err != nil {
		return pamReturnErrorFrom(err)
	}

	// Broker + session selection.
	brokerID, sessionID, encryptionKey, err := m.selectBrokerAndStartSession(username)
	if err != nil {
		return pamReturnErrorFrom(err)
	}

	// Authentication loop: repeats on auth.Next (multi-step auth).
	for {
		done, result, err := m.authLoop(sessionID, brokerID, encryptionKey)
		if err != nil {
			return pamReturnErrorFrom(err)
		}
		if done {
			return result
		}
		// auth.Next: get a new session and continue.
		sessionID, encryptionKey, err = m.startSession(brokerID, username)
		if err != nil {
			return pamReturnErrorFrom(err)
		}
	}
}

// selectUser prompts for a username (if not already set by PAM) and returns it.
func (m nativeClient) selectUser() (string, error) {
	user, err := m.pamMTx.GetItem(pam.User)
	if err != nil {
		return "", fmt.Errorf("getting PAM user: %w", err)
	}
	if user != "" {
		// Username was already set by the PAM stack (e.g. SSH, su).
		return user, nil
	}

	// Interactive prompt loop.
	for {
		if err := m.pamMTx.SetItem(pam.User, ""); err != nil {
			return "", err
		}
		user, err = m.promptForInput(pam.PromptEchoOn, inputPromptStyleInline, "Username")
		if errors.Is(err, errEmptyResponse) {
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}

	// Under SSH, pre-check the user to avoid leaking whether an account exists.
	if m.userServiceClient != nil {
		_, err := m.userServiceClient.GetUserByName(context.TODO(), &authd.GetUserByNameRequest{
			Name:           user,
			ShouldPreCheck: true,
		})
		if err != nil {
			log.Infof(context.TODO(), "can't get user info for %q: %v", user, err)
			// Fall through to the local broker so the caller gets ErrIgnore.
			return brokers.LocalBrokerName, nil
		}
	}

	if err := m.pamMTx.SetItem(pam.User, user); err != nil {
		return "", err
	}
	return user, nil
}

// selectBrokerAndStartSession picks a broker (automatically or by prompting),
// starts a session, and returns the session details.
func (m nativeClient) selectBrokerAndStartSession(username string) (brokerID, sessionID string, encryptionKey *rsa.PublicKey, err error) {
	if username == brokers.LocalBrokerName {
		return "", "", nil, nativeErrorf(pam.ErrIgnore, "")
	}

	availableBrokers, err := m.getAvailableBrokers()
	if err != nil {
		return "", "", nil, err
	}

	// Filter out local broker for polkit when other brokers are available.
	if m.serviceName == polkitServiceName && len(availableBrokers) > 1 {
		availableBrokers = slices.DeleteFunc(slices.Clone(availableBrokers), func(b *authd.ABResponse_BrokerInfo) bool {
			return b.Id == brokers.LocalBrokerName
		})
	}

	brokerID, err = m.chooseBroker(username, availableBrokers)
	if err != nil {
		return "", "", nil, err
	}
	if brokerID == brokers.LocalBrokerName {
		return "", "", nil, nativeErrorf(pam.ErrIgnore, "")
	}

	sessionID, encryptionKey, err = m.startSession(brokerID, username)
	return brokerID, sessionID, encryptionKey, err
}

// chooseBroker returns the broker ID to use, via automatic or manual selection.
func (m nativeClient) chooseBroker(username string, availableBrokers []*authd.ABResponse_BrokerInfo) (string, error) {
	// Try automatic selection (previously used broker).
	r, err := m.client.GetPreviousBroker(context.TODO(), &authd.GPBRequest{Username: username})
	if err == nil && r.GetPreviousBroker() != "" {
		return r.GetPreviousBroker(), nil
	}

	if len(availableBrokers) == 0 {
		return "", nativeErrorf(pam.ErrSystem, "%s", "no brokers available")
	}
	if len(availableBrokers) == 1 {
		return availableBrokers[0].Id, nil
	}

	var choices []choicePair
	for _, b := range availableBrokers {
		choices = append(choices, choicePair{id: b.Id, label: b.Name})
	}
	for {
		id, err := m.promptForChoice("Provider selection", choices, "Choose your provider")
		if errors.Is(err, errGoBack) {
			// Can't go back past broker selection; loop.
			continue
		}
		if err != nil {
			return "", nativeErrorf(pam.ErrSystem, "provider selection: %v", err)
		}
		return id, nil
	}
}

// getAvailableBrokers fetches the broker list from authd.
func (m nativeClient) getAvailableBrokers() ([]*authd.ABResponse_BrokerInfo, error) {
	resp, err := m.client.AvailableBrokers(context.TODO(), &authd.Empty{})
	if err != nil {
		return nil, nativeErrorf(pam.ErrSystem, "could not get available brokers: %v", err)
	}
	return resp.GetBrokersInfos(), nil
}

// startSession starts a broker session for the given user.
func (m nativeClient) startSession(brokerID, username string) (sessionID string, encryptionKey *rsa.PublicKey, err error) {
	lang := "C"
	for _, e := range []string{"LANG", "LC_MESSAGES", "LC_ALL"} {
		if l := os.Getenv(e); l != "" {
			lang = l
		}
	}
	lang = strings.TrimSuffix(lang, ".UTF-8")

	resp, err := m.client.SelectBroker(context.TODO(), &authd.SBRequest{
		BrokerId: brokerID,
		Username: username,
		Lang:     lang,
		Mode:     m.mode,
	})
	if err != nil {
		return "", nil, nativeErrorf(pam.ErrSystem, "%s", err.Error())
	}
	sessionID = resp.GetSessionId()
	if sessionID == "" {
		return "", nil, nativeErrorf(pam.ErrSystem, "%s", "no session ID returned by broker")
	}
	encryptionKeyStr := resp.GetEncryptionKey()
	if encryptionKeyStr == "" {
		return "", nil, nativeErrorf(pam.ErrSystem, "%s", "no encryption key returned by broker")
	}

	pubASN1, err := base64.StdEncoding.DecodeString(encryptionKeyStr)
	if err != nil {
		return "", nil, nativeErrorf(pam.ErrSystem, "encryption key sent by broker is not a valid base64 encoded string: %v", err)
	}
	pubKey, err := x509.ParsePKIXPublicKey(pubASN1)
	if err != nil {
		return "", nil, nativeErrorf(pam.ErrSystem, "encryption key sent by broker is not valid: %v", err)
	}
	rsaKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return "", nil, nativeErrorf(pam.ErrSystem, "expected RSA public key from broker, got %T", pubKey)
	}
	return sessionID, rsaKey, nil
}

// authLoop runs one complete authentication round (select mode -> challenge -> result).
// Returns (done=true, result, nil) when finished, (done=false, _, nil) on auth.Next,
// or (_, _, err) on hard errors.
func (m nativeClient) authLoop(sessionID, brokerID string, encryptionKey *rsa.PublicKey) (done bool, result PamReturnStatus, err error) {
	authModes, err := m.getAuthModes(sessionID)
	if err != nil {
		return true, nil, err
	}

	selectedModeID, err := m.chooseAuthMode(authModes)
	if err != nil {
		return true, nil, err
	}

	uiLayout, err := m.getLayout(sessionID, selectedModeID)
	if err != nil {
		return true, nil, err
	}

	// Challenge loop: retry on auth.Retry, reselectAuthMode on nil item.
	for {
		item, err := m.collectChallengeInput(selectedModeID, authModes, uiLayout)
		if err != nil {
			return true, nil, err
		}
		if item == nil {
			// Reselect auth mode: re-fetch layout and retry from top.
			selectedModeID, err = m.chooseAuthMode(authModes)
			if err != nil {
				return true, nil, err
			}
			uiLayout, err = m.getLayout(sessionID, selectedModeID)
			if err != nil {
				return true, nil, err
			}
			continue
		}

		// Encrypt the secret if present.
		if secret, ok := item.(*authd.IARequest_AuthenticationData_Secret); ok {
			ciphertext, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, encryptionKey, []byte(secret.Secret), nil)
			if err != nil {
				return true, nil, nativeErrorf(pam.ErrSystem, "failed to encrypt secret: %v", err)
			}
			item = &authd.IARequest_AuthenticationData_Secret{
				Secret: base64.StdEncoding.EncodeToString(ciphertext),
			}
		}

		resp, err := m.client.IsAuthenticated(context.TODO(), &authd.IARequest{
			SessionId:          sessionID,
			AuthenticationData: &authd.IARequest_AuthenticationData{Item: item},
		})
		if err != nil {
			return true, nil, nativeErrorf(pam.ErrSystem, "%s", err.Error())
		}

		msg, err := dataToMsg(resp.GetMsg())
		if err != nil {
			return true, nil, nativeErrorf(pam.ErrSystem, "%s", err.Error())
		}

		switch resp.GetAccess() {
		case auth.Granted:
			if err := m.sendInfo(msg); err != nil {
				return true, nil, err
			}
			return true, PamSuccess{BrokerID: brokerID, msg: msg}, nil

		case auth.Denied:
			if msg == "" {
				msg = "Access denied"
			}
			return true, pamError{status: pam.ErrAuth, msg: msg}, nil

		case auth.Next:
			if err := m.sendInfo(msg); err != nil {
				return true, nil, err
			}
			return false, nil, nil // caller will start a new session

		case auth.Retry:
			if err := m.sendError(msg); err != nil {
				return true, nil, err
			}
			continue // retry with same layout

		case auth.Cancelled:
			return true, pamError{status: pam.ErrAuth, msg: "Authentication cancelled"}, nil

		default:
			return true, nil, nativeErrorf(pam.ErrSystem, "unknown access type: %q", resp.GetAccess())
		}
	}
}

// getAuthModes fetches the authentication modes available for this session.
func (m nativeClient) getAuthModes(sessionID string) ([]*authd.GAMResponse_AuthenticationMode, error) {
	resp, err := m.client.GetAuthenticationModes(context.Background(), &authd.GAMRequest{
		SessionId:          sessionID,
		SupportedUiLayouts: m.supportedUILayouts(),
	})
	if err != nil {
		return nil, nativeErrorf(pam.ErrSystem, "%s", err.Error())
	}
	authModes := resp.GetAuthenticationModes()
	if len(authModes) == 0 {
		return nil, nativeErrorf(pam.ErrCredUnavail, "%s", "no supported authentication mode available for this provider")
	}
	return authModes, nil
}

// chooseAuthMode returns the auth mode ID to use, auto-selecting if only one.
func (m nativeClient) chooseAuthMode(authModes []*authd.GAMResponse_AuthenticationMode) (string, error) {
	if len(authModes) == 1 {
		return authModes[0].Id, nil
	}
	var choices []choicePair
	for _, am := range authModes {
		choices = append(choices, choicePair{id: am.Id, label: am.Label})
	}
	for {
		id, err := m.promptForChoice("Authentication method selection", choices, "Choose your authentication method")
		if errors.Is(err, errGoBack) {
			continue
		}
		if err != nil {
			return "", nativeErrorf(pam.ErrSystem, "authentication method selection: %v", err)
		}
		return id, nil
	}
}

// getLayout fetches the UI layout for the given auth mode.
func (m nativeClient) getLayout(sessionID, authModeID string) (*authd.UILayout, error) {
	resp, err := m.client.SelectAuthenticationMode(context.TODO(), &authd.SAMRequest{
		SessionId:            sessionID,
		AuthenticationModeId: authModeID,
	})
	if err != nil {
		return nil, nativeErrorf(pam.ErrSystem, "can't select authentication mode: %v", err)
	}
	if resp.UiLayoutInfo == nil {
		return nil, nativeErrorf(pam.ErrSystem, "%s", "invalid empty UI Layout information from broker")
	}
	return resp.GetUiLayoutInfo(), nil
}

// supportedUILayouts returns the list of UI layouts this native client supports.
func (m nativeClient) supportedUILayouts() []*authd.UILayout {
	required, optional := layouts.Required, layouts.Optional
	supportedEntries := layouts.OptionalItems(
		entries.Chars,
		entries.CharsPassword,
		entries.Digits,
		entries.DigitsPassword,
	)

	ls := []*authd.UILayout{
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
	}

	if m.serviceName != polkitServiceName {
		rendersQrCode := m.isQrcodeRenderingSupported()
		ls = append(ls, &authd.UILayout{
			Type:          layouts.QrCode,
			Content:       &required,
			Code:          &optional,
			Wait:          &layouts.RequiredWithBooleans,
			Label:         &optional,
			Button:        &optional,
			RendersQrcode: &rendersQrCode,
		})
	}
	return ls
}

// collectChallengeInput collects user input for the given UI layout.
// Returns nil item to signal that the auth mode should be reselected.
func (m nativeClient) collectChallengeInput(modeID string, authModes []*authd.GAMResponse_AuthenticationMode, layout *authd.UILayout) (authd.IARequestAuthenticationDataItem, error) {
	hasWait := layout.GetWait() == layouts.True
	switch layout.Type {
	case layouts.Form:
		return m.collectFormInput(modeID, authModes, layout, hasWait)
	case layouts.QrCode:
		if !hasWait {
			return nil, nativeErrorf(pam.ErrSystem, "%s", "can't handle qrcode without waiting")
		}
		return m.collectQrCodeInput(modeID, authModes, layout)
	case layouts.NewPassword:
		return m.collectNewPasswordInput(modeID, authModes, layout)
	default:
		return nil, nativeErrorf(pam.ErrSystem, "unknown layout type: %q", layout.Type)
	}
}

func (m nativeClient) modeLabel(modeID string, authModes []*authd.GAMResponse_AuthenticationMode, fallback string) string {
	idx := slices.IndexFunc(authModes, func(am *authd.GAMResponse_AuthenticationMode) bool {
		return am.Id == modeID
	})
	if idx < 0 {
		return fallback
	}
	return authModes[idx].Label
}

func (m nativeClient) collectFormInput(modeID string, authModes []*authd.GAMResponse_AuthenticationMode, layout *authd.UILayout, hasWait bool) (authd.IARequestAuthenticationDataItem, error) {
	authMode := m.modeLabel(modeID, authModes, "Authentication")

	if buttonLabel := layout.GetButton(); buttonLabel != "" {
		choices := []choicePair{
			{id: "continue", label: fmt.Sprintf("Proceed with %s", authMode)},
			{id: layouts.Button, label: buttonLabel},
		}
		id, err := m.promptForChoice(authMode, choices, "Choose action")
		if errors.Is(err, errGoBack) || errors.Is(err, errEmptyResponse) {
			return m.collectFormInput(modeID, authModes, layout, hasWait)
		}
		if err != nil {
			return nil, err
		}
		if id == layouts.Button {
			return nil, nil // reselect auth mode
		}
	}

	prompt := strings.TrimSuffix(layout.GetLabel(), ":")
	if prompt == "" {
		return nil, nativeErrorf(pam.ErrSystem, "no label provided for entry %q", layout.GetEntry())
	}

	if hasWait {
		instructions := "Leave the input field empty to wait for the alternative authentication method"
		if layout.GetEntry() == "" {
			instructions = "Press Enter to wait for authentication"
		}
		if err := m.sendInfo("== %s ==\n%s", authMode, instructions); err != nil {
			return nil, err
		}
	} else {
		if err := m.sendInfo("== %s ==", authMode); err != nil {
			return nil, err
		}
	}

	secret, err := m.promptForSecret(layout, prompt)
	if errors.Is(err, errGoBack) {
		return m.collectFormInput(modeID, authModes, layout, hasWait)
	}
	if errors.Is(err, errEmptyResponse) && hasWait {
		return &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True}, nil
	}
	if err != nil {
		return nil, err
	}
	return &authd.IARequest_AuthenticationData_Secret{Secret: secret}, nil
}

func (m nativeClient) collectQrCodeInput(modeID string, authModes []*authd.GAMResponse_AuthenticationMode, layout *authd.UILayout) (authd.IARequestAuthenticationDataItem, error) {
	qrCode, err := qrcode.New(layout.GetContent(), qrcode.Medium)
	if err != nil {
		return nil, nativeErrorf(pam.ErrSystem, "can't generate qrcode: %v", err)
	}

	var qrcodeView []string
	qrcodeView = append(qrcodeView, layout.GetLabel())

	var firstQrCodeLine string
	if m.isQrcodeRenderingSupported() {
		rendered := m.renderQrCode(qrCode)
		qrcodeView = append(qrcodeView, rendered)
		firstQrCodeLine = strings.SplitN(rendered, "\n", 2)[0]
	}
	if firstQrCodeLine == "" {
		firstQrCodeLine = layout.GetContent()
	}

	qrcodeView = append(qrcodeView, centerString(layout.GetContent(), firstQrCodeLine))
	if code := layout.GetCode(); code != "" {
		qrcodeView = append(qrcodeView, centerString(code, firstQrCodeLine))
	}
	qrcodeView = append(qrcodeView, " ")

	choices := []choicePair{{id: layouts.Wait, label: "Wait for authentication result"}}
	if buttonLabel := layout.GetButton(); buttonLabel != "" {
		choices = append(choices, choicePair{id: layouts.Button, label: buttonLabel})
	}

	id, err := m.promptForChoiceWithMessage(m.modeLabel(modeID, authModes, "QR code"),
		strings.Join(qrcodeView, "\n"), choices, "Choose action")
	if errors.Is(err, errGoBack) || errors.Is(err, errEmptyResponse) {
		return &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True}, nil
	}
	if err != nil {
		return nil, err
	}
	if id == layouts.Button {
		return nil, nil // reselect auth mode
	}
	return &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True}, nil
}

func (m nativeClient) collectNewPasswordInput(modeID string, authModes []*authd.GAMResponse_AuthenticationMode, layout *authd.UILayout) (authd.IARequestAuthenticationDataItem, error) {
	if buttonLabel := layout.GetButton(); buttonLabel != "" {
		label := m.modeLabel(modeID, authModes, "Password Update")
		choices := []choicePair{
			{id: "continue", label: "Proceed with password update"},
			{id: layouts.Button, label: buttonLabel},
		}
		id, err := m.promptForChoice(label, choices, "Choose action")
		if errors.Is(err, errGoBack) || errors.Is(err, errEmptyResponse) {
			return m.collectNewPasswordInput(modeID, authModes, layout)
		}
		if err != nil {
			return nil, err
		}
		if id == layouts.Button {
			return &authd.IARequest_AuthenticationData_Skip{Skip: layouts.True}, nil
		}
	}
	return m.newPasswordChallenge(layout, nil)
}

func (m nativeClient) newPasswordChallenge(layout *authd.UILayout, previousPassword *string) (authd.IARequestAuthenticationDataItem, error) {
	if previousPassword == nil {
		if err := m.sendInfo("== Password Update =="); err != nil {
			return nil, err
		}
	}

	prompt := layout.GetLabel()
	if previousPassword != nil {
		prompt = "Confirm Password"
	}

	password, err := m.promptForSecret(layout, prompt)
	if errors.Is(err, errGoBack) {
		return m.newPasswordChallenge(layout, nil)
	}
	if err != nil && !errors.Is(err, errEmptyResponse) {
		return nil, err
	}

	if previousPassword == nil {
		if err := checkPasswordQuality("", password); err != nil {
			if sendErr := m.sendError(err.Error()); sendErr != nil {
				return nil, sendErr
			}
			return m.newPasswordChallenge(layout, nil)
		}
		return m.newPasswordChallenge(layout, &password)
	}

	if password != *previousPassword {
		if err := m.sendError("Password entries don't match"); err != nil {
			return nil, err
		}
		return m.newPasswordChallenge(layout, nil)
	}
	return &authd.IARequest_AuthenticationData_Secret{Secret: password}, nil
}

// ---------- prompting helpers ----------

func (m nativeClient) checkForPromptReplyValidity(reply string) error {
	switch reply {
	case nativeCancelKey:
		return errGoBack
	case "", "\n":
		return errEmptyResponse
	}
	return nil
}

func (m nativeClient) promptForInput(style pam.Style, inputStyle inputPromptStyle, prompt string) (string, error) {
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

func (m nativeClient) promptForNumericInput(style pam.Style, prompt string) (int, error) {
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

func (m nativeClient) promptForNumericInputUntilValid(style pam.Style, prompt string) (int, error) {
	value, err := m.promptForNumericInput(style, prompt)
	if !errors.Is(err, errNotAnInteger) {
		return value, err
	}
	if err := m.sendError("Unsupported input"); err != nil {
		return -1, err
	}
	return m.promptForNumericInputUntilValid(style, prompt)
}

func (m nativeClient) promptForNumericInputAsString(style pam.Style, prompt string) (string, error) {
	input, err := m.promptForNumericInputUntilValid(style, prompt)
	return fmt.Sprint(input), err
}

func (m nativeClient) sendError(errorMsg string) error {
	if errorMsg == "" {
		return nil
	}
	_, err := m.pamMTx.StartStringConvf(pam.ErrorMsg, errorMsg)
	return err
}

func (m nativeClient) sendInfo(infoMsg string, args ...any) error {
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

func (m nativeClient) promptForChoiceWithMessage(title string, message string, choices []choicePair, prompt string) (string, error) {
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
	for {
		if err := m.sendInfo(msg); err != nil {
			return "", err
		}
		idx, err := m.promptForNumericInputUntilValid(pam.PromptEchoOn, prompt)
		if err != nil {
			return "", err
		}
		if idx < 1 || idx > len(choices) {
			if err := m.sendError("Invalid selection"); err != nil {
				return "", err
			}
			continue
		}
		return choices[idx-1].id, nil
	}
}

func (m nativeClient) promptForChoice(title string, choices []choicePair, prompt string) (string, error) {
	return m.promptForChoiceWithMessage(title, "", choices, prompt)
}

func (m nativeClient) promptForSecret(layout *authd.UILayout, prompt string) (string, error) {
	switch layout.GetEntry() {
	case entries.Chars, "":
		return m.promptForInput(pam.PromptEchoOn, inputPromptStyleMultiLine, prompt)
	case entries.CharsPassword:
		return m.promptForInput(pam.PromptEchoOff, inputPromptStyleMultiLine, prompt)
	case entries.Digits:
		return m.promptForNumericInputAsString(pam.PromptEchoOn, prompt)
	case entries.DigitsPassword:
		return m.promptForNumericInputAsString(pam.PromptEchoOff, prompt)
	default:
		return "", fmt.Errorf("unhandled entry %q", layout.GetEntry())
	}
}

// ---------- QR code helpers ----------

func (m nativeClient) renderQrCode(qrCode *qrcode.QRCode) (qr string) {
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

func (m nativeClient) isQrcodeRenderingSupported() bool {
	switch m.serviceName {
	case polkitServiceName:
		return false
	default:
		return !isSSHSession(m.pamMTx) && IsTerminalTTY(m.pamMTx)
	}
}

func centerString(s string, reference string) string {
	sizeDiff := len([]rune(reference)) - len(s)
	if sizeDiff <= 0 {
		return s
	}
	padding := strings.Repeat(" ", sizeDiff/2)
	return padding + s + padding
}

// ---------- error helpers ----------

// nativeError wraps a pamError so it satisfies the error interface and can be
// returned from internal helper functions.  At the boundary, pamReturnErrorFrom
// unwraps it back into a PamReturnStatus.
type nativeError struct{ pamError }

func (e nativeError) Error() string { return e.Message() }

func nativeErrorf(status pam.Error, format string, args ...any) error {
	return nativeError{pamError{status: status, msg: fmt.Sprintf(format, args...)}}
}

func pamReturnErrorFrom(err error) PamReturnStatus {
	var ne nativeError
	if errors.As(err, &ne) {
		return ne.pamError
	}
	return pamError{status: pam.ErrSystem, msg: err.Error()}
}
