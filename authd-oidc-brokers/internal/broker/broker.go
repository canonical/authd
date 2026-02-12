// Package broker is the generic oidc business code.
package broker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/fileutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/ubuntu/authd/log"
	"github.com/ubuntu/decorate"
	"golang.org/x/oauth2"
)

const (
	maxAuthAttempts    = 3
	maxRequestDuration = 5 * time.Second
)

// Config is the configuration for the broker.
type Config struct {
	ConfigFile string
	DataDir    string

	userConfig
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	cfg Config

	provider providers.Provider
	oidcCfg  oidc.Config

	currentSessions   map[string]session
	currentSessionsMu sync.RWMutex

	privateKey *rsa.PrivateKey
}

type session struct {
	username string
	lang     string
	mode     string

	selectedMode    string
	authModes       []string
	attemptsPerMode map[string]int
	nextAuthModes   []string

	oidcServer              *oidc.Provider
	oauth2Config            oauth2.Config
	isOffline               bool
	providerConnectionError error
	userDataDir             string
	passwordPath            string
	tokenPath               string

	// Data to pass from one request to another.
	deviceAuthResponse *oauth2.DeviceAuthResponse
	authInfo           *token.AuthCachedInfo

	isAuthenticating *isAuthenticatedCtx
}

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

type option struct {
	provider providers.Provider
}

// Option is a func that allows to override some of the broker default settings.
type Option func(*option)

// New returns a new oidc Broker with the providers listed in the configuration file.
func New(cfg Config, args ...Option) (b *Broker, err error) {
	p := providers.CurrentProvider()

	if cfg.ConfigFile != "" {
		cfg.userConfig, err = parseConfigFromPath(cfg.ConfigFile, p)
		if err != nil {
			return nil, fmt.Errorf("could not parse config file '%s': %v", cfg.ConfigFile, err)
		}
	}

	opts := option{
		provider: p,
	}
	for _, arg := range args {
		arg(&opts)
	}

	if cfg.DataDir == "" {
		err = errors.Join(err, errors.New("cache path is required and was not provided"))
	}
	if cfg.issuerURL == "" {
		err = errors.Join(err, errors.New("issuer URL is required and was not provided"))
	}
	if cfg.clientID == "" {
		err = errors.Join(err, errors.New("client ID is required and was not provided"))
	}
	if err != nil {
		return nil, err
	}

	if cfg.homeBaseDir == "" {
		cfg.homeBaseDir = "/home"
	}

	// Generate a new private key for the broker.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return nil, errors.New("failed to generate broker private key")
	}

	clientID := cfg.clientID
	if opts.provider.SupportsDeviceRegistration() && cfg.registerDevice {
		clientID = consts.MicrosoftBrokerAppID
	}

	b = &Broker{
		cfg:        cfg,
		provider:   opts.provider,
		oidcCfg:    oidc.Config{ClientID: clientID},
		privateKey: privateKey,

		currentSessions:   make(map[string]session),
		currentSessionsMu: sync.RWMutex{},
	}
	return b, nil
}

