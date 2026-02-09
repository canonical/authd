// Package adapter is the package for the PAM library
package adapter

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/msteinert/pam/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// PamClientType indicates the type of the PAM client we're handling.
type PamClientType int

const (
	// Native indicates a PAM Client that is not supporting any special protocol.
	Native PamClientType = iota
	// InteractiveTerminal indicates an interactive terminal we can directly write our interface to.
	InteractiveTerminal
	// Gdm is a gnome-shell client via GDM display manager.
	Gdm
)

var (
	debug string

	infoMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#909090",
		Dark:  "#7f7f7f",
	}).Padding(1, 0, 0, 2)
)

// sessionInfo contains the global broker session information.
type sessionInfo struct {
	brokerID      string
	sessionID     string
	encryptionKey *rsa.PublicKey
}

// uiModel is the global models orchestrator.
type uiModel struct {
	width int

	// pamMTx is the [pam.ModuleTransaction] used to communicate with PAM.
	pamMTx pam.ModuleTransaction
	// conn is the [grpc.ClientConn] opened with authd daemon.
	conn *grpc.ClientConn
	// PamClientType is the kind of the PAM client we're handling.
	clientType PamClientType
	// sessionMode is the mode of the session invoked by the module.
	sessionMode authd.SessionMode

	// client is the [authd.PAMClient] handle used to communicate with authd.
	client authd.PAMClient

	sessionStartingForBroker string
	currentSession           *sessionInfo

	healthCheckCancel      func()
	userSelectionModel     userSelectionModel
	brokerSelectionModel   brokerSelectionModel
	authModeSelectionModel authModeSelectionModel
	authenticationModel    authenticationModel
	gdmModel               gdmModel
	nativeModel            nativeModel

	// exitStatus is a pointer to the [PamReturnStatus] value where the
	// exit status will be written to.
	exitStatus *PamReturnStatus
}

/* global events */

// BrokerListReceived is received when we got the broker list.
type BrokerListReceived struct{}

// UsernameSelected is received when the user name is filled (from pam or manually).
type UsernameSelected struct{}

// BrokerSelected signifies that the broker has been chosen.
type BrokerSelected struct {
	BrokerID string
}

// SessionStarted signals that we started a session with a given broker.
type SessionStarted struct {
	brokerID      string
	sessionID     string
	encryptionKey string
}

// GetAuthenticationModesRequested signals that a model needs to get the broker authentication modes.
type GetAuthenticationModesRequested struct{}

// AuthModeSelected is triggered when the authentication mode has been chosen.
type AuthModeSelected struct {
	ID string
}

// UILayoutReceived means that we got the ui layout to display by the broker.
type UILayoutReceived struct {
	layout *authd.UILayout
}

// SessionEnded signals that the session is done and closed from the broker.
type SessionEnded struct{}

// ChangeStage signals that the model requires a stage change.
type ChangeStage struct {
	Stage proto.Stage
}

// StageChanged signals that the model just finished a stage change.
type StageChanged ChangeStage

// NewUIModel creates and initializes the main model orchestrator.
func NewUIModel(mTx pam.ModuleTransaction, clientType PamClientType, mode authd.SessionMode, conn *grpc.ClientConn, exitStatus *PamReturnStatus) tea.Model {
	var userServiceClient authd.UserServiceClient
	if conn != nil && isSSHSession(mTx) {
		userServiceClient = authd.NewUserServiceClient(conn)
	}

	m := newUIModelForClients(mTx, clientType, mode, authd.NewPAMClient(conn), userServiceClient, exitStatus)
	m.conn = conn
	return m
}

// newUIModelForClients is the internal implementation of [NewUIModel] for testing purposes.
func newUIModelForClients(mTx pam.ModuleTransaction, clientType PamClientType, mode authd.SessionMode, pamClient authd.PAMClient, userServiceClient authd.UserServiceClient, exitStatus *PamReturnStatus) uiModel {
	m := uiModel{
		pamMTx:      mTx,
		clientType:  clientType,
		sessionMode: mode,
		exitStatus:  exitStatus,
		client:      pamClient,
	}

	if m.exitStatus != nil {
		*m.exitStatus = errNoExitStatus
	}

	switch m.clientType {
	case Gdm:
		m.gdmModel = gdmModel{pamMTx: m.pamMTx}
	case Native:
		m.nativeModel = newNativeModel(m.pamMTx, userServiceClient)
	}

	m.userSelectionModel = newUserSelectionModel(m.pamMTx, m.clientType)
	m.brokerSelectionModel = newBrokerSelectionModel(m.client, m.clientType)
	m.authModeSelectionModel = newAuthModeSelectionModel(m.clientType)
	m.authenticationModel = newAuthenticationModel(m.client, m.clientType, mode)
	m.healthCheckCancel = func() {}

	return m
}

