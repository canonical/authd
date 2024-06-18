// Package broker is the generic oidc business code.
package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/ubuntu/decorate"
	"github.com/ubuntu/oidc-broker/internal/providers"
	"github.com/ubuntu/oidc-broker/internal/providers/group"
	"golang.org/x/exp/slog"
	"golang.org/x/oauth2"
)

const maxAuthAttempts = 3

// Config is the configuration for the broker.
type Config struct {
	IssuerURL   string
	ClientID    string
	CachePath   string
	HomeBaseDir string
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	providerInfo providers.ProviderInfoer
	auth         authConfig
	homeDirPath  string

	currentSessions   map[string]sessionInfo
	currentSessionsMu sync.RWMutex

	privateKey *rsa.PrivateKey
}

type authConfig struct {
	cachePath string

	provider    *oidc.Provider
	providerURL string

	oidcCfg  oidc.Config
	oauthCfg oauth2.Config
}

type sessionInfo struct {
	username string
	lang     string
	mode     string

	selectedMode      string
	firstSelectedMode string
	supportedModes    map[string]string
	attemptsPerMode   map[string]int

	authInfo  map[string]any
	cachePath string

	currentAuthStep int

	isAuthenticating *isAuthenticatedCtx
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

	clientID := cfg.ClientID
	issuerURL := cfg.IssuerURL
	if issuerURL == "" || clientID == "" {
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

	// Create provider
	provider, err := oidc.NewProvider(context.TODO(), issuerURL)
	if err != nil {
		return &Broker{}, err
	}
	oidcCfg := oidc.Config{
		ClientID:                   clientID,
		InsecureSkipSignatureCheck: opts.skipJWTSignatureCheck,
	}
	oauthCfg := oauth2.Config{
		ClientID: clientID,
		Endpoint: provider.Endpoint(),
		Scopes:   append([]string{oidc.ScopeOpenID, "profile", "email"}, opts.providerInfo.AdditionalScopes()...),
	}
	authCfg := authConfig{
		provider:    provider,
		providerURL: issuerURL,
		cachePath:   cfg.CachePath,
		oidcCfg:     oidcCfg,
		oauthCfg:    oauthCfg,
	}

	return &Broker{
		providerInfo: opts.providerInfo,
		auth:         authCfg,
		homeDirPath:  homeDirPath,
		privateKey:   privateKey,

		currentSessions:   make(map[string]sessionInfo),
		currentSessionsMu: sync.RWMutex{},
	}, nil
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

	_, url, _ := strings.Cut(b.auth.providerURL, "://")
	url = strings.ReplaceAll(url, "/", "_")
	url = strings.ReplaceAll(url, ":", "_")
	session.cachePath = filepath.Join(b.auth.cachePath, url, username+".cache")

	b.currentSessionsMu.Lock()
	b.currentSessions[sessionID] = session
	b.currentSessionsMu.Unlock()

	return sessionID, base64.StdEncoding.EncodeToString(pubASN1), nil
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

	availableModes, err := b.providerInfo.CurrentAuthenticationModesOffered(
		session.mode,
		supportedAuthModes,
		tokenExists,
		session.currentAuthStep)
	if err != nil {
		return nil, err
	}

	for _, id := range availableModes {
		label, ok := supportedAuthModes[id]
		if !ok {
			return nil, fmt.Errorf("required mode %q is not supported", id)
		}
		authModes = append(authModes, map[string]string{
			"id":    id,
			"label": label,
		})
	}

	session.supportedModes = supportedAuthModes
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
			supportedModes["qrcode"] = "Device Authentication"

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
	if _, exists := session.supportedModes[authModeID]; !exists {
		return nil, fmt.Errorf("selected authentication mode %q does not exist", authModeID)
	}

	var uiLayout map[string]string
	switch authModeID {
	case "qrcode":
		response, err := b.auth.oauthCfg.DeviceAuth(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("could not generate QR code layout: %v", err)
		}
		session.authInfo["response"] = response

		uiLayout = map[string]string{
			"type": "qrcode",
			"label": fmt.Sprintf(
				"Scan the QR code or access %q and use the code %q",
				response.VerificationURI,
				response.UserCode,
			),
			"wait":    "true",
			"button":  "regenerate QR code",
			"content": response.VerificationURI,
		}

	case "password":
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Enter your local password",
			"entry": "chars_password",
		}

	case "newpassword":
		uiLayout = map[string]string{
			"type":  "newpassword",
			"label": "Create a local password",
			"entry": "chars_password",
		}
	}

	return uiLayout, nil
}