// NewSession creates a new session for the user.
func (b *Broker) NewSession(username, lang, mode string) (sessionID, encryptionKey string, err error) {
	defer decorate.OnError(&err, "could not create new session for user %q", username)

	sessionID = uuid.New().String()
	s := session{
		username: username,
		lang:     lang,
		mode:     mode,

		attemptsPerMode: make(map[string]int),
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&b.privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	_, issuer, _ := strings.Cut(b.cfg.issuerURL, "://")
	issuer = strings.ReplaceAll(issuer, "/", "_")
	issuer = strings.ReplaceAll(issuer, ":", "_")
	s.userDataDir = filepath.Join(b.cfg.DataDir, issuer, username)
	// The token is stored in $DATA_DIR/$ISSUER/$USERNAME/token.json.
	s.tokenPath = filepath.Join(s.userDataDir, "token.json")
	// The password is stored in $DATA_DIR/$ISSUER/$USERNAME/password.
	s.passwordPath = filepath.Join(s.userDataDir, "password")

	// Construct an OIDC provider via OIDC discovery.
	s.oidcServer, err = b.connectToOIDCServer(context.Background())
	if err != nil {
		log.Noticef(context.Background(), "Could not connect to the provider: %v. Starting session in offline mode.", err)
		s.isOffline = true
		s.providerConnectionError = err
	}

	scopes := append(consts.DefaultScopes, b.provider.AdditionalScopes()...)
	if b.provider.SupportsDeviceRegistration() && b.cfg.registerDevice {
		scopes = consts.MicrosoftBrokerAppScopes
	}
	// Append extra scopes from config
	scopes = append(scopes, b.cfg.extraScopes...)

	if s.oidcServer != nil {
		s.oauth2Config = oauth2.Config{
			ClientID:     b.oidcCfg.ClientID,
			ClientSecret: b.cfg.clientSecret,
			Endpoint:     s.oidcServer.Endpoint(),
			Scopes:       scopes,
		}
	}

	b.currentSessionsMu.Lock()
	b.currentSessions[sessionID] = s
	b.currentSessionsMu.Unlock()

	return sessionID, base64.StdEncoding.EncodeToString(pubASN1), nil
}

func (b *Broker) connectToOIDCServer(ctx context.Context) (*oidc.Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()

	return oidc.NewProvider(ctx, b.cfg.issuerURL)
}

// GetAuthenticationModes returns the authentication modes available for the user.
func (b *Broker) GetAuthenticationModes(sessionID string, supportedUILayouts []map[string]string) (authModesWithLabels []map[string]string, err error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	availableModes, err := b.availableAuthModes(session)
	if err != nil {
		return nil, err
	}

	// Store the available auth modes, so that we can check in SelectAuthenticationMode if the selected mode is valid.
	session.authModes = availableModes
	if err := b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	modesSupportedByUI := b.authModesSupportedByUI(supportedUILayouts)

	for _, mode := range availableModes {
		if !slices.Contains(modesSupportedByUI, mode) {
			continue
		}

		authModesWithLabels = append(authModesWithLabels, map[string]string{
			"id":    mode,
			"label": authmodes.Label[mode],
		})
	}

	if len(authModesWithLabels) == 0 {
		// If we can't use a local authentication mode and we failed to connect to the provider,
		// report the connection error.
		if session.providerConnectionError != nil {
			log.Errorf(context.Background(), "Error connecting to provider: %v", session.providerConnectionError)
			//nolint:staticcheck,revive // ST1005 This error is displayed as is to the user, so it should be capitalized
			return nil, errors.New("Error connecting to provider. Check your network connection.")
		}
		return nil, fmt.Errorf("no authentication modes available for user %q", session.username)
	}

	return authModesWithLabels, nil
}

func (b *Broker) availableAuthModes(session session) (availableModes []string, err error) {
	if len(session.nextAuthModes) > 0 {
		for _, mode := range session.nextAuthModes {
			if !b.authModeIsAvailable(session, mode) {
				continue
			}
			availableModes = append(availableModes, mode)
		}
		if availableModes == nil {
			log.Warningf(context.Background(), "None of the next auth modes are available: %v", session.nextAuthModes)
		}
		return availableModes, nil
	}

	switch session.mode {
	case sessionmode.ChangePassword, sessionmode.ChangePasswordOld:
		// Session is for changing the password.
		if !passwordFileExists(session) {
			return nil, errors.New("password file does not exist, cannot change password")
		}
		return []string{authmodes.Password}, nil

	default:
		// Session is for login. Check which auth modes are available.
		// The order of the modes is important, because authd picks the first supported one.
		// Password authentication should be the first option if available, to avoid performing device authentication
		// when it's not necessary.
		modes := append([]string{authmodes.Password}, b.provider.SupportedOIDCAuthModes()...)
		for _, mode := range modes {
			if b.authModeIsAvailable(session, mode) {
				availableModes = append(availableModes, mode)
			}
		}
		return availableModes, nil
	}
}

func (b *Broker) authModeIsAvailable(session session, authMode string) bool {
	switch authMode {
	case authmodes.Password:
		if !tokenExists(session) {
			log.Debugf(context.Background(), "Token does not exist for user %q, so local password authentication is not available", session.username)
			return false
		}

		if !passwordFileExists(session) {
			log.Debugf(context.Background(), "Password file does not exist for user %q, so local password authentication is not available", session.username)
			return false
		}

		authInfo, err := token.LoadAuthInfo(session.tokenPath)
		if err != nil {
			log.Warningf(context.Background(), "Could not load token, so local password authentication is not available: %v", err)
			return false
		}

		if !b.provider.SupportsDeviceRegistration() {
			// If the provider does not support device registration,
			// we can always use the token for local password authentication.
			log.Debugf(context.Background(), "Provider does not support device registration, so local password authentication is available for user %q", session.username)
			return true
		}

		if session.isOffline {
			// If the session is in offline mode, we can't register the device anyway,
			// so we can allow the user to use local password authentication.
			log.Debugf(context.Background(), "Session is in offline mode, so local password authentication is available for user %q", session.username)
			return true
		}

		isTokenForDeviceRegistration, err := b.provider.IsTokenForDeviceRegistration(authInfo.Token)
		if err != nil {
			log.Warningf(context.Background(), "Could not check if token is for device registration, so local password authentication is not available: %v", err)
			return false
		}

		if b.cfg.registerDevice && !isTokenForDeviceRegistration {
			// TODO: We might want to display a message to the user in this case
			log.Noticef(context.Background(), "Token exists for user %q, but it cannot be used for device registration, so local password authentication is not available", session.username)
			return false
		}
		if !b.cfg.registerDevice && isTokenForDeviceRegistration {
			// TODO: We might want to display a message to the user in this case
			log.Noticef(context.Background(), "Token exists for user %q, but it requires device registration, so local password authentication is not available", session.username)
			return false
		}

		return true
	case authmodes.NewPassword:
		return true
	case authmodes.Device, authmodes.DeviceQr:
		if session.oidcServer == nil {
			log.Debugf(context.Background(), "OIDC server is not initialized, so device authentication is not available")
			return false
		}
		if session.oidcServer.Endpoint().DeviceAuthURL == "" {
			log.Debugf(context.Background(), "OIDC server does not support device authentication, so device authentication is not available")
			return false
		}
		if session.isOffline {
			log.Noticef(context.Background(), "Session is in offline mode, so device authentication is not available")
			return false
		}
		return true
	}
	return false
}

func tokenExists(session session) bool {
	exists, err := fileutils.FileExists(session.tokenPath)
	if err != nil {
		log.Warningf(context.Background(), "Could not check if token exists: %v", err)
	}
	return exists
}

func passwordFileExists(session session) bool {
	exists, err := fileutils.FileExists(session.passwordPath)
	if err != nil {
		log.Warningf(context.Background(), "Could not check if local password file exists: %v", err)
	}
	return exists
}

func (b *Broker) authModesSupportedByUI(supportedUILayouts []map[string]string) (supportedModes []string) {
	for _, layout := range supportedUILayouts {
		mode := b.supportedAuthModeFromLayout(layout)
		if mode != "" {
			supportedModes = append(supportedModes, mode)
		}
	}
	return supportedModes
}

func (b *Broker) supportedAuthModeFromLayout(layout map[string]string) string {
	supportedEntries := strings.Split(strings.TrimPrefix(layout["entry"], "optional:"), ",")
	switch layout["type"] {
	case "qrcode":
		if !strings.Contains(layout["wait"], "true") {
			return ""
		}
		if layout["renders_qrcode"] == "false" {
			return authmodes.Device
		}
		return authmodes.DeviceQr

	case "form":
		if slices.Contains(supportedEntries, "chars_password") {
			return authmodes.Password
		}

	case "newpassword":
		if slices.Contains(supportedEntries, "chars_password") {
			return authmodes.NewPassword
		}
	}
	return ""
}

// SelectAuthenticationMode selects the authentication mode for the user.
func (b *Broker) SelectAuthenticationMode(sessionID, authModeID string) (uiLayoutInfo map[string]string, err error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	// populate UI options based on selected authentication mode
	uiLayoutInfo, err = b.generateUILayout(&session, authModeID)
	if err != nil {
		return nil, err
	}

	// Store selected mode
	session.selectedMode = authModeID

	if err = b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	return uiLayoutInfo, nil
}

func (b *Broker) generateUILayout(session *session, authModeID string) (map[string]string, error) {
	if !slices.Contains(session.authModes, authModeID) {
		return nil, fmt.Errorf("selected authentication mode %q does not exist", authModeID)
	}

	var uiLayout map[string]string
	switch authModeID {
	case authmodes.Device, authmodes.DeviceQr:
		ctx, cancel := context.WithTimeout(context.Background(), maxRequestDuration)
		defer cancel()

		var authOpts []oauth2.AuthCodeOption

		// Workaround to cater for RFC compliant oauth2 server. Public providers do not properly
		// implement the RFC, (probably) because they assume that device clients are public.
		// As described in https://datatracker.ietf.org/doc/html/rfc8628#section-3.1
		// device authentication requests must provide client authentication, similar to that for
		// the token endpoint.
		// The golang/oauth2 library does not implement this, see https://github.com/golang/oauth2/issues/685.
		// We implement a workaround for implementing the client_secret_post client authn method.
		// Supporting client_secret_basic would require us to patch the http client used by the
		// oauth2 lib.
		// Some providers support both of these authentication methods, some implement only one and
		// some implement neither.
		// This was tested with the following providers:
		// - Ory Hydra: supports client_secret_post
		// TODO @shipperizer: client_authentication methods should be configurable
		if secret := session.oauth2Config.ClientSecret; secret != "" {
			authOpts = append(authOpts, oauth2.SetAuthURLParam("client_secret", secret))
		}

		log.Debug(ctx, "Retrieving device code...")
		response, err := session.oauth2Config.DeviceAuth(ctx, authOpts...)
		if err != nil {
			return nil, fmt.Errorf("could not generate Device Authentication code layout: %v", err)
		}
		session.deviceAuthResponse = response
		log.Debug(ctx, "Retrieved device code.")

		label := "Open the URL and enter the code below."
		if authModeID == authmodes.DeviceQr {
			label = "Scan the QR code or open the URL and enter the code below."
		}

		uiLayout = map[string]string{
			"type":    "qrcode",
			"label":   label,
			"wait":    "true",
			"button":  "Request new code",
			"content": response.VerificationURI,
			"code":    response.UserCode,
		}

	case authmodes.Password:
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Enter your local password",
			"entry": "chars_password",
		}

	case authmodes.NewPassword:
		label := "Create a local password"
		if session.mode == sessionmode.ChangePassword || session.mode == sessionmode.ChangePasswordOld {
			label = "Update your local password"
		}

		uiLayout = map[string]string{
			"type":  "newpassword",
			"label": label,
			"entry": "chars_password",
		}
	}

	return uiLayout, nil
}

