package adapter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	pam_proto "github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/msteinert/pam/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// cancellationWait is the time that we are waiting for the cancellation to be
	// delivered to the brokers, but also it's used to compute the time we should
	// wait for the fully cancellation to have completed once delivered.
	cancellationWait = time.Millisecond * 10
)

var (
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
)

// sendIsAuthenticated sends the authentication secrets or wait request to the brokers.
// The event will contain the returned value from the broker.
func sendIsAuthenticated(ctx context.Context, client authd.PAMClient, sessionID string,
	authData *authd.IARequest_AuthenticationData, secret *string) tea.Cmd {
	return func() (msg tea.Msg) {
		log.Debugf(context.TODO(), "Authentication request for session %q: %#v",
			sessionID, authData.Item)
		defer func() {
			log.Debugf(context.TODO(), "Authentication completed for session %q: %#v",
				sessionID, msg)
		}()

		res, err := client.IsAuthenticated(ctx, &authd.IARequest{
			SessionId:          sessionID,
			AuthenticationData: authData,
		})
		if err != nil {
			if st := status.Convert(err); st.Code() == codes.Canceled {
				// Note that this error is only the client-side error, so being here doesn't
				// mean the cancellation on broker side is fully completed.

				// Wait for the cancellation requests to have been delivered and actually handled.
				// The multiplier can be increased to avoid that we return the cancelled event too
				// early, but it implies slowing down the UI responses.
				<-time.After(cancellationWait * 3)

				return isAuthenticatedResultReceived{
					access: auth.Cancelled,
					secret: secret,
				}
			}
			return pamError{
				status: pam.ErrSystem,
				msg:    err.Error(),
			}
		}

		return isAuthenticatedResultReceived{
			access: res.Access,
			msg:    res.Msg,
			secret: secret,
		}
	}
}

// isAuthenticatedRequested is the internal events signalling that authentication
// with the given password or wait has been requested.
type isAuthenticatedRequested struct {
	item authd.IARequestAuthenticationDataItem
}

// isAuthenticatedRequestedSend is the internal event signaling that the authentication
// request should be sent to the broker.
type isAuthenticatedRequestedSend struct {
	isAuthenticatedRequested
	ctx context.Context
}

// isAuthenticatedResultReceived is the internal event with the authentication access result
// and data that was retrieved.
type isAuthenticatedResultReceived struct {
	access string
	secret *string
	msg    string
}

// isAuthenticatedCancelled is the event to cancel the auth request.
type isAuthenticatedCancelled struct{}

// reselectAuthMode signals to restart auth mode selection with the same id (to resend sms or
// reenable the broker).
type reselectAuthMode struct{}

// authenticationComponent is the interface that all sub layout models needs to match.
type authenticationComponent interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (tea.Model, tea.Cmd)
	View() string
	Focus() tea.Cmd
	Focused() bool
	Blur()
}

// authenticationModel is the orchestrator model of all the authentication sub model layouts.
type authenticationModel struct {
	client     authd.PAMClient
	clientType PamClientType
	mode       authd.SessionMode

	inProgress       bool
	currentModel     authenticationComponent
	currentSessionID string
	currentBrokerID  string
	currentSecret    string
	currentLayout    string

	authTracker *authTracker

	encryptionKey *rsa.PublicKey

	errorMsg string
}

type authTracker struct {
	cancelFunc func()
	cond       *sync.Cond
}

// startAuthentication signals that the authentication model can start
// wait:true authentication and reset fields.
type startAuthentication struct{}

// startAuthentication signals that the authentication has been stopped.
type stopAuthentication struct{}

// errMsgToDisplay signals from an authentication form to display an error message.
type errMsgToDisplay struct {
	msg string
}

// newPasswordCheck is sent to request a new password quality check.
type newPasswordCheck struct {
	ctx      context.Context
	password string
}

// newPasswordCheckResult returns the password quality check result.
type newPasswordCheckResult struct {
	ctx      context.Context
	password string
	msg      string
}

// newAuthenticationModel initializes a authenticationModel which needs to be Compose then.
func newAuthenticationModel(client authd.PAMClient, clientType PamClientType, mode authd.SessionMode) authenticationModel {
	return authenticationModel{
		client:      client,
		clientType:  clientType,
		mode:        mode,
		authTracker: &authTracker{cond: sync.NewCond(&sync.Mutex{})},
	}
}

