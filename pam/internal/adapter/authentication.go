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
				// We still wait briefly so that the CancelIsAuthenticated D-Bus call (sent
				// by the broker layer when ctx is cancelled) has time to arrive at the broker
				// after the IsAuthenticated call — not before it.  Serialisation of
				// back-to-back IsAuthenticated calls for the same session is handled
				// server-side, so we no longer need a longer delay here.
				<-time.After(cancellationWait)

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

// authTracker serialises IsAuthenticated calls and supports cancellation.
//
// At most one authentication goroutine is in flight at a time. A goroutine
// that arrives while another is running blocks until the running one finishes.
// cancelAndWait() cancels any in-flight goroutine and bumps the generation
// counter so that any goroutine that is waiting (or has just been woken) knows
// it has been superseded and must abort without making the RPC.
type authTracker struct {
	mu         sync.Mutex
	generation uint64        // incremented by cancelAndWait; goroutines abort if theirs is stale
	cancelFunc func()        // cancels the context of the current in-flight RPC; nil when idle
	done       chan struct{} // closed by the goroutine when it exits; nil when idle
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
		authTracker: &authTracker{},
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
			// TODO: This will not select the correct secret in case the last authentication step uses a secret
			// which is not the local password (e.g. OTP).
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
			// waitForSlot blocks until no other auth is in flight, then registers
			// us as the active goroutine for this generation.  It returns false
			// if we have been superseded by a cancelAndWait() call and must abort.
			if !authTracker.waitForSlot(cancelFunc) {
				return nil
			}

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
			m.authTracker.finish()
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
			var secret string
			// TODO: This will not select the correct secret in case the last authentication step uses a secret
			// which is not the local password (e.g. OTP).
			if msg.secret != nil {
				secret = *msg.secret
			} else if m.currentSecret != "" {
				secret = m.currentSecret
			} else {
				log.Warningf(context.Background(), "authentication granted, but no secret returned, cannot set PAM_AUTHTOK")
			}
			return m, sendEvent(PamSuccess{BrokerID: m.currentBrokerID, AuthTok: secret, msg: authMsg})

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

// waitForSlot blocks until no other authentication goroutine is in flight,
// then registers itself as the active goroutine.  It returns true when the
// caller should proceed with the RPC, or false when a cancelAndWait() call
// has superseded this goroutine and the caller must abort.
//
// The generation counter is the key to correctness: cancelAndWait() bumps it
// under the lock before signalling, so any goroutine that wakes up afterwards
// will see a stale generation and abort — with no window for a race.
func (at *authTracker) waitForSlot(cancelFunc func()) bool {
	at.mu.Lock()
	gen := at.generation
	// Wait until the previous auth goroutine has finished.
	for at.done != nil {
		done := at.done
		at.mu.Unlock()
		<-done
		at.mu.Lock()
	}
	// If cancelAndWait() was called while we were waiting (or before we even
	// started), our generation is stale — abort without making any RPC.
	if at.generation != gen {
		at.mu.Unlock()
		return false
	}
	// Register ourselves as the active goroutine.
	at.cancelFunc = cancelFunc
	at.done = make(chan struct{})
	at.mu.Unlock()
	return true
}

// finish marks the active authentication goroutine as done.  It must be called
// by every goroutine that received true from waitForSlot, regardless of whether
// the RPC was actually made (e.g. even for newPasswordCheck detours).
func (at *authTracker) finish() {
	at.mu.Lock()
	done := at.done
	at.cancelFunc = nil
	at.done = nil
	at.mu.Unlock()
	if done != nil {
		close(done)
	}
}

// cancelAndWait cancels the in-flight authentication (if any) and waits for
// its goroutine to exit.  After it returns, any goroutine currently blocked in
// waitForSlot will also abort, because the generation counter was bumped.
func (at *authTracker) cancelAndWait() {
	at.mu.Lock()
	at.generation++
	cancelFunc := at.cancelFunc
	done := at.done
	at.mu.Unlock()

	if cancelFunc != nil {
		cancelFunc()
	}
	if done != nil {
		<-done
	}
}