// IsAuthenticated evaluates the provided authenticationData and returns the authentication status for the user.
func (b *Broker) IsAuthenticated(sessionID, authenticationData string) (string, string, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return AuthDenied, "{}", err
	}

	var authData map[string]string
	if authenticationData != "" {
		if err := json.Unmarshal([]byte(authenticationData), &authData); err != nil {
			return AuthDenied, "{}", fmt.Errorf("authentication data is not a valid json value: %v", err)
		}
	}

	ctx, err := b.startAuthenticate(sessionID)
	if err != nil {
		return AuthDenied, "{}", err
	}

	// Cleans up the IsAuthenticated context when the call is done.
	defer b.CancelIsAuthenticated(sessionID)

	authDone := make(chan struct{})
	var access string
	var iadResponse isAuthenticatedDataResponse
	go func() {
		access, iadResponse = b.handleIsAuthenticated(ctx, &session, authData)
		close(authDone)
	}()

	select {
	case <-authDone:
	case <-ctx.Done():
		// We can ignore the error here since the message is constant.
		msg, _ := json.Marshal(errorMessage{Message: "Authentication request cancelled"})
		return AuthCancelled, string(msg), ctx.Err()
	}

	if access == AuthRetry {
		session.attemptsPerMode[session.selectedMode]++
		if session.attemptsPerMode[session.selectedMode] == maxAuthAttempts {
			access = AuthDenied
			iadResponse = errorMessage{Message: "Maximum number of authentication attempts reached"}
		}
	}

	if err = b.updateSession(sessionID, session); err != nil {
		return AuthDenied, "{}", err
	}

	encoded, err := json.Marshal(iadResponse)
	if err != nil {
		return AuthDenied, "{}", fmt.Errorf("could not parse data to JSON: %v", err)
	}

	data := string(encoded)
	if data == "null" {
		data = "{}"
	}
	return access, data, nil
}