// IsAuthenticated evaluates the provided authenticationData and returns the authentication status for the user.
func (b *Broker) IsAuthenticated(sessionID, authenticationData string) (string, string, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return AuthDenied, "", err
	}

	var authData map[string]string
	if authenticationData != "" {
		if err := json.Unmarshal([]byte(authenticationData), &authData); err != nil {
			return AuthDenied, "", fmt.Errorf("authentication data is not a valid json value: %v", err)
		}
	}

	ctx, err := b.startAuthenticate(sessionID)
	if err != nil {
		return AuthDenied, "", err
	}

	// Cleans up the IsAuthenticated context when the call is done.
	defer b.CancelIsAuthenticated(sessionID)

	authDone := make(chan struct{})
	var access, data string
	go func() {
		access, data = b.handleIsAuthenticated(ctx, &session, authData)
		close(authDone)
	}()

	select {
	case <-authDone:
	case <-ctx.Done():
		return AuthCancelled, `{"message": "authentication request cancelled"}`, ctx.Err()
	}

	switch access {
	case AuthRetry:
		session.attemptsPerMode[session.selectedMode]++
		if session.attemptsPerMode[session.selectedMode] == maxAuthAttempts {
			access = AuthDenied
			data = `{"message": "maximum number of attempts reached"}`
		}

	case AuthNext:
		session.currentAuthStep++
	}

	if err = b.updateSession(sessionID, session); err != nil {
		return AuthDenied, "", err
	}
	return access, data, nil
}

func (b *Broker) handleIsAuthenticated(ctx context.Context, session *sessionInfo, authData map[string]string) (access, data string) {
	// Decrypt challenge if present.
	challenge, err := decodeRawChallenge(b.privateKey, authData["challenge"])
	if err != nil {
		return AuthRetry, fmt.Sprintf(`{"message": "could not decode challenge: %v"}`, err)
	}

	var authInfo authCachedInfo
	var userClaims claims
	var groups []group.Info

	offline := false
	switch session.selectedMode {
	case "qrcode":
		response, ok := session.authInfo["response"].(*oauth2.DeviceAuthResponse)
		if !ok {
			return AuthDenied, `{"message": "could not get required response"}`
		}

		t, err := b.auth.oauthCfg.DeviceAccessToken(ctx, response, b.providerInfo.AuthOptions()...)
		if err != nil {
			return AuthRetry, fmt.Sprintf(`{"message": "could not authenticate user: %v"}`, err)
		}

		rawIDToken, ok := t.Extra("id_token").(string)
		if !ok {
			return AuthDenied, `{"message": "could not get id_token"}`
		}

		session.authInfo["auth_info"] = authCachedInfo{Token: t, RawIDToken: rawIDToken}
		return AuthNext, ""

	case "password":
		authInfo, offline, err = b.loadAuthInfo(session, challenge)
		if err != nil {
			return AuthRetry, fmt.Sprintf(`{"message": "could not authenticate user: %v"}`, err)
		}

		if session.mode == "passwd" {
			session.authInfo["auth_info"] = authInfo
			return AuthNext, ""
		}

	case "newpassword":
		var ok bool
		// This mode must always come after a authentication mode, so it has to have an auth_info.
		authInfo, ok = session.authInfo["auth_info"].(authCachedInfo)
		if !ok {
			return AuthDenied, `{"message": "could not get required information"}`
		}
	}

	if authInfo.UserInfo == "" {
		userClaims, groups, err = b.fetchUserInfo(ctx, session, &authInfo)
		if err != nil {
			return AuthDenied, fmt.Sprintf(`{"message": "could not get user info: %v"}`, err)
		}

		authInfo.UserInfo, err = b.userInfoFromClaims(userClaims, groups)
		if err != nil {
			return AuthDenied, fmt.Sprintf(`{"message": "could not parse user info from claims: %v"}`, err)
		}
	}

	if offline {
		return AuthGranted, fmt.Sprintf(`{"userinfo": %s}`, authInfo.UserInfo)
	}

	if err := b.cacheAuthInfo(session, authInfo, challenge); err != nil {
		return AuthRetry, fmt.Sprintf(`{"message": "could not update cached info: %v"}`, err)
	}

	return AuthGranted, fmt.Sprintf(`{"userinfo": %s}`, authInfo.UserInfo)
}