// Init initializes the main model orchestrator.
func (m uiModel) Init() tea.Cmd {
	var cmds []tea.Cmd

	if m.client == nil {
		return sendEvent(pamError{
			status: pam.ErrAbort,
			msg:    "No PAM client set",
		})
	}

	switch m.clientType {
	case Gdm:
		cmds = append(cmds, m.gdmModel.Init())
	case Native:
		cmds = append(cmds, m.nativeModel.Init())
	}

	cmds = append(cmds, m.userSelectionModel.Init())
	cmds = append(cmds, m.brokerSelectionModel.Init())
	cmds = append(cmds, m.authModeSelectionModel.Init())
	cmds = append(cmds, m.authenticationModel.Init())
	cmds = append(cmds, sendEvent(initHealthCheck{}))

	return tea.Batch(cmds...)
}

type initHealthCheck struct{}

func (m *uiModel) startHealthCheck() tea.Cmd {
	if m.conn == nil {
		return nil
	}

	var ctx context.Context
	ctx, m.healthCheckCancel = context.WithCancel(context.Background())
	healthClient := healthgrpc.NewHealthClient(m.conn)
	hcReq := &healthgrpc.HealthCheckRequest{Service: consts.ServiceName}

	return func() tea.Msg {
		for {
			r, err := healthClient.Check(ctx, hcReq)
			if status.Convert(err).Code() == codes.Canceled {
				return nil
			}
			if err != nil {
				log.Errorf(ctx, "Health check failed: %v", err)
				// We just consider this as a serving failure, without writing the whole error.
				r = &healthgrpc.HealthCheckResponse{
					Status: healthgrpc.HealthCheckResponse_NOT_SERVING,
				}
			}
			if r.Status != healthgrpc.HealthCheckResponse_SERVING {
				return pamError{
					status: pam.ErrSystem,
					msg:    fmt.Sprintf("%s stopped serving", m.conn.Target()),
				}
			}

			<-time.After(500 * time.Millisecond)
		}
	}
}

