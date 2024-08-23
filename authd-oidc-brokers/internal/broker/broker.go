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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/decorate"
	"golang.org/x/exp/slog"
	"golang.org/x/oauth2"
)

const (
	maxAuthAttempts    = 3
	maxRequestDuration = 5 * time.Second
)

// Config is the configuration for the broker.
type Config struct {
	IssuerURL          string
	ClientID           string
	CachePath          string
	HomeBaseDir        string
	AllowedSSHSuffixes []string
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	providerInfo providers.ProviderInfoer
	issuerURL    string
	oidcCfg      oidc.Config

	cachePath          string
	homeDirPath        string
	allowedSSHSuffixes []string

	currentSessions   map[string]sessionInfo
	currentSessionsMu sync.RWMutex

	privateKey *rsa.PrivateKey
}

type sessionInfo struct {
	username string
	lang     string
	mode     string

	selectedMode      string
	firstSelectedMode string
	authModes         []string
	attemptsPerMode   map[string]int

	authCfg   authConfig
	authInfo  map[string]any
	isOffline bool
	cachePath string

	currentAuthStep int

	isAuthenticating *isAuthenticatedCtx
}

// authConfig holds the required values for authenticating a user with OIDC.
type authConfig struct {
	provider *oidc.Provider
	oauth    oauth2.Config
}

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

type option struct {
	// skipJWTSignatureCheck is used to skip the JWT validation done by the oidc web server.
	skipJWTSignatureCheck bool
	providerInfo          providers.ProviderInfoer
}

// Option is a func that allows to override some of the broker default settings.
type Option func(*option)

// New returns a new oidc Broker with the providers listed in the configuration file.
func New(cfg Config, args ...Option) (b *Broker, err error) {
	defer decorate.OnError(&err, "could not create broker with provided issuer and client ID")

	opts := option{
		// This is to avoid too much complexity in the tests.
		skipJWTSignatureCheck: false,
		providerInfo:          providers.CurrentProviderInfo(),
	}
	for _, arg := range args {
		arg(&opts)
	}

	if cfg.CachePath == "" {
		return &Broker{}, errors.New("cache path must be provided")
	}

	if cfg.IssuerURL == "" || cfg.ClientID == "" {
		return &Broker{}, errors.New("issuer and client ID must be provided")
	}

	homeDirPath := "/home"
	if cfg.HomeBaseDir != "" {
		homeDirPath = cfg.HomeBaseDir
	}

	// Generate a new private key for the broker.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("could not create an valid rsa key: %v", err))
	}

	b = &Broker{
		providerInfo:       opts.providerInfo,
		issuerURL:          cfg.IssuerURL,
		oidcCfg:            oidc.Config{ClientID: cfg.ClientID, InsecureSkipSignatureCheck: opts.skipJWTSignatureCheck},
		cachePath:          cfg.CachePath,
		homeDirPath:        homeDirPath,
		allowedSSHSuffixes: cfg.AllowedSSHSuffixes,
		privateKey:         privateKey,

		currentSessions:   make(map[string]sessionInfo),
		currentSessionsMu: sync.RWMutex{},
	}
	return b, nil
}

// NewSession creates a new session for the user.
func (b *Broker) NewSession(username, lang, mode string) (sessionID, encryptionKey string, err error) {
	defer decorate.OnError(&err, "could not create new session for user %q", username)

	sessionID = uuid.New().String()
	session := sessionInfo{
		username: username,
		lang:     lang,
		mode:     mode,

		authInfo:        make(map[string]any),
		attemptsPerMode: make(map[string]int),
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&b.privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	_, url, _ := strings.Cut(b.issuerURL, "://")
	url = strings.ReplaceAll(url, "/", "_")
	url = strings.ReplaceAll(url, ":", "_")
	session.cachePath = filepath.Join(b.cachePath, url, username+".cache")

	// Check whether to start the session in offline mode.
	session.authCfg, err = b.connectToProvider(context.Background())
	if err != nil {
		slog.Debug(fmt.Sprintf("Could not connect to the provider: %v. Starting session in offline mode.", err))
		session.isOffline = true
	}

	b.currentSessionsMu.Lock()
	b.currentSessions[sessionID] = session
	b.currentSessionsMu.Unlock()

	return sessionID, base64.StdEncoding.EncodeToString(pubASN1), nil
}

func (b *Broker) connectToProvider(ctx context.Context) (authCfg authConfig, err error) {
	ctx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, b.issuerURL)
	if err != nil {
		return authConfig{}, err
	}

	oauthCfg := oauth2.Config{
		ClientID: b.oidcCfg.ClientID,
		Endpoint: provider.Endpoint(),
		Scopes:   append([]string{oidc.ScopeOpenID, "profile", "email"}, b.providerInfo.AdditionalScopes()...),
	}

	return authConfig{provider: provider, oauth: oauthCfg}, nil
}