func unexpectedErrMsg(msg string) errorMessage {
	return errorMessage{Message: fmt.Sprintf("An unexpected error occurred: %s. Please report this error on https://github.com/canonical/authd/issues", msg)}
}

func (b *Broker) handleIsAuthenticated(ctx context.Context, session *session, authData map[string]string) (access string, data isAuthenticatedDataResponse) {
	rawSecret, ok := authData[AuthDataSecret]
	if !ok {
		rawSecret = authData[AuthDataSecretOld]
	}

	// Decrypt secret if present.
	secret, err := decodeRawSecret(b.privateKey, rawSecret)
	if err != nil {
		log.Errorf(context.Background(), "could not decode secret: %s", err)
		return AuthRetry, unexpectedErrMsg("could not decode secret")
	}

	switch session.selectedMode {
	case authmodes.Device, authmodes.DeviceQr:
		return b.deviceAuth(ctx, session)
	case authmodes.Password:
		return b.passwordAuth(ctx, session, secret)
	case authmodes.NewPassword:
		return b.newPassword(session, secret)
	default:
		log.Errorf(context.Background(), "unknown authentication mode %q", session.selectedMode)
		return AuthDenied, unexpectedErrMsg("unknown authentication mode")
	}
}

func (b *Broker) deviceAuth(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	response := session.deviceAuthResponse
	if response == nil {
		log.Error(context.Background(), "device auth response is not set")
		return AuthDenied, unexpectedErrMsg("device auth response is not set")
	}

	if response.Expiry.IsZero() {
		response.Expiry = time.Now().Add(time.Hour)
		log.Debugf(context.Background(), "Device code does not have an expiry time, using default of %s", response.Expiry)
	} else {
		log.Debugf(context.Background(), "Device code expiry time: %s", response.Expiry)
	}
	expiryCtx, cancel := context.WithDeadline(ctx, response.Expiry)
	defer cancel()

	// The default interval is 5 seconds, which means the user has to wait up to 5 seconds after
	// successful authentication. We're reducing the interval to 1 second to improve UX a bit.
	response.Interval = 1

	log.Debug(ctx, "Polling to exchange device code for token...")
	t, err := session.oauth2Config.DeviceAccessToken(expiryCtx, response, b.provider.AuthOptions()...)
	if err != nil {
		log.Errorf(context.Background(), "Error retrieving access token: %s", err)
		return AuthRetry, errorMessage{Message: "Error retrieving access token. Please try again."}
	}
	log.Debug(ctx, "Exchanged device code for token.")

	if t.RefreshToken == "" {
		log.Warningf(context.Background(), "No refresh token returned for user during device authentication. You might have to add the 'offline_access' scope to the 'extra_scopes' setting.")
	}

	rawIDToken, ok := t.Extra("id_token").(string)
	if !ok {
		log.Error(context.Background(), "token response does not contain an ID token")
		return AuthDenied, unexpectedErrMsg("token response does not contain an ID token")
	}

	authInfo := token.NewAuthCachedInfo(t, rawIDToken, b.provider)

	authInfo.ProviderMetadata, err = b.provider.GetMetadata(session.oidcServer)
	if err != nil {
		log.Errorf(context.Background(), "could not get provider metadata: %s", err)
		return AuthDenied, unexpectedErrMsg("could not get provider metadata")
	}

	authInfo.UserInfo, err = b.userInfoFromIDToken(ctx, session, rawIDToken)
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}

	if b.provider.SupportsDeviceRegistration() && b.cfg.registerDevice {
		// Load existing device registration data if there is any, to avoid re-registering the device.
		var deviceRegistrationData []byte
		oldAuthInfo, err := token.LoadAuthInfo(session.tokenPath)
		if err == nil {
			deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
		}

		var cleanup func()
		authInfo.DeviceRegistrationData, cleanup, err = b.provider.MaybeRegisterDevice(ctx, t,
			session.username,
			b.cfg.issuerURL,
			deviceRegistrationData,
		)
		if err != nil {
			log.Errorf(context.Background(), "error registering device: %s", err)
			return AuthDenied, errorMessage{Message: "Error registering device"}
		}
		defer cleanup()

		// Store the auth info, so that the device registration data is not lost if the login fails after this point.
		if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}
	}

	// We can only fetch the groups after registering the device, because the token acquired for device registration
	// cannot be used with the Microsoft Graph API and a new token must be acquired for the Graph API.
	authInfo.UserInfo.Groups, err = b.getGroups(ctx, session, authInfo)
	if err != nil {
		log.Errorf(context.Background(), "failed to get groups: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Failed to retrieve groups from Microsoft Graph API")
	}

	// Store the auth info in the session so that we can use it when handling the
	// next IsAuthenticated call for the new password mode.
	session.authInfo = authInfo
	session.nextAuthModes = []string{authmodes.NewPassword}

	return AuthNext, nil
}