func (b *Broker) startAuthenticate(sessionID string) (context.Context, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	if session.isAuthenticating != nil {
		return nil, fmt.Errorf("IsAuthenticated already running for session %q", sessionID)
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
	AcquiredAt time.Time
	RawIDToken string
	UserInfo   string
}

// cacheAuthInfo serializes the access token and cache it.
func (b *Broker) cacheAuthInfo(session *sessionInfo, authInfo authCachedInfo, password string) (err error) {
	defer decorate.OnError(&err, "could not cache info")

	authInfo.AcquiredAt = time.Now()
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
func (b *Broker) loadAuthInfo(session *sessionInfo, password string) (loadedInfo authCachedInfo, offline bool, err error) {
	defer decorate.OnError(&err, "could not load cached info")

	s, err := os.ReadFile(session.cachePath)
	if err != nil {
		return authCachedInfo{}, false, fmt.Errorf("could not read token: %v", err)
	}

	deserialized, err := decrypt(s, []byte(password))
	if err != nil {
		return authCachedInfo{}, false, fmt.Errorf("could not deserialize token: %v", err)
	}

	var cachedInfo authCachedInfo
	if err := json.Unmarshal(deserialized, &cachedInfo); err != nil {
		return authCachedInfo{}, false, fmt.Errorf("could not unmarshal token: %v", err)
	}

	// Tries to refresh the access token. If the service is unavailable, we allow authentication.
	tok, err := b.auth.oauthCfg.TokenSource(context.Background(), cachedInfo.Token).Token()
	if err != nil {
		castErr := &oauth2.RetrieveError{}
		if !errors.As(err, &castErr) || castErr.Response.StatusCode != http.StatusServiceUnavailable {
			return authCachedInfo{}, false, fmt.Errorf("could not refresh token: %v", err)
		}

		// The provider is unavailable, so we allow offline authentication.
		return cachedInfo, true, nil
	}

	// If the ID token was refreshed, we overwrite the cached one.
	refreshedIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		refreshedIDToken = cachedInfo.RawIDToken
	}

	return authCachedInfo{Token: tok, RawIDToken: refreshedIDToken}, false, nil
}

func (b *Broker) fetchUserInfo(ctx context.Context, session *sessionInfo, t *authCachedInfo) (userClaims claims, userGroups []group.Info, err error) {
	defer decorate.OnError(&err, "could not fetch user info")

	// If we didn't restore user information from the cache, we need to query the provider for it, which means
	// we need to validate the token.
	idToken, err := b.auth.provider.Verifier(&b.auth.oidcCfg).Verify(ctx, t.RawIDToken)
	if err != nil {
		return claims{}, nil, fmt.Errorf("could not verify token: %v", err)
	}

	userGroups, err = b.providerInfo.GetGroups(t.Token)
	if err != nil {
		return claims{}, nil, fmt.Errorf("could not get user groups: %v", err)
	}

	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, nil, fmt.Errorf("could not get user info: %v", err)
	}

	if userClaims.Email == "" {
		return claims{}, nil, errors.New("user email is required, but was not provided")
	}

	if userClaims.Email != session.username {
		return claims{}, nil, fmt.Errorf("returned user %q does not match the selected one %q", userClaims.Email, session.username)
	}

	return userClaims, userGroups, nil
}

type claims struct {
	Name              string `json:"name"`
	PreferredUserName string `json:"preferred_username"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Sub               string `json:"sub"`
}

func (b *Broker) userInfoFromClaims(userClaims claims, groups []group.Info) (string, error) {
	user := struct {
		Name   string
		UUID   string
		Home   string
		Shell  string
		Gecos  string
		Groups []group.Info
	}{
		Name:   userClaims.Email,
		UUID:   userClaims.Sub,
		Home:   filepath.Join(b.homeDirPath, userClaims.Email),
		Shell:  "/usr/bin/bash",
		Gecos:  userClaims.Name,
		Groups: groups,
	}

	var buf bytes.Buffer
	err := template.Must(template.New("").Parse(`{
		"name": "{{.Name}}",
		"uuid": "{{.UUID}}",
		"gecos": "{{.Gecos}}",
		"dir": "{{.Home}}",
		"shell": "{{.Shell}}",
		"groups": [ {{range $index, $g := .Groups}}
			{{- if $index}}, {{end -}}
			{"name": "{{.Name}}", "ugid": "{{.UGID}}"}
		{{- end}} ]
}`)).Execute(&buf, user)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