// GetAuthenticationModes returns the authentication modes available for the user.
func (b *Broker) GetAuthenticationModes(sessionID string, supportedUILayouts []map[string]string) (authModes []map[string]string, err error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	supportedAuthModes := b.supportedAuthModesFromLayout(supportedUILayouts)

	// Checks if the token exists in the cache.
	_, err = os.Stat(session.cachePath)
	tokenExists := err == nil

	endpoints := make(map[string]struct{})
	if session.authCfg.provider != nil && session.authCfg.provider.Endpoint().DeviceAuthURL != "" {
		endpoints["device_auth"] = struct{}{}
	}

	availableModes, err := b.providerInfo.CurrentAuthenticationModesOffered(
		session.mode,
		supportedAuthModes,
		tokenExists,
		!session.isOffline,
		endpoints,
		session.currentAuthStep)
	if err != nil {
		return nil, err
	}

	for _, id := range availableModes {
		authModes = append(authModes, map[string]string{
			"id":    id,
			"label": supportedAuthModes[id],
		})
	}

	if len(authModes) == 0 {
		return nil, fmt.Errorf("no authentication modes available for user %q", session.username)
	}

	session.authModes = availableModes
	if err := b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	return authModes, nil
}

func (b *Broker) supportedAuthModesFromLayout(supportedUILayouts []map[string]string) (supportedModes map[string]string) {
	supportedModes = make(map[string]string)
	for _, layout := range supportedUILayouts {
		supportedEntries := strings.Split(strings.TrimPrefix(layout["entry"], "optional:"), ",")
		switch layout["type"] {
		case "qrcode":
			if !strings.Contains(layout["wait"], "true") {
				continue
			}
			supportedModes["device_auth"] = "Device Authentication"

		case "form":
			if slices.Contains(supportedEntries, "chars_password") {
				supportedModes["password"] = "Local Password Authentication"
			}

		case "newpassword":
			if slices.Contains(supportedEntries, "chars_password") {
				supportedModes["newpassword"] = "Define your local password"
			}
		}
	}

	return supportedModes
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
	// Store the first one to use to update the lastSelectedMode in MFA cases.
	if session.currentAuthStep == 0 {
		session.firstSelectedMode = authModeID
	}

	if err = b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	return uiLayoutInfo, nil
}

