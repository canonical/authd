package pam_test

import (
	"context"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/log"
	"github.com/google/uuid"
	"golang.org/x/exp/maps"
	"google.golang.org/grpc"
)

type options struct {
	availableBrokersRet []*authd.ABResponse_BrokerInfo
	availableBrokersErr error

	getPreviousBrokerRet string
	getPreviousBrokerErr error

	selectBrokerRet *authd.SBResponse
	selectBrokerErr error

	getAuthenticationModesRet []*authd.GAMResponse_AuthenticationMode
	getAuthenticationModesErr error

	selectAuthenticationModeRet *authd.UILayout
	selectAuthenticationModeErr error

	isAuthenticatedRet        *authd.IAResponse
	isAuthenticatedErr        error
	isAuthenticatedWantSecret string
	isAuthenticatedWantSkip   bool
	isAuthenticatedWantWait   time.Duration
	isAuthenticatedMessage    string
	isAuthenticatedMaxRetries int

	endSessionErr error

	defaultBrokerForUser       map[string]string
	setDefaultBrokerForUserErr error

	uiLayouts map[string]*authd.UILayout
	authModes map[string]*authd.GAMResponse_AuthenticationMode

	ignoreSessionIDChecks     bool
	ignoreSessionIDGeneration bool
}

// DummyClient is a dummy implementation of [authd.PAMClient].
type DummyClient struct {
	options
	mu sync.Mutex

	privateKey    *rsa.PrivateKey
	encryptionKey string

	currentSessionID string
	selectedBrokerID string
	selectedUsername string
	selectedLang     string
}

// DummyClientOptions is the function signature used to tweak the daemon creation.
type DummyClientOptions func(*options)

// WithAvailableBrokers is the option to define the AvailableBrokers return values.
func WithAvailableBrokers(ret []*authd.ABResponse_BrokerInfo, err error) func(o *options) {
	return func(o *options) {
		o.availableBrokersRet = ret
		o.availableBrokersErr = err
	}
}

// WithPreviousBrokerForUser is the option to define the default broker ID for users.
func WithPreviousBrokerForUser(user string, brokerID string) func(o *options) {
	return func(o *options) {
		o.defaultBrokerForUser[user] = brokerID
	}
}

// WithGetPreviousBrokerReturn is the option to define the GetPreviousBroker return values.
func WithGetPreviousBrokerReturn(ret string, err error) func(o *options) {
	return func(o *options) {
		o.getPreviousBrokerRet = ret
		o.getPreviousBrokerErr = err
	}
}

// WithSelectBrokerReturn is the option to define the SelectBroker return values.
func WithSelectBrokerReturn(ret *authd.SBResponse, err error) func(o *options) {
	return func(o *options) {
		o.selectBrokerRet = ret
		o.selectBrokerErr = err
	}
}

// WithGetAuthenticationModesReturn is the option to define the GetAuthenticationModes return values.
func WithGetAuthenticationModesReturn(ret []*authd.GAMResponse_AuthenticationMode, err error) func(o *options) {
	return func(o *options) {
		o.getAuthenticationModesRet = ret
		o.getAuthenticationModesErr = err
	}
}

// WithSelectAuthenticationModeReturn is the option to define the SelectAuthenticationMode return values.
func WithSelectAuthenticationModeReturn(ret *authd.UILayout, err error) func(o *options) {
	return func(o *options) {
		o.selectAuthenticationModeRet = ret
		o.selectAuthenticationModeErr = err
	}
}

// WithIsAuthenticatedReturn is the option to define the IsAuthenticated return values.
func WithIsAuthenticatedReturn(ret *authd.IAResponse, err error) func(o *options) {
	return func(o *options) {
		o.isAuthenticatedRet = ret
		o.isAuthenticatedErr = err
	}
}

// WithIsAuthenticatedWantSecret is the option to define the IsAuthenticated wanted secret.
func WithIsAuthenticatedWantSecret(secret string) func(o *options) {
	return func(o *options) {
		o.isAuthenticatedWantSecret = secret
	}
}

// WithIsAuthenticatedWantSkip is the option to define the IsAuthenticated skip.
func WithIsAuthenticatedWantSkip() func(o *options) {
	return func(o *options) {
		o.isAuthenticatedWantSkip = true
	}
}