// Init initializes authenticationModel.
func (m authenticationModel) Init() tea.Cmd {
	return nil
}

func (m *authenticationModel) cancelIsAuthenticated() tea.Cmd {
	authTracker := m.authTracker
	return func() tea.Msg {
		authTracker.cancelAndWait()
		return stopAuthentication{}
	}
}

// Update handles events and actions.
func (m authenticationModel) Update(msg tea.Msg) (authModel authenticationModel, command tea.Cmd) {
	switch msg := msg.(type) {
	case StageChanged:
		if msg.Stage != pam_proto.Stage_challenge {
			return m, nil
		}
		safeMessageDebug(msg, "in progress %v, focused: %v",
			m.inProgress, m.Focused())
		if m.inProgress || !m.Focused() {
			return m, nil
		}
		return m, sendEvent(startAuthentication{})

	case startAuthentication:
		safeMessageDebug(msg, "current model %v, focused %v",
			m.currentModel, m.Focused())
		if !m.Focused() {
			return m, nil
		}
		m.inProgress = true

	case stopAuthentication:
		safeMessageDebug(msg, "current model %v, focused %v",
			m.currentModel, m.Focused())
		m.inProgress = false

	case reselectAuthMode:
		safeMessageDebug(msg)
		return m, tea.Sequence(m.cancelIsAuthenticated(), sendEvent(AuthModeSelected{}))

	case newPasswordCheck:
		safeMessageDebug(msg)
		var oldPassword string
		if m.mode == authd.SessionMode_CHANGE_PASSWORD {
			// Only compare the new password with the current one if the session is for changing the password.
			// If the session is for authentication, we allow the user to set the same password again, to avoid
			// that the user is forced to change their password if e.g. device authentication is forced when
			// the refresh token is expired.
			oldPassword = m.currentSecret
		}

		return m, func() tea.Msg {
			res := newPasswordCheckResult{ctx: msg.ctx, password: msg.password}
			if err := checkPasswordQuality(oldPassword, msg.password); err != nil {
				res.msg = err.Error()
			}
			return res
		}

	case newPasswordCheckResult:
		safeMessageDebug(msg)
		if m.clientType != Gdm {
			// This may be handled by the current model, so don't return early.
			break
		}

		if msg.msg == "" {
			return m, sendEvent(isAuthenticatedRequestedSend{
				ctx: msg.ctx,
				isAuthenticatedRequested: isAuthenticatedRequested{
					item: &authd.IARequest_AuthenticationData_Secret{Secret: msg.password},
				},
			})
		}

		errMsg, err := json.Marshal(msg.msg)
		if err != nil {
			return m, sendEvent(pamError{
				status: pam.ErrSystem,
				msg:    fmt.Sprintf("could not encode %q error: %v", msg.msg, err),
			})
		}

		return m, sendEvent(isAuthenticatedResultReceived{
			access: auth.Retry,
			msg:    fmt.Sprintf(`{"message": %s}`, errMsg),
		})

	case isAuthenticatedRequested:
		safeMessageDebug(msg)

		authTracker := m.authTracker

		ctx, cancel := context.WithCancel(context.Background())
		cancelFunc := func() {
			// Very very ugly, but we need to ensure that IsAuthenticated call has been delivered
			// to the broker before calling broker's cancelIsAuthenticated or that cancel request may happen
			// before than the IsAuthenticated() one has been invoked, and thus we may have nothing
			// to cancel in the broker side.
			// So let's wait a bit in such case (we may be even too much generous), before delivering
			// the actual cancellation.
			<-time.After(cancellationWait)
			cancel()
		}

		// At the point that we proceed with the actual authentication request in the goroutine,
		// there may still an authentication in progress, so send the request only after
		// we've completed the previous one(s).
		clientType := m.clientType
		currentLayout := m.currentLayout
		return m, func() tea.Msg {
			authTracker.waitAndStart(cancelFunc)

			secret, hasSecret := msg.item.(*authd.IARequest_AuthenticationData_Secret)
			if hasSecret && clientType == Gdm && currentLayout == layouts.NewPassword {
				return newPasswordCheck{ctx: ctx, password: secret.Secret}
			}

			return isAuthenticatedRequestedSend{msg, ctx}
		}

	case isAuthenticatedRequestedSend:
		safeMessageDebug(msg)
		// no password value, pass it as is
		plainTextSecret, err := msg.encryptSecretIfPresent(m.encryptionKey)
		if err != nil {
			return m, sendEvent(pamError{status: pam.ErrSystem, msg: fmt.Sprintf("could not encrypt password payload: %v", err)})
		}

		return m, sendIsAuthenticated(msg.ctx, m.client, m.currentSessionID, &authd.IARequest_AuthenticationData{Item: msg.item}, plainTextSecret)

	case isAuthenticatedCancelled:
		safeMessageDebug(msg)
		return m, m.cancelIsAuthenticated()

	case isAuthenticatedResultReceived:
		safeMessageDebug(msg)

		// Resets password if the authentication wasn't successful.
		defer func() {
			// the returned authModel is a copy of function-level's `m` at this point!
			m := &authModel
			if msg.secret != nil &&
				(msg.access == auth.Granted || msg.access == auth.Next) {
				m.currentSecret = *msg.secret
			}

			if msg.access != auth.Next && msg.access != auth.Retry {
				m.currentModel = nil
			}
			m.authTracker.reset()
		}()

		var authMsg string
		if msg.access != auth.Cancelled {
			msg, err := dataToMsg(msg.msg)
			if err != nil {
				return m, sendEvent(pamError{status: pam.ErrSystem, msg: err.Error()})
			}
			authMsg = msg
		}

		switch msg.access {
		case auth.Granted:
			return m, sendEvent(PamSuccess{BrokerID: m.currentBrokerID, msg: authMsg})

		case auth.Retry:
			m.errorMsg = authMsg
			return m, sendEvent(startAuthentication{})

		case auth.Denied:
			if authMsg == "" {
				authMsg = "Access denied"
			}
			return m, sendEvent(pamError{status: pam.ErrAuth, msg: authMsg})

		case auth.Next:
			if authMsg != "" {
				m.errorMsg = authMsg

				// Give the user some time to read the message, if any, using
				const baseWPM = float64(120)
				const delay = 500 * time.Millisecond
				const extraTime = 1000 * time.Millisecond
				words := len(strings.Fields(m.errorMsg))
				readTime := time.Duration(float64(words)/baseWPM*60) * time.Second
				userReadTime := delay + readTime + extraTime
				return m, tea.Tick(userReadTime, func(t time.Time) tea.Msg {
					return GetAuthenticationModesRequested{}
				})
			}
			return m, sendEvent(GetAuthenticationModesRequested{})

		case auth.Cancelled:
			// nothing to do
			return m, nil
		}

	case errMsgToDisplay:
		m.errorMsg = msg.msg
		return m, nil
	}

	if m.clientType != InteractiveTerminal {
		return m, nil
	}

	// interaction events
	if !m.Focused() {
		return m, nil
	}

	var cmd tea.Cmd
	var model tea.Model
	if m.currentModel != nil {
		model, cmd = m.currentModel.Update(msg)
		m.currentModel = convertTo[authenticationComponent](model)
	}
	return m, cmd
}