func (b *Broker) generateUILayout(session *sessionInfo, authModeID string) (map[string]string, error) {
	if !slices.Contains(session.authModes, authModeID) {
		return nil, fmt.Errorf("selected authentication mode %q does not exist", authModeID)
	}

	var uiLayout map[string]string
	switch authModeID {
	case "device_auth":
		ctx, cancel := context.WithTimeout(context.Background(), maxRequestDuration)
		defer cancel()
		response, err := session.authCfg.oauth.DeviceAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not generate QR code layout: %v", err)
		}
		session.authInfo["response"] = response

		uiLayout = map[string]string{
			"type": "qrcode",
			"label": fmt.Sprintf(
				"Scan the QR code or access %q and use the provided login code",
				response.VerificationURI,
			),
			"wait":    "true",
			"button":  "Request new login code",
			"content": response.VerificationURI,
			"code":    response.UserCode,
		}

	case "password":
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Enter your local password",
			"entry": "chars_password",
		}

	case "newpassword":
		label := "Create a local password"
		if session.mode == "passwd" {
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
		msg, _ := json.Marshal(errorMessage{Message: "authentication request cancelled"})
		return AuthCancelled, string(msg), ctx.Err()
	}

	switch access {
	case AuthRetry:
		session.attemptsPerMode[session.selectedMode]++
		if session.attemptsPerMode[session.selectedMode] == maxAuthAttempts {
			access = AuthDenied
			iadResponse = errorMessage{Message: "maximum number of attempts reached"}
		}

	case AuthNext:
		session.currentAuthStep++
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

func (b *Broker) handleIsAuthenticated(ctx context.Context, session *sessionInfo, authData map[string]string) (access string, data isAuthenticatedDataResponse) {
	defer decorateErrorMessage(&data, "authentication failure")

	// Decrypt challenge if present.
	challenge, err := decodeRawChallenge(b.privateKey, authData["challenge"])
	if err != nil {
		slog.Error(err.Error())
		return AuthRetry, errorMessage{Message: "could not decode challenge"}
	}

	var authInfo authCachedInfo
	switch session.selectedMode {
	case "device_auth":
		response, ok := session.authInfo["response"].(*oauth2.DeviceAuthResponse)
		if !ok {
			slog.Error("could not get device auth response")
			return AuthDenied, errorMessage{Message: "could not get required response"}
		}

		if response.Expiry.IsZero() {
			response.Expiry = time.Now().Add(time.Hour)
		}
		expiryCtx, cancel := context.WithDeadline(ctx, response.Expiry)
		defer cancel()
		t, err := session.authCfg.oauth.DeviceAccessToken(expiryCtx, response, b.providerInfo.AuthOptions()...)
		if err != nil {
			slog.Error(err.Error())
			return AuthRetry, errorMessage{Message: "could not authenticate user remotely"}
		}

		rawIDToken, ok := t.Extra("id_token").(string)
		if !ok {
			slog.Error("could not get ID token")
			return AuthDenied, errorMessage{Message: "could not get ID token"}
		}

		authInfo = authCachedInfo{Token: t, RawIDToken: rawIDToken}
		authInfo.UserInfo, err = b.fetchUserInfo(ctx, session, &authInfo)
		if err != nil {
			slog.Error(err.Error())
			return AuthDenied, errorMessage{Message: "could not fetch user info"}
		}

		session.authInfo["auth_info"] = authInfo
		return AuthNext, nil

	case "password":
		authInfo, err = b.loadAuthInfo(ctx, session, challenge)
		if err != nil {
			slog.Error(err.Error())
			return AuthRetry, errorMessage{Message: "could not load cached info"}
		}

		if authInfo.UserInfo.Name == "" {
			authInfo.UserInfo, err = b.fetchUserInfo(ctx, session, &authInfo)
			if err != nil {
				slog.Error(err.Error())
				return AuthDenied, errorMessage{Message: "could not fetch user info"}
			}
		}

		if session.mode == "passwd" {
			session.authInfo["auth_info"] = authInfo
			return AuthNext, nil
		}

	case "newpassword":
		if challenge == "" {
			return AuthRetry, errorMessage{Message: "empty challenge"}
		}

		var ok bool
		// This mode must always come after a authentication mode, so it has to have an auth_info.
		authInfo, ok = session.authInfo["auth_info"].(authCachedInfo)
		if !ok {
			slog.Error("could not get required information")
			return AuthDenied, errorMessage{Message: "could not get required information"}
		}
	}

	if session.isOffline {
		return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
	}

	if err := b.cacheAuthInfo(session, authInfo, challenge); err != nil {
		slog.Error(err.Error())
		return AuthDenied, errorMessage{Message: "could not cache user info"}
	}

	return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
}

func (b *Broker) startAuthenticate(sessionID string) (context.Context, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	if session.isAuthenticating != nil {
		slog.Error(fmt.Sprintf("Authentication already running for session %q", sessionID))
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
		slog.Error(fmt.Sprintf("Error when cancelling IsAuthenticated: %v", err))
	}
}

// UserPreCheck checks if the user is valid and can be allowed to authenticate.
func (b *Broker) UserPreCheck(username string) (string, error) {
	found := false
	for _, suffix := range b.allowedSSHSuffixes {
		if strings.HasSuffix(username, suffix) {
			found = true
			break
		}
	}

	if !found {
		return "", errors.New("username does not match the allowed suffixes")
	}

	u := info.NewUser(username, filepath.Join(b.homeDirPath, username), "", "", "", nil)
	encoded, err := json.Marshal(u)
	if err != nil {
		return "", fmt.Errorf("could not marshal user info: %v", err)
	}
	return string(encoded), nil
}

// getSession returns the session information for the specified session ID or an error if the session is not active.
func (b *Broker) getSession(sessionID string) (sessionInfo, error) {
	b.currentSessionsMu.RLock()
	defer b.currentSessionsMu.RUnlock()
	session, active := b.currentSessions[sessionID]
	if !active {
		return sessionInfo{}, fmt.Errorf("%s is not a current transaction", sessionID)
	}
	return session, nil
}

// updateSession checks if the session is still active and updates the session info.
func (b *Broker) updateSession(sessionID string, session sessionInfo) error {
	// Checks if the session was ended in the meantime, otherwise we would just accidentally recreate it.
	if _, err := b.getSession(sessionID); err != nil {
		return err
	}
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()
	b.currentSessions[sessionID] = session
	return nil
}

// authCachedInfo represents the token that will be saved on disk for offline authentication.
type authCachedInfo struct {
	Token      *oauth2.Token
	RawIDToken string
	UserInfo   info.User
}

// cacheAuthInfo serializes the access token and cache it.
func (b *Broker) cacheAuthInfo(session *sessionInfo, authInfo authCachedInfo, password string) (err error) {
	content, err := json.Marshal(authInfo)
	if err != nil {
		return fmt.Errorf("could not marshal token: %v", err)
	}

	serialized, err := encrypt(content, []byte(password))
	if err != nil {
		return fmt.Errorf("could not encrypt token: %v", err)
	}

	// Create issuer specific cache directory if it doesn't exist.
	if err = os.MkdirAll(filepath.Dir(session.cachePath), 0700); err != nil {
		return fmt.Errorf("could not create token directory: %v", err)
	}

	if err = os.WriteFile(session.cachePath, serialized, 0600); err != nil {
		return fmt.Errorf("could not save token: %v", err)
	}

	return nil
}

// loadAuthInfo deserializes the token from the cache and refreshes it if needed.
func (b *Broker) loadAuthInfo(ctx context.Context, session *sessionInfo, password string) (loadedInfo authCachedInfo, err error) {
	s, err := os.ReadFile(session.cachePath)
	if err != nil {
		return authCachedInfo{}, fmt.Errorf("could not read token: %v", err)
	}

	deserialized, err := decrypt(s, []byte(password))
	if err != nil {
		return authCachedInfo{}, fmt.Errorf("could not deserialize token: %v", err)
	}

	var cachedInfo authCachedInfo
	if err := json.Unmarshal(deserialized, &cachedInfo); err != nil {
		return authCachedInfo{}, fmt.Errorf("could not unmarshal token: %v", err)
	}

	// If the token is still valid, we return it. Ideally, we would refresh it online, but the TokenSource API also uses
	// this logic to decide whether the token needs refreshing, so we should run it early to control the returned values.
	if cachedInfo.Token.Valid() || session.isOffline {
		return cachedInfo, nil
	}

	// Tries to refresh the access token. If the service is unavailable, we allow authentication.
	timeoutCtx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()
	tok, err := session.authCfg.oauth.TokenSource(timeoutCtx, cachedInfo.Token).Token()
	if err != nil {
		return authCachedInfo{}, fmt.Errorf("could not refresh token: %v", err)
	}

	// If the ID token was refreshed, we overwrite the cached one.
	refreshedIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		refreshedIDToken = cachedInfo.RawIDToken
	}

	return authCachedInfo{Token: tok, RawIDToken: refreshedIDToken}, nil
}