func (b *Broker) passwordAuth(ctx context.Context, session *session, secret string) (string, isAuthenticatedDataResponse) {
	ok, err := password.CheckPassword(secret, session.passwordPath)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return AuthDenied, unexpectedErrMsg("could not check password")
	}
	if !ok {
		log.Noticef(context.Background(), "Authentication failure: incorrect local password for user %q", session.username)
		return AuthRetry, errorMessage{Message: "Incorrect password, please try again."}
	}

	authInfo, err := token.LoadAuthInfo(session.tokenPath)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return AuthDenied, unexpectedErrMsg("could not load stored token")
	}

	// If the session is for changing the password, we don't need to refresh the token and user info (and we don't
	// want the method call to return an error if refreshing the token or user info fails).
	if session.mode == sessionmode.ChangePassword || session.mode == sessionmode.ChangePasswordOld {
		// Store the auth info in the session so that we can use it when handling the
		// next IsAuthenticated call for the new password mode.
		session.authInfo = authInfo
		session.nextAuthModes = []string{authmodes.NewPassword}
		return AuthNext, nil
	}

	if b.cfg.forceProviderAuthentication && session.isOffline {
		log.Error(context.Background(), "Remote authentication failed: force_provider_authentication is enabled, but the identity provider is not reachable")
		return AuthDenied, errorMessage{Message: "Remote authentication failed: identity provider is not reachable"}
	}

	if authInfo.UserIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: user %q is disabled in Microsoft Entra ID and session is offline", session.username)
		return AuthDenied, errorMessage{Message: "This user is disabled in Microsoft Entra ID. Please contact your administrator or try again with a working network connection."}
	}

	if authInfo.DeviceIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: device %q is disabled in Microsoft Entra ID and session is offline", session.username)
		return AuthDenied, errorMessage{Message: "This device is disabled in Microsoft Entra ID. Please contact your administrator or try again with a working network connection."}
	}

	// Refresh the token if we're online even if the token has not expired
	if b.cfg.forceProviderAuthentication || !session.isOffline {
		// Check if we have a refresh token before attempting to refresh
		if authInfo.Token.RefreshToken == "" {
			log.Warningf(context.Background(), "No refresh token available for user %q", session.username)
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "Remote authentication failed: No refresh token. Please contact your administrator."}
		}

		// We have a refresh token, attempt to refresh
		oldAuthInfo := authInfo
		authInfo, err = b.refreshToken(ctx, session, authInfo)
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) {
			if b.provider.IsTokenExpiredError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token expired for user %q, new device authentication required", session.username)
				session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
				return AuthNext, errorMessage{Message: "Refresh token expired, please authenticate again using device authentication."}
			}
			if b.provider.IsUserDisabledError(retrieveErr) {
				log.Error(context.Background(), retrieveErr.Error())
				log.Errorf(context.Background(), "Login failed: User %q is disabled in Microsoft Entra ID, please contact your administrator.", session.username)

				// Store the information that the user is disabled, so that we can deny login on subsequent offline attempts.
				oldAuthInfo.UserIsDisabled = true
				if err = token.CacheAuthInfo(session.tokenPath, oldAuthInfo); err != nil {
					log.Errorf(context.Background(), "Failed to store token: %s", err)
					return AuthDenied, unexpectedErrMsg("failed to store token")
				}

				return AuthDenied, errorMessage{Message: "This user is disabled in Microsoft Entra ID, please contact your administrator."}
			}
		}
		if err != nil {
			log.Errorf(context.Background(), "Failed to refresh token: %s", err)
			return AuthDenied, errorMessage{Message: "Failed to refresh token"}
		}
	}

	// If device registration is enabled, ensure that the device is registered.
	if b.provider.SupportsDeviceRegistration() && !session.isOffline && b.cfg.registerDevice {
		var cleanup func()
		authInfo.DeviceRegistrationData, cleanup, err = b.provider.MaybeRegisterDevice(ctx,
			authInfo.Token,
			session.username,
			b.cfg.issuerURL,
			authInfo.DeviceRegistrationData,
		)
		if err != nil {
			log.Errorf(context.Background(), "error registering device: %s", err)
			return AuthDenied, errorMessage{Message: "Error registering device"}
		}
		defer cleanup()

		// Store the auth info, so that the device registration data is not lost if the login fails after this point.
		if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}
	}

	// Try to refresh the groups
	groups, err := b.getGroups(ctx, session, authInfo)
	if errors.Is(err, himmelblau.ErrDeviceDisabled) {
		// The device is disabled, deny login
		log.Errorf(context.Background(), "Login failed: %s", err)

		// Store the information that the device is disabled, so that we can deny login on subsequent offline attempts.
		authInfo.DeviceIsDisabled = true
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		return AuthDenied, errorMessage{Message: "This device is disabled in Microsoft Entra ID, please contact your administrator."}
	}
	if errors.Is(err, himmelblau.ErrInvalidRedirectURI) {
		// Deny login if the redirect URI is invalid, so that users and administrators are aware of the issue.
		log.Errorf(context.Background(), "Login failed: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Invalid redirect URI")
	}
	var tokenAcquisitionError himmelblau.TokenAcquisitionError
	if errors.As(err, &tokenAcquisitionError) {
		log.Errorf(context.Background(), "Token acquisition failed: %s. Try again using device authentication.", err)
		// The token acquisition failed unexpectedly.
		// One possible reason is that the device was deleted by an administrator in Entra ID.
		// In this case, the user can perform device authentication again to get a new token
		// and register the device again, allowing the user to log in.
		// We delete the device registration data to cause device authentication to re-register the device.
		authInfo.DeviceRegistrationData = nil
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
		msg := "Authentication failed due to a token issue. Please try again using device authentication."
		return AuthNext, errorMessage{Message: msg}
	}
	if err != nil {
		// We couldn't fetch the groups, but we have valid cached ones.
		log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
	} else {
		authInfo.UserInfo.Groups = groups
	}

	return b.finishAuth(session, authInfo)
}