// Focus focuses this model.
func (m authenticationModel) Focus() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Focus, focused %v", m, m.Focused())
	if m.currentModel == nil {
		return nil
	}

	if m.Focused() {
		// This is in the case of re-authentication or next, as the stage has
		// not been changed and we are already focused.
		return sendEvent(startAuthentication{})
	}

	return m.currentModel.Focus()
}

// Focused returns if this model is focused.
func (m authenticationModel) Focused() bool {
	if m.currentModel == nil {
		return false
	}
	return m.currentModel.Focused()
}

// Blur releases the focus from this model.
func (m *authenticationModel) Blur() {
	log.Debugf(context.TODO(), "%T: Blur", m)
	if m.currentModel == nil {
		return
	}
	m.currentModel.Blur()
}

// Compose initialize the authentication model to be used.
// It creates and attaches the sub layout models based on UILayout.
func (m *authenticationModel) Compose(brokerID, sessionID string, encryptionKey *rsa.PublicKey, layout *authd.UILayout) tea.Cmd {
	m.currentBrokerID = brokerID
	m.currentSessionID = sessionID
	m.encryptionKey = encryptionKey
	m.currentLayout = layout.Type

	m.errorMsg = ""

	if m.clientType != InteractiveTerminal {
		m.currentModel = &focusTrackerModel{}
		return sendEvent(ChangeStage{pam_proto.Stage_challenge})
	}

	switch layout.Type {
	case layouts.Form:
		form := newFormModel(layout.GetLabel(), layout.GetEntry(), layout.GetButton(), layout.GetWait() == layouts.True)
		m.currentModel = form

	case layouts.QrCode:
		qrcodeModel, err := newQRCodeModel(layout.GetContent(), layout.GetCode(),
			layout.GetLabel(), layout.GetButton(), layout.GetWait() == layouts.True)
		if err != nil {
			return sendEvent(pamError{status: pam.ErrSystem, msg: err.Error()})
		}
		m.currentModel = qrcodeModel

	case layouts.NewPassword:
		newPasswordModel := newNewPasswordModel(layout.GetLabel(), layout.GetEntry(), layout.GetButton())
		m.currentModel = newPasswordModel

	default:
		return sendEvent(pamError{
			status: pam.ErrSystem,
			msg:    fmt.Sprintf("unknown layout type: %q", layout.Type),
		})
	}

	return tea.Sequence(
		m.currentModel.Init(),
		sendEvent(ChangeStage{pam_proto.Stage_challenge}))
}