func (b *Broker) fetchUserInfo(ctx context.Context, session *sessionInfo, t *authCachedInfo) (userInfo info.User, err error) {
	if session.isOffline {
		return info.User{}, errors.New("session is in offline mode")
	}

	idToken, err := session.authCfg.provider.Verifier(&b.oidcCfg).Verify(ctx, t.RawIDToken)
	if err != nil {
		return info.User{}, fmt.Errorf("could not verify token: %v", err)
	}

	userInfo, err = b.providerInfo.GetUserInfo(ctx, t.Token, idToken)
	if err != nil {
		return info.User{}, fmt.Errorf("could not get user info: %v", err)
	}

	// Some providers are case-insensitive, but we are not. So we need to lowercase the returned username.
	if !strings.EqualFold(userInfo.Name, session.username) {
		return info.User{}, fmt.Errorf("returned user %q does not match the selected one %q", userInfo.Name, session.username)
	}

	// This means that home was not provided by the claims, so we need to set it to the broker default.
	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.homeDirPath, userInfo.Home)
	}

	return userInfo, err
}

// decorateErrorMessage decorates the isAuthenticatedDataResponse with the provided message, if it's an errorMessage.
func decorateErrorMessage(data *isAuthenticatedDataResponse, msg string) {
	if *data == nil {
		return
	}
	errMsg, ok := (*data).(errorMessage)
	if !ok {
		return
	}
	errMsg.Message = fmt.Sprintf("%s: %s", msg, errMsg.Message)
	*data = errMsg
}