func (b *Broker) finishAuth(session *session, authInfo *token.AuthCachedInfo) (string, isAuthenticatedDataResponse) {
	if b.cfg.shouldRegisterOwner() {
		if err := b.cfg.registerOwner(b.cfg.ConfigFile, authInfo.UserInfo.Name); err != nil {
			// The user is not allowed if we fail to create the owner-autoregistration file.
			// Otherwise the owner might change if the broker is restarted.
			log.Errorf(context.Background(), "Failed to assign the owner role: %v", err)
			return AuthDenied, unexpectedErrMsg("failed to assign the owner role")
		}
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}

	// Add extra groups to the user info.
	for _, name := range b.cfg.extraGroups {
		log.Debugf(context.Background(), "Adding extra group %q", name)
		authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
	}

	if b.isOwner(authInfo.UserInfo.Name) {
		// Add the owner extra groups to the user info.
		for _, name := range b.cfg.ownerExtraGroups {
			log.Debugf(context.Background(), "Adding owner extra group %q", name)
			authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
		}
	}

	if session.isOffline {
		return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
	}

	if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
		log.Errorf(context.Background(), "Failed to store token: %s", err)
		return AuthDenied, unexpectedErrMsg("failed to store token")
	}

	return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
}

func (b *Broker) newPassword(session *session, secret string) (string, isAuthenticatedDataResponse) {
	if secret == "" {
		return AuthRetry, unexpectedErrMsg("empty secret")
	}

	// This mode must always come after an authentication mode, so we should have auth info from the previous mode
	// stored in the session.
	authInfo := session.authInfo
	if authInfo == nil {
		log.Error(context.Background(), "auth info is not set")
		return AuthDenied, unexpectedErrMsg("auth info is not set")
	}

	if err := password.HashAndStorePassword(secret, session.passwordPath); err != nil {
		log.Errorf(context.Background(), "Failed to store password: %s", err)
		return AuthDenied, unexpectedErrMsg("failed to store password")
	}

	return b.finishAuth(session, authInfo)
}