// View renders a text view of the authentication UI.
func (m authenticationModel) View() string {
	if !m.inProgress {
		return ""
	}
	if !m.Focused() {
		return ""
	}
	contents := []string{m.currentModel.View()}

	errMsg := m.errorMsg
	if errMsg != "" {
		contents = append(contents, errorStyle.Render(errMsg))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		contents...,
	)
}

// Resets zeroes any internal state on the authenticationModel.
func (m *authenticationModel) Reset() tea.Cmd {
	log.Debugf(context.TODO(), "%T: Reset", m)
	m.inProgress = false
	m.currentModel = nil
	m.currentSessionID = ""
	m.currentBrokerID = ""
	m.currentLayout = ""
	return m.cancelIsAuthenticated()
}

// dataToMsg returns the data message from a given JSON message.
func dataToMsg(data string) (string, error) {
	if data == "" {
		return "", nil
	}

	v := make(map[string]string)
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		return "", fmt.Errorf("invalid json data from provider: %v", err)
	}
	if len(v) == 0 {
		return "", nil
	}

	r, ok := v["message"]
	if !ok {
		return "", fmt.Errorf("no message entry in json data from provider: %v", v)
	}
	return r, nil
}

func (authData *isAuthenticatedRequestedSend) encryptSecretIfPresent(publicKey *rsa.PublicKey) (*string, error) {
	// no password value, pass it as is
	secret, ok := authData.item.(*authd.IARequest_AuthenticationData_Secret)
	if !ok {
		return nil, nil
	}

	ciphertext, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, publicKey, []byte(secret.Secret), nil)
	if err != nil {
		return nil, err
	}

	// encrypt it to base64 and replace the password with it
	base64Encoded := base64.StdEncoding.EncodeToString(ciphertext)
	authData.item = &authd.IARequest_AuthenticationData_Secret{Secret: base64Encoded}
	return &secret.Secret, nil
}

// wait waits for the current authentication to be completed.
func (at *authTracker) wait() {
	at.cond.L.Lock()
	defer at.cond.L.Unlock()

	for at.cancelFunc != nil {
		at.cond.Wait()
	}
}

// waitAndStart waits for the current authentication to be completed and
// marks the authentication as in progress.
func (at *authTracker) waitAndStart(cancelFunc func()) {
	at.cond.L.Lock()
	defer at.cond.L.Unlock()

	for at.cancelFunc != nil {
		at.cond.Wait()
	}

	at.cancelFunc = cancelFunc
}

func (at *authTracker) cancelAndWait() {
	at.cond.L.Lock()
	cancelFunc := at.cancelFunc
	at.cond.L.Unlock()
	if cancelFunc == nil {
		return
	}
	cancelFunc()
	at.wait()
}

func (at *authTracker) reset() {
	at.cond.L.Lock()
	defer at.cond.L.Unlock()
	at.cancelFunc = nil
	at.cond.Signal()
}