// Update handles events and actions to be done from the main model orchestrator.
func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	// Key presses
	case tea.KeyMsg:
		safeMessageDebugWithPrefix("Key", msg, "in stage %q", m.currentStage())

		switch msg.String() {
		case "ctrl+c":
			return m, sendEvent(pamError{
				status: pam.ErrAbort,
				msg:    "cancel requested",
			})
		case "esc":
			if !m.canGoBack() {
				return m, nil
			}
			return m, sendEvent(ChangeStage{m.previousStage()})
		}

	case initHealthCheck:
		safeMessageDebug(msg)
		return m, m.startHealthCheck()

	// Exit cases
	case PamReturnStatus:
		safeMessageDebug(msg)
		if m.exitStatus == nil {
			return m, m.quit()
		}
		if *m.exitStatus != errNoExitStatus {
			// Nothing to do, we're already exiting...
			return m, nil
		}
		*m.exitStatus = msg
		return m, m.quit()

	// Events
	case BrokerListReceived:
		safeMessageDebug(msg, "brokers: %#v", m.availableBrokers())
		if m.availableBrokers() == nil {
			return m, nil
		}
		return m, m.userSelectionModel.SelectUser()

	case UsernameSelected:
		safeMessageDebug(msg, "user: %q", m.username())
		if m.username() == "" {
			return m, nil
		}

		// Got user and brokers? Time to auto or manually select.
		return m, AutoSelectForUser(m.client, m.username())

	case BrokerSelected:
		safeMessageDebug(msg)
		if m.sessionStartingForBroker == "" {
			m.sessionStartingForBroker = msg.BrokerID
			return m, startBrokerSession(m.client, msg.BrokerID, m.username(), m.sessionMode)
		}
		if m.sessionStartingForBroker != msg.BrokerID {
			return m, tea.Sequence(endSession(m.client, m.currentSession), sendEvent(msg))
		}
	case SessionStarted:
		safeMessageDebug(msg)
		m.sessionStartingForBroker = ""
		pubASN1, err := base64.StdEncoding.DecodeString(msg.encryptionKey)
		if err != nil {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    fmt.Sprintf("encryption key sent by broker is not a valid base64 encoded string: %v", err),
			})
		}

		pubKey, err := x509.ParsePKIXPublicKey(pubASN1)
		if err != nil {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    fmt.Sprintf("encryption key send by broker is not valid: %v", err),
			})
		}
		rsaPublicKey, ok := pubKey.(*rsa.PublicKey)
		if !ok {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    fmt.Sprintf("expected encryption key sent by broker to be  RSA public key, got %T", pubKey),
			})
		}

		m.currentSession = &sessionInfo{
			brokerID:      msg.brokerID,
			sessionID:     msg.sessionID,
			encryptionKey: rsaPublicKey,
		}
		return m, sendEvent(GetAuthenticationModesRequested{})

	case ChangeStage:
		safeMessageDebug(msg)
		return m, m.changeStage(msg.Stage)

	case StageChanged:
		safeMessageDebug(msg)

	case GetAuthenticationModesRequested:
		safeMessageDebug(msg)
		if m.currentSession == nil {
			return m, nil
		}

		return m, tea.Sequence(
			getAuthenticationModes(m.client, m.currentSession.sessionID, m.authModeSelectionModel.SupportedUILayouts()),
			sendEvent(ChangeStage{proto.Stage_authModeSelection}),
		)

	case AuthModeSelected:
		safeMessageDebug(msg)
		if m.currentSession == nil {
			return m, nil
		}
		// Reselection/reset of current authentication mode requested (button clicked for instance)
		if msg.ID == "" {
			msg.ID = m.authModeSelectionModel.currentAuthModeSelectedID
		}
		if msg.ID == "" {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    "reselection of current auth mode without current ID",
			})
		}
		return m, tea.Sequence(
			m.updateClientModel(msg),
			getLayout(m.client, m.currentSession.sessionID, msg.ID),
		)

	case UILayoutReceived:
		safeMessageDebug(msg)
		if m.currentSession == nil {
			return m, nil
		}

		return m, tea.Sequence(
			m.authenticationModel.Compose(
				m.currentSession.brokerID,
				m.currentSession.sessionID,
				m.currentSession.encryptionKey,
				msg.layout,
			),
			m.updateClientModel(msg),
		)

	case SessionEnded:
		safeMessageDebug(msg)
		m.sessionStartingForBroker = ""
		m.currentSession = nil
		return m, nil
	}

	var cmd tea.Cmd
	var cmds tea.BatchMsg
	m.userSelectionModel, cmd = m.userSelectionModel.Update(msg)
	cmds = append(cmds, cmd)
	m.brokerSelectionModel, cmd = m.brokerSelectionModel.Update(msg)
	cmds = append(cmds, cmd)
	m.authModeSelectionModel, cmd = m.authModeSelectionModel.Update(msg)
	cmds = append(cmds, cmd)
	m.authenticationModel, cmd = m.authenticationModel.Update(msg)
	cmds = append(cmds, cmd)

	cmds = append(cmds, m.updateClientModel(msg))

	return m, tea.Batch(cmds...)
}

func (m *uiModel) updateClientModel(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.clientType {
	case Gdm:
		m.gdmModel, cmd = m.gdmModel.Update(msg)
	case Native:
		m.nativeModel, cmd = m.nativeModel.Update(msg)
	}
	return cmd
}

// View renders a text view of the whole UI.
func (m uiModel) View() string {
	if m.clientType != InteractiveTerminal {
		return ""
	}

	var viewBuilder strings.Builder

	switch m.currentStage() {
	case proto.Stage_userSelection:
		viewBuilder.WriteString(m.userSelectionModel.View())
	case proto.Stage_brokerSelection:
		viewBuilder.WriteString(m.brokerSelectionModel.View())
	case proto.Stage_authModeSelection:
		viewBuilder.WriteString(m.authModeSelectionModel.View())
	case proto.Stage_challenge:
		viewBuilder.WriteString(m.authenticationModel.View())
	default:
		viewBuilder.WriteString("INVALID STAGE")
	}

	if debug != "" {
		viewBuilder.WriteString(debug)
	}

	view := viewBuilder.String()

	if len(view) > 0 && m.canGoBack() {
		infoMessage := infoMsgStyle.Render(fmt.Sprintf("Press escape key to %s",
			goBackLabel(m.previousStage())))
		view = lipgloss.JoinVertical(lipgloss.Left, view, infoMessage)
	}

	if m.width == 0 {
		return view
	}
	// Wrap the view to the terminal width.
	return lipgloss.NewStyle().Width(m.width).Render(view)
}