// userNameIsAllowed checks whether the user's username is allowed to access the machine.
func (b *Broker) userNameIsAllowed(userName string) bool {
	return b.cfg.userNameIsAllowed(b.provider.NormalizeUsername(userName))
}

// isOwner returns true if the user is the owner of the machine.
func (b *Broker) isOwner(userName string) bool {
	return b.cfg.owner == b.provider.NormalizeUsername(userName)
}

func (b *Broker) userNotAllowedLogMsg(userName string) string {
	logMsg := fmt.Sprintf("User %q is not in the list of allowed users.", userName)
	logMsg += fmt.Sprintf("\nYou can add the user to allowed_users in %s", b.cfg.ConfigFile)
	return logMsg
}

func (b *Broker) startAuthenticate(sessionID string) (context.Context, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	if session.isAuthenticating != nil {
		log.Errorf(context.Background(), "Authentication already running for session %q", sessionID)
		return nil, errors.New("authentication already running for this user session")
	}

	ctx, cancel := context.WithCancel(context.Background())
	session.isAuthenticating = &isAuthenticatedCtx{ctx: ctx, cancelFunc: cancel}

	if err := b.updateSession(sessionID, session); err != nil {
		cancel()
		return nil, err
	}

	return ctx, nil
}

// EndSession ends the session for the user.
func (b *Broker) EndSession(sessionID string) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}

	// Checks if there is a isAuthenticated call running for this session and cancels it before ending the session.
	if session.isAuthenticating != nil {
		b.CancelIsAuthenticated(sessionID)
	}

	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()
	delete(b.currentSessions, sessionID)
	return nil
}

// CancelIsAuthenticated cancels the IsAuthenticated call for the user.
func (b *Broker) CancelIsAuthenticated(sessionID string) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return
	}

	if session.isAuthenticating == nil {
		return
	}

	session.isAuthenticating.cancelFunc()
	session.isAuthenticating = nil

	if err := b.updateSession(sessionID, session); err != nil {
		log.Errorf(context.Background(), "Error when cancelling IsAuthenticated: %v", err)
	}
}