// WithIsAuthenticatedWantWait is the option to define the IsAuthenticated wait duration.
func WithIsAuthenticatedWantWait(wait time.Duration) func(o *options) {
	return func(o *options) {
		o.isAuthenticatedWantWait = wait
	}
}

// WithIsAuthenticatedMaxRetries is the option to define the IsAuthenticated max retries return values.
func WithIsAuthenticatedMaxRetries(maxRetries int) func(o *options) {
	return func(o *options) {
		o.isAuthenticatedMaxRetries = maxRetries
	}
}

// WithIsAuthenticatedMessage is the option to define the IsAuthenticated message return values.
func WithIsAuthenticatedMessage(message string) func(o *options) {
	return func(o *options) {
		o.isAuthenticatedMessage = message
	}
}

// WithEndSessionReturn is the option to define the EndSession return values.
func WithEndSessionReturn(err error) func(o *options) {
	return func(o *options) {
		o.endSessionErr = err
	}
}

// WithSetDefaultBrokerReturn is the option to define the SetDefaultBroker return values.
func WithSetDefaultBrokerReturn(err error) func(o *options) {
	return func(o *options) {
		o.setDefaultBrokerForUserErr = err
	}
}

// WithUILayout is the option to define the UI layouts supported return values.
func WithUILayout(authModeID string, label string, uiLayout *authd.UILayout) func(o *options) {
	return func(o *options) {
		o.uiLayouts[authModeID] = uiLayout
		o.authModes[authModeID] = &authd.GAMResponse_AuthenticationMode{Id: authModeID, Label: label}
	}
}

// WithIgnoreSessionIDChecks is the option to ignore session ID checks.
func WithIgnoreSessionIDChecks() func(o *options) {
	return func(o *options) {
		o.ignoreSessionIDChecks = true
	}
}

// WithIgnoreSessionIDGeneration is the option to ignore session ID checks.
func WithIgnoreSessionIDGeneration() func(o *options) {
	return func(o *options) {
		o.ignoreSessionIDGeneration = true
	}
}

// NewDummyClient returns a Dummy client with the given options.
func NewDummyClient(privateKey *rsa.PrivateKey, args ...DummyClientOptions) *DummyClient {
	// Set default options.
	dc := &DummyClient{
		privateKey: privateKey,
	}

	if privateKey != nil {
		pubASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		if err != nil {
			panic(err)
		}
		dc.encryptionKey = base64.StdEncoding.EncodeToString(pubASN1)
	}

	dc.defaultBrokerForUser = make(map[string]string)
	dc.uiLayouts = make(map[string]*authd.UILayout)
	dc.authModes = make(map[string]*authd.GAMResponse_AuthenticationMode)

	// Apply given args.
	for _, f := range args {
		f(&dc.options)
	}

	if dc.selectBrokerRet != nil && dc.selectBrokerRet.EncryptionKey == "" {
		dc.selectBrokerRet.EncryptionKey = dc.encryptionKey
	}

	return dc
}