// currentStage returns our current stage step.
func (m uiModel) currentStage() proto.Stage {
	if m.userSelectionModel.Focused() {
		return proto.Stage_userSelection
	}
	if m.brokerSelectionModel.Focused() {
		return proto.Stage_brokerSelection
	}
	if m.authModeSelectionModel.Focused() {
		return proto.Stage_authModeSelection
	}
	if m.authenticationModel.Focused() {
		return proto.Stage_challenge
	}
	return proto.Stage_userSelection
}

// changeStage returns a command acting to change the current stage and reset any previous views.
func (m *uiModel) changeStage(s proto.Stage) tea.Cmd {
	var commands []tea.Cmd
	currentStage := m.currentStage()

	if currentStage != s {
		switch currentStage {
		case proto.Stage_userSelection:
			m.userSelectionModel.Blur()
		case proto.Stage_brokerSelection:
			m.brokerSelectionModel.Blur()
		case proto.Stage_authModeSelection:
			m.authModeSelectionModel.Blur()
		case proto.Stage_challenge:
			m.authenticationModel.Blur()
			commands = append(commands, m.authenticationModel.Reset())
		}
	}

	switch s {
	case proto.Stage_userSelection:
		// The session should be ended when going back to previous state, but we don’t quit the stage immediately
		// and so, we should always ensure we cancel previous session.
		commands = append(commands, endSession(m.client, m.currentSession), m.userSelectionModel.Focus())

	case proto.Stage_brokerSelection:
		m.authModeSelectionModel.Reset()
		commands = append(commands, endSession(m.client, m.currentSession), m.brokerSelectionModel.Focus())

	case proto.Stage_authModeSelection:
		commands = append(commands, m.authModeSelectionModel.Focus())

	case proto.Stage_challenge:
		commands = append(commands, m.authenticationModel.Focus())

	default:
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("unknown PAM stage: %q", s),
		})
	}

	if currentStage != s {
		commands = append(commands, sendEvent(StageChanged{s}))
	}

	return tea.Sequence(commands...)
}

func (m uiModel) previousStage() proto.Stage {
	currentStage := m.currentStage()
	if currentStage > proto.Stage_authModeSelection && len(m.availableAuthModes()) > 1 {
		return proto.Stage_authModeSelection
	}
	if currentStage > proto.Stage_brokerSelection && len(m.availableBrokers()) > 1 {
		return proto.Stage_brokerSelection
	}
	return proto.Stage_userSelection
}

func (m uiModel) canGoBack() bool {
	if m.userSelectionModel.Enabled() {
		return m.currentStage() > proto.Stage_userSelection
	}
	return m.previousStage() > proto.Stage_userSelection
}

// MsgFilter is the handler for the UI model.
func MsgFilter(model tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.QuitMsg); !ok {
		return msg
	}

	m := convertTo[uiModel](model)
	m.healthCheckCancel()

	if m.clientType == Gdm && !m.gdmModel.conversationsStopped {
		return tea.Sequence(sendEvent(gdmStopConversations{}), sendEvent(msg))()
	}

	return msg
}

var errNoExitStatus = pamError{status: pam.ErrSystem, msg: "model did not return anything"}

// username returns currently selected user name.
func (m uiModel) username() string {
	return m.userSelectionModel.Username()
}

// availableBrokers returns currently available brokers.
func (m uiModel) availableBrokers() []*authd.ABResponse_BrokerInfo {
	return m.brokerSelectionModel.availableBrokers
}

// availableBrokers returns currently available brokers.
func (m uiModel) availableAuthModes() []*authd.GAMResponse_AuthenticationMode {
	return m.authModeSelectionModel.availableAuthModes
}