// UserPreCheck checks if the user is valid and can be allowed to authenticate.
// It returns the user info in JSON format if the user is valid, or an empty string if the user is not allowed.
func (b *Broker) UserPreCheck(username string) (string, error) {
	found := false
	for _, suffix := range b.cfg.allowedSSHSuffixes {
		if suffix == "" {
			continue
		}

		// If suffix is only "*", TrimPrefix will return the empty string and that works for the 'match all' case also.
		suffix = strings.TrimPrefix(suffix, "*")
		if strings.HasSuffix(username, suffix) {
			found = true
			break
		}
	}

	if !found {
		// The username does not match any of the allowed suffixes.
		return "", nil
	}

	u := info.NewUser(username, filepath.Join(b.cfg.homeBaseDir, username), "", "", "", nil)
	encoded, err := json.Marshal(u)
	if err != nil {
		return "", fmt.Errorf("could not marshal user info: %v", err)
	}
	return string(encoded), nil
}

// getSession returns the session information for the specified session ID or an error if the session is not active.
func (b *Broker) getSession(sessionID string) (session, error) {
	b.currentSessionsMu.RLock()
	defer b.currentSessionsMu.RUnlock()
	s, active := b.currentSessions[sessionID]
	if !active {
		return session{}, fmt.Errorf("%s is not a current transaction", sessionID)
	}
	return s, nil
}

// updateSession checks if the session is still active and updates the session info.
func (b *Broker) updateSession(sessionID string, session session) error {
	// Checks if the session was ended in the meantime, otherwise we would just accidentally recreate it.
	if _, err := b.getSession(sessionID); err != nil {
		return err
	}
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()
	b.currentSessions[sessionID] = session
	return nil
}

// refreshToken refreshes the OAuth2 token and returns the updated AuthCachedInfo.
func (b *Broker) refreshToken(ctx context.Context, session *session, oldToken *token.AuthCachedInfo) (*token.AuthCachedInfo, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()
	// set cached token expiry time to one hour in the past
	// this makes sure the token is refreshed even if it has not 'actually' expired
	oldToken.Token.Expiry = time.Now().Add(-time.Hour)
	oauthToken, err := session.oauth2Config.TokenSource(timeoutCtx, oldToken.Token).Token()
	if err != nil {
		return nil, err
	}

	// Update the raw ID token
	rawIDToken, ok := oauthToken.Extra("id_token").(string)
	if !ok {
		log.Debug(context.Background(), "refreshed token does not contain an ID token, keeping the old one")
		rawIDToken = oldToken.RawIDToken
	}

	t := token.NewAuthCachedInfo(oauthToken, rawIDToken, b.provider)
	t.ProviderMetadata = oldToken.ProviderMetadata
	t.DeviceRegistrationData = oldToken.DeviceRegistrationData

	t.UserInfo, err = b.userInfoFromIDToken(ctx, session, rawIDToken)
	if err != nil {
		return nil, err
	}

	t.UserInfo.Groups = oldToken.UserInfo.Groups

	return t, nil
}

// userInfoFromIDToken verifies and parses the raw ID token and returns the user info from it.
// Note that verifying the ID token requires a working network connection to the provider's JWKs endpoint,
// so make sure to only call this function if the session is online.
func (b *Broker) userInfoFromIDToken(ctx context.Context, session *session, rawIDToken string) (info.User, error) {
	idToken, err := session.oidcServer.Verifier(&b.oidcCfg).Verify(ctx, rawIDToken)
	if err != nil {
		return info.User{}, fmt.Errorf("could not verify token: %v", err)
	}

	userInfo, err := b.provider.GetUserInfo(idToken)
	if err != nil {
		return info.User{}, err
	}

	if err = b.provider.VerifyUsername(session.username, userInfo.Name); err != nil {
		return info.User{}, fmt.Errorf("username verification failed: %w", err)
	}

	// This means that home was not provided by the claims, so we need to set it to the broker default.
	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}

	return userInfo, nil
}

func (b *Broker) getGroups(ctx context.Context, session *session, t *token.AuthCachedInfo) ([]info.Group, error) {
	if session.isOffline {
		return nil, errors.New("session is in offline mode")
	}

	return b.provider.GetGroups(ctx,
		b.cfg.clientID,
		b.cfg.issuerURL,
		t.Token,
		t.ProviderMetadata,
		t.DeviceRegistrationData,
	)
}

// Checks if the provided error is of type ForDisplayError. If it is, it returns the error message. Else, it returns
// the provided fallback message.
func errorMessageForDisplay(err error, fallback string) errorMessage {
	var forDisplayErr *providerErrors.ForDisplayError
	if errors.As(err, &forDisplayErr) {
		return errorMessage{Message: forDisplayErr.Error()}
	}
	return errorMessage{Message: fallback}
}