// AvailableBrokers simulates AvailableBrokers using the provided parameters.
func (dc *DummyClient) AvailableBrokers(ctx context.Context, in *authd.Empty, opts ...grpc.CallOption) (*authd.ABResponse, error) {
	log.Debugf(ctx, "AvailableBrokers Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.availableBrokers()
}

func (dc *DummyClient) availableBrokers() (*authd.ABResponse, error) {
	if dc.availableBrokersErr != nil {
		return nil, dc.availableBrokersErr
	}
	return &authd.ABResponse{BrokersInfos: dc.availableBrokersRet}, nil
}

// GetPreviousBroker simulates GetPreviousBroker using the provided parameters.
func (dc *DummyClient) GetPreviousBroker(ctx context.Context, in *authd.GPBRequest, opts ...grpc.CallOption) (*authd.GPBResponse, error) {
	log.Debugf(ctx, "GetPreviousBroker Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.getPreviousBrokerErr != nil {
		return nil, dc.getPreviousBrokerErr
	}
	if dc.getPreviousBrokerRet != "" {
		return &authd.GPBResponse{PreviousBroker: dc.getPreviousBrokerRet}, nil
	}
	if in == nil {
		return &authd.GPBResponse{}, nil
	}
	if in.Username == "" {
		return nil, errors.New("no username provided")
	}
	brokerID := dc.defaultBrokerForUser[in.Username]
	return &authd.GPBResponse{PreviousBroker: brokerID}, nil
}

// SelectBroker simulates SelectBroker using the provided parameters.
func (dc *DummyClient) SelectBroker(ctx context.Context, in *authd.SBRequest, opts ...grpc.CallOption) (*authd.SBResponse, error) {
	log.Debugf(ctx, "SelectBroker Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.selectBrokerErr != nil {
		return nil, dc.selectBrokerErr
	}
	if !dc.ignoreSessionIDChecks && dc.currentSessionID != "" {
		if in != nil && dc.selectedUsername != in.Username {
			return nil, fmt.Errorf("session %q is still active", dc.currentSessionID)
		}
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if in.BrokerId == "" {
		return nil, errors.New("no broker ID provided")
	}
	sessionID := dc.currentSessionID
	if !dc.ignoreSessionIDGeneration && sessionID == "" {
		sessionID = uuid.New().String()
	}

	if dc.selectBrokerRet != nil {
		dc.selectedBrokerID = in.BrokerId
		dc.selectedLang = in.Lang
		dc.selectedUsername = in.Username

		if dc.selectBrokerRet.SessionId != "" {
			sessionID = dc.selectBrokerRet.SessionId
		}

		dc.currentSessionID = sessionID
		if dc.ignoreSessionIDChecks || dc.selectBrokerRet.SessionId != "" {
			return dc.selectBrokerRet, nil
		}
	}

	brokers, err := dc.availableBrokers()
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(brokers.BrokersInfos, func(b *authd.ABResponse_BrokerInfo) bool {
		return b.Id == in.BrokerId
	}) {
		return nil, fmt.Errorf("broker %q not found", in.BrokerId)
	}
	dc.selectedBrokerID = in.BrokerId
	dc.selectedLang = in.Lang
	dc.selectedUsername = in.Username
	dc.currentSessionID = sessionID
	return &authd.SBResponse{
		SessionId:     dc.currentSessionID,
		EncryptionKey: dc.encryptionKey,
	}, nil
}

// GetAuthenticationModes simulates GetAuthenticationModes using the provided parameters.
func (dc *DummyClient) GetAuthenticationModes(ctx context.Context, in *authd.GAMRequest, opts ...grpc.CallOption) (*authd.GAMResponse, error) {
	log.Debugf(ctx, "GetAuthenticationModes Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.getAuthenticationModesErr != nil {
		return nil, dc.getAuthenticationModesErr
	}
	if dc.getAuthenticationModesRet != nil {
		return &authd.GAMResponse{
			AuthenticationModes: dc.getAuthenticationModesRet,
		}, nil
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if !dc.ignoreSessionIDChecks && in.SessionId == "" {
		return nil, errors.New("no session ID provided")
	}
	if !dc.ignoreSessionIDChecks && dc.currentSessionID != in.SessionId {
		return nil, fmt.Errorf("impossible to get authentication mode, session ID %q not found", in.SessionId)
	}
	authModes := maps.Values(dc.authModes)
	slices.SortFunc(authModes,
		func(a *authd.GAMResponse_AuthenticationMode, b *authd.GAMResponse_AuthenticationMode) int {
			return strings.Compare(a.Id, b.Id)
		})
	return &authd.GAMResponse{
		AuthenticationModes: authModes,
	}, nil
}

// SelectAuthenticationMode simulates SelectAuthenticationMode using the provided parameters.
func (dc *DummyClient) SelectAuthenticationMode(ctx context.Context, in *authd.SAMRequest, opts ...grpc.CallOption) (*authd.SAMResponse, error) {
	log.Debugf(ctx, "SelectAuthenticationMode Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.selectAuthenticationModeErr != nil {
		return nil, dc.selectAuthenticationModeErr
	}
	if dc.selectAuthenticationModeRet != nil {
		return &authd.SAMResponse{
			UiLayoutInfo: dc.selectAuthenticationModeRet,
		}, nil
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if !dc.ignoreSessionIDChecks && in.SessionId == "" {
		return nil, errors.New("no session ID provided")
	}
	if !dc.ignoreSessionIDChecks && dc.currentSessionID != in.SessionId {
		return nil, fmt.Errorf("impossible to select authentication mode, session ID %q not found", in.SessionId)
	}
	if in.AuthenticationModeId == "" {
		return nil, errors.New("no authentication mode ID provided")
	}
	uiLayout, ok := dc.uiLayouts[in.AuthenticationModeId]
	if !ok {
		return nil, fmt.Errorf("authentication mode %q not found", in.AuthenticationModeId)
	}
	return &authd.SAMResponse{UiLayoutInfo: uiLayout}, nil
}

// IsAuthenticated simulates IsAuthenticated using the provided parameters.
func (dc *DummyClient) IsAuthenticated(ctx context.Context, in *authd.IARequest, opts ...grpc.CallOption) (*authd.IAResponse, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	log.Debugf(ctx, "IsAuthenticated Called: %#v", in)
	if dc.isAuthenticatedErr != nil {
		return nil, dc.isAuthenticatedErr
	}
	if dc.isAuthenticatedRet != nil {
		return dc.isAuthenticatedRet, nil
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if !dc.ignoreSessionIDChecks && in.SessionId == "" {
		return nil, errors.New("no session ID provided")
	}
	if !dc.ignoreSessionIDChecks && dc.currentSessionID != in.SessionId {
		return nil, fmt.Errorf("impossible to authenticate, session ID %q not found", in.SessionId)
	}
	if in.AuthenticationData == nil {
		return nil, errors.New("no authentication data provided")
	}

	var msg string
	if dc.isAuthenticatedMessage != "" {
		msg = fmt.Sprintf(`{"message": "%s"}`, dc.isAuthenticatedMessage)
	}

	switch item := in.AuthenticationData.Item.(type) {
	case *authd.IARequest_AuthenticationData_Secret:
		if dc.isAuthenticatedWantSecret == "" {
			return nil, errors.New("no wanted secret provided")
		}
		return dc.handleChallenge(item.Secret, msg)
	case *authd.IARequest_AuthenticationData_Wait:
		if dc.isAuthenticatedWantWait == 0 {
			return nil, errors.New("no wanted wait provided")
		}
		select {
		case <-time.After(time.Duration(testutils.SleepMultiplier() * float64(dc.isAuthenticatedWantWait))):
		case <-ctx.Done():
			msg = ""
			if dc.isAuthenticatedMessage != "" {
				msg = fmt.Sprintf(`{"message": "Cancelled: %s"}`, dc.isAuthenticatedMessage)
			}
			return &authd.IAResponse{
				Access: auth.Cancelled,
				Msg:    msg,
			}, nil
		}
		return &authd.IAResponse{
			Access: auth.Granted,
			Msg:    msg,
		}, nil
	case *authd.IARequest_AuthenticationData_Skip:
		if !dc.isAuthenticatedWantSkip {
			return nil, errors.New("no wanted skip requested")
		}
		return &authd.IAResponse{Msg: msg}, nil
	default:
		return nil, errors.New("no authentication data provided")
	}
}

func (dc *DummyClient) handleChallenge(secret string, msg string) (*authd.IAResponse, error) {
	if secret == "" {
		return nil, errors.New("no secret provided")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, err
	}
	if dc.privateKey == nil {
		return nil, errors.New("no private key defined")
	}
	plaintext, err := rsa.DecryptOAEP(sha512.New(), nil, dc.privateKey, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	if string(plaintext) == dc.isAuthenticatedWantSecret {
		return &authd.IAResponse{
			Access: auth.Granted,
			Msg:    msg,
		}, nil
	}

	dc.isAuthenticatedMaxRetries--
	if dc.isAuthenticatedMaxRetries < 0 {
		return &authd.IAResponse{
			Access: auth.Denied,
			Msg:    msg,
		}, nil
	}

	return &authd.IAResponse{
		Access: auth.Retry,
		Msg:    msg,
	}, nil
}

// EndSession simulates EndSession using the provided parameters.
func (dc *DummyClient) EndSession(ctx context.Context, in *authd.ESRequest, opts ...grpc.CallOption) (*authd.Empty, error) {
	log.Debugf(ctx, "EndSession Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.endSessionErr != nil {
		return nil, dc.endSessionErr
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if !dc.ignoreSessionIDChecks && in.SessionId == "" {
		return nil, errors.New("no session ID provided")
	}
	if !dc.ignoreSessionIDChecks && dc.currentSessionID != in.SessionId {
		return nil, fmt.Errorf("impossible to end session %q, not found", in.SessionId)
	}
	dc.currentSessionID = ""
	dc.selectedUsername = ""
	return &authd.Empty{}, nil
}

// SetDefaultBrokerForUser simulates SetDefaultBrokerForUser using the provided parameters.
func (dc *DummyClient) SetDefaultBrokerForUser(ctx context.Context, in *authd.SDBFURequest, opts ...grpc.CallOption) (*authd.Empty, error) {
	log.Debugf(ctx, "SetDefaultBrokerForUser Called: %#v", in)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.setDefaultBrokerForUserErr != nil {
		return nil, dc.setDefaultBrokerForUserErr
	}
	if in == nil {
		return nil, errors.New("no input values provided")
	}
	if in.Username == "" {
		return nil, errors.New("no valid username provided")
	}
	if in.BrokerId == "" {
		return nil, errors.New("no valid broker ID provided")
	}
	dc.defaultBrokerForUser[in.Username] = in.BrokerId
	return &authd.Empty{}, nil
}

// Utility functions for testing purposes.

// SelectedUsername returns the selected Username on the client.
func (dc *DummyClient) SelectedUsername() string {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.selectedUsername
}

// SelectedBrokerID returns the selected BrokerID on the client.
func (dc *DummyClient) SelectedBrokerID() string {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.selectedBrokerID
}

// CurrentSessionID returns the selected BrokerID on the client.
func (dc *DummyClient) CurrentSessionID() string {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.currentSessionID
}

// SelectedLang returns the selected Lang on the client.
func (dc *DummyClient) SelectedLang() string {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.selectedLang
}

// UIOptions is the function signature used to tweak the [FormUILayout].
type UIOptions func(*authd.UILayout)

// WithButton is an option for [FormUILayout] to set the button parameter.
func WithButton(label string) func(l *authd.UILayout) {
	return func(l *authd.UILayout) { l.Button = &label }
}

// WithWait is an option for [FormUILayout] to enable wait in FormUI UI.
func WithWait(hasWait bool) func(l *authd.UILayout) {
	wait := (*string)(nil)
	if hasWait {
		wait = &layouts.OptionalWithBooleans
	}
	return func(l *authd.UILayout) { l.Wait = wait }
}

// FormUILayout returns an [authd.UILayout] for forms.
func FormUILayout(opts ...UIOptions) *authd.UILayout {
	required := layouts.Optional
	supportedEntries := layouts.OptionalItems(
		entries.Chars,
		entries.CharsPassword,
	)
	uiLayout := &authd.UILayout{
		Type:  layouts.Form,
		Label: &required,
		Entry: &supportedEntries,
	}
	for _, f := range opts {
		f(uiLayout)
	}
	return uiLayout
}

// WithQrCodeCode is an option for [QrCodeUILayout] to set the code parameter in QrCode UI.
func WithQrCodeCode(code string) func(l *authd.UILayout) {
	return func(l *authd.UILayout) { l.Code = &code }
}

// WithQrCodeRenders is an option for [QrCodeUILayout] to set the rendering parameter in QrCode UI.
func WithQrCodeRenders(renders *bool) func(l *authd.UILayout) {
	return func(l *authd.UILayout) { l.RendersQrcode = renders }
}

// QrCodeUILayout returns an [authd.UILayout] for qr code.
func QrCodeUILayout(opts ...UIOptions) *authd.UILayout {
	required, optional := layouts.Required, layouts.Optional
	rendersQrCode := true

	uiLayout := &authd.UILayout{
		Type:          layouts.QrCode,
		Content:       &required,
		Code:          &required,
		Wait:          &layouts.RequiredWithBooleans,
		Label:         &optional,
		Button:        &optional,
		RendersQrcode: &rendersQrCode,
	}

	for _, f := range opts {
		f(uiLayout)
	}

	return uiLayout
}

// NewPasswordUILayout returns an [authd.UILayout] for new password forms.
func NewPasswordUILayout() *authd.UILayout {
	required, optional := layouts.Required, layouts.Optional
	optionalWithBooleans := layouts.OptionalWithBooleans
	supportedEntries := layouts.OptionalItems(
		entries.Chars,
		entries.CharsPassword,
	)
	return &authd.UILayout{
		Type:   layouts.NewPassword,
		Label:  &required,
		Entry:  &supportedEntries,
		Wait:   &optionalWithBooleans,
		Button: &optional,
	}
}
