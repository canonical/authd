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
	"net"
	"os"
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
	"github.com/canonical/authd/log"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	// LatestAPIVersion is the latest API version supported by the broker. It should be incremented when a non backward
	// compatible change is made to the API.
	// Note: Remember to also bump the LatestAPIVersion in internal/brokers/dbusbroker.go.
	LatestAPIVersion uint = 2

	maxAuthAttempts    = 3
	maxRequestDuration = 5 * time.Second

	// maxMFAPollDuration caps the total wall-clock time spent polling for MFA
	// approval, to prevent infinite polling.
	maxMFAPollDuration = 5 * time.Minute
)

// reauthModes is the set of auth modes offered when the user must re-authenticate
// (e.g. after token revocation, expiry, or password change).
var reauthModes = []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr}

// Config is the configuration for the broker.
type Config struct {
	ConfigFile string
	DataDir    string

	userConfig
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	cfg        Config
	apiVersion uint

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
	mfaFlowActive      *himmelblau.MFAFlowState
	mfaChallengeInfo   *himmelblau.MFAChallengeInfo
	entraPasswordHash  string // pre-computed hash (not plaintext) for offline use

	isAuthenticating *isAuthenticatedCtx
}

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// verifyAndExtractEntraUserInfo verifies the Entra MFA access token's RS256
// signature against the tenant JWKS — defense-in-depth against a TLS MITM, since
// the claims on this path come from libhimmelblau decoding the token rather than a
// verified OIDC ID token — and extracts the user info from its claims. It does NOT
// cross-check the username against the session; first login does that via
// userInfoFromTokenExtras.
func (b *Broker) verifyAndExtractEntraUserInfo(ctx context.Context, token *oauth2.Token) (info.User, error) {
	ep, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider)
	if !ok {
		return info.User{}, errors.New("provider does not support Entra password authentication")
	}
	if err := ep.VerifyAccessToken(ctx, b.cfg.issuerURL, token.AccessToken); err != nil {
		return info.User{}, fmt.Errorf("access token verification failed: %w", err)
	}

	preferredUsername, _ := token.Extra("preferred_username").(string)
	if preferredUsername == "" {
		preferredUsername, _ = token.Extra("email").(string)
	}
	if preferredUsername == "" {
		return info.User{}, errors.New("token extras do not contain preferred_username")
	}

	sub, _ := token.Extra("sub").(string)
	gecos, _ := token.Extra("name").(string)
	userInfo := info.NewUser(preferredUsername, "", sub, "", gecos, nil)

	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}

	return userInfo, nil
}

// userInfoFromTokenExtras is verifyAndExtractEntraUserInfo plus a cross-check that
// the returned identity matches the username the user authenticated as. Used on
// first login (finishEntraAuth), where the username has not yet been bound to a
// verified identity.
func (b *Broker) userInfoFromTokenExtras(ctx context.Context, session *session, token *oauth2.Token) (info.User, error) {
	userInfo, err := b.verifyAndExtractEntraUserInfo(ctx, token)
	if err != nil {
		return info.User{}, err
	}

	if err := b.provider.VerifyUsername(session.username, userInfo.Name); err != nil {
		return info.User{}, fmt.Errorf("username verification failed: %w", err)
	}

	return userInfo, nil
}

// populateAuthInfo creates an AuthCachedInfo and populates it with provider
// metadata and user info. It returns the populated authInfo, or an auth
// response pair if a step fails.
//
// When userInfoOverride is nil, the default verified OIDC ID token path
// (getUserInfo) is used. Callers that already resolved user info through a
// different trust path (e.g. Entra MFA token extras) can pass it directly.
func (b *Broker) populateAuthInfo(ctx context.Context, session *session, t *oauth2.Token, rawIDToken string, userInfoOverride *info.User) (*token.AuthCachedInfo, string, isAuthenticatedDataResponse) {
	mp, mpOK := providers.ProviderAs[providers.MetadataProvider](b.provider)
	var extraFields map[string]interface{}
	if mpOK {
		extraFields = mp.GetExtraFields(t)
	}
	authInfo := token.NewAuthCachedInfo(t, rawIDToken, extraFields)

	var err error
	if mpOK {
		authInfo.ProviderMetadata, err = mp.GetMetadata(session.oidcServer)
		if err != nil {
			log.Errorf(context.Background(), "could not get provider metadata: %s", err)
			return nil, AuthDenied, unexpectedErrMsg("could not get provider metadata")
		}
	}

	if userInfoOverride != nil {
		authInfo.UserInfo = *userInfoOverride
	} else {
		authInfo.UserInfo, err = b.getUserInfo(ctx, session, t, rawIDToken, false)
	}
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return nil, AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return nil, AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}

	return authInfo, "", nil
}

type option struct {
	provider providers.Provider
}

// Option is a func that allows to override some of the broker default settings.
type Option func(*option)

// New returns a new oidc Broker with the providers listed in the configuration file.
func New(cfg Config, apiVersion uint, args ...Option) (b *Broker, err error) {
	p := providers.CurrentProvider()

	if cfg.ConfigFile != "" {
		cfg.userConfig, err = parseConfigFromPath(cfg.ConfigFile, p)
		if err != nil {
			return nil, err
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
	if _, ok := providers.ProviderAs[providers.DeviceRegisterer](opts.provider); ok && cfg.registerDevice {
		clientID = consts.MicrosoftBrokerAppID
	}

	b = &Broker{
		cfg:        cfg,
		apiVersion: apiVersion,
		provider:   opts.provider,
		oidcCfg:    oidc.Config{ClientID: clientID},
		privateKey: privateKey,

		currentSessions:   make(map[string]session),
		currentSessionsMu: sync.RWMutex{},
	}

	// If the provider supports app-only Graph API group lookup and a client secret
	// is configured, propagate the secret so it can use client credentials as
	// a fallback when the delegated token lacks GroupMember.Read.All.
	if setter, ok := providers.ProviderAs[providers.GraphClientSecretSetter](opts.provider); ok && cfg.clientSecret != "" {
		setter.SetGraphClientSecret(cfg.clientSecret)
	}

	return b, nil
}

// normalizedIssuer converts an issuer URL into a filesystem-safe directory name
// by stripping the scheme and replacing path/port separators with underscores.
func normalizedIssuer(issuerURL string) string {
	_, issuer, found := strings.Cut(issuerURL, "://")
	if !found {
		// If the issuer URL does not contain a scheme, use the whole issuer URL as the issuer.
		issuer = issuerURL
	}
	issuer = strings.ReplaceAll(issuer, "/", "_")
	issuer = strings.ReplaceAll(issuer, ":", "_")

	return issuer
}

// userDataDir returns the path to the broker's data directory for the given user.
// If the issuer URL or the username contains path traversal characters, an error is returned.
func (b *Broker) userDataDir(username string) (string, error) {
	if username == "" {
		return "", errors.New("username cannot be empty")
	}

	issuer := normalizedIssuer(b.cfg.issuerURL)
	issuerDataDir := filepath.Join(b.cfg.DataDir, issuer)
	// Check that the issuer does not contain path traversal characters by verifying that the resulting path is within
	// the data directory and the basename matches the issuer.
	if !strings.HasPrefix(issuerDataDir, b.cfg.DataDir) || filepath.Base(issuerDataDir) != issuer {
		return "", fmt.Errorf("invalid issuer URL %q: path traversal detected", b.cfg.issuerURL)
	}

	dir := filepath.Join(issuerDataDir, username)
	// Check that the username does not contain path traversal characters by verifying that the resulting path is within
	// the issuer data directory and the basename matches the username.
	if !strings.HasPrefix(dir, issuerDataDir) || filepath.Base(dir) != username {
		return "", fmt.Errorf("invalid username %q: path traversal detected", username)
	}

	return dir, nil
}

// NewSession creates a new session for the user.
func (b *Broker) NewSession(username, lang, mode string) (sessionID, encryptionKey string, err error) {
	if username == "" {
		return "", "", errors.New("username is required")
	}

	sessionID = uuid.New().String()
	s := session{
		username: username,
		lang:     lang,
		mode:     mode,

		attemptsPerMode: make(map[string]int),
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&b.privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal broker public key: %v", err)
	}

	s.userDataDir, err = b.userDataDir(username)
	if err != nil {
		return "", "", err
	}

	// The token is stored in $DATA_DIR/$ISSUER/$USERNAME/token.json.
	s.tokenPath = filepath.Join(s.userDataDir, "token.json")
	// The password is stored in $DATA_DIR/$ISSUER/$USERNAME/password.
	s.passwordPath = filepath.Join(s.userDataDir, "password")

	// Construct an OIDC provider via OIDC discovery.
	s.oidcServer, err = b.connectToOIDCServer(context.Background())
	if err != nil && b.cfg.forceAccessCheckWithProvider {
		log.Errorf(context.Background(), "Could not connect to the provider and force_access_check_with_provider is set, denying authentication: %v", err)
		//nolint:staticcheck,revive // ST1005 This error is displayed as is to the user, so it should be capitalized
		return "", "", errors.New("Error connecting to provider. Check your network connection.")
	}
	if err != nil {
		log.Noticef(context.Background(), "Could not connect to the provider, starting session in offline mode: %v", err)
		s.isOffline = true
		s.providerConnectionError = err
	}

	scopes := append(consts.DefaultScopes, b.provider.AdditionalScopes()...)
	if _, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider); ok && b.cfg.registerDevice {
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
		modes := append([]string{authmodes.Password}, b.provider.SupportedOnlineAuthModes()...)
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

		dr, isDR := providers.ProviderAs[providers.DeviceRegisterer](b.provider)
		if !isDR {
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

		isTokenForDeviceRegistration := dr.IsTokenForDeviceRegistration(authInfo)

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
		if !b.cfg.flows.DeviceAuth {
			log.Debugf(context.Background(), "Device authentication is disabled in the [flows] config, so it is not available")
			return false
		}
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
	case authmodes.EntraPassword:
		if !b.cfg.flows.EntraPassword {
			log.Debugf(context.Background(), "The %q flow is disabled in the [flows] config, so it is not available", authmodes.EntraPassword)
			return false
		}
		if session.isOffline {
			log.Debugf(context.Background(), "Session is in offline mode, so Entra password authentication is not available")
			return false
		}
		if _, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider); !ok {
			return false
		}
		// The entra_password flow can only retrieve groups from Microsoft Graph
		// when device registration (PRT-based token exchange) or a client secret
		// (app-only client credentials) is available. Without either, every
		// entra_password login would fail at the group-fetch step, so don't offer
		// the mode rather than letting users hit an undiagnosable denial.
		//
		// This availability is decided here (per login) rather than at config-parse
		// time on purpose: an earlier version disabled the flow while parsing the
		// config (mutating the user's [flows] setting, and erroring out when
		// entra_password was the only enabled flow). That coupled config parsing to
		// provider capabilities and rejected otherwise-valid configs at startup.
		// The trade-off of deciding it here: if entra_password is the only enabled
		// flow and no group source is configured, the user is no longer rejected at
		// startup but instead sees "no authentication modes available" at login.
		if !b.cfg.registerDevice && b.cfg.clientSecret == "" {
			log.Debugf(context.Background(), "The %q flow requires %q to be enabled or a client secret to be configured to retrieve groups from Microsoft Graph, so it is not available", flowsEntraPasswordKey, registerDeviceKey)
			return false
		}
		return true
	case authmodes.EntraMFAWait, authmodes.EntraMFACode:
		// MFA follow-up modes are always available when offered via AuthNext.
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
		modes := b.supportedAuthModesFromLayout(layout)
		supportedModes = append(supportedModes, modes...)
	}
	return supportedModes
}

func (b *Broker) supportedAuthModesFromLayout(layout map[string]string) []string {
	supportedEntries := strings.Split(strings.TrimPrefix(layout["entry"], "optional:"), ",")
	switch layout["type"] {
	case "qrcode":
		if !strings.Contains(layout["wait"], "true") {
			return nil
		}
		if layout["renders_qrcode"] == "false" {
			return []string{authmodes.Device}
		}
		return []string{authmodes.DeviceQr}

	case "form":
		var modes []string
		if slices.Contains(supportedEntries, "chars_password") {
			modes = append(modes, authmodes.Password, authmodes.EntraPassword)
		}
		if strings.Contains(layout["wait"], "true") {
			modes = append(modes, authmodes.EntraMFAWait)
		}
		if slices.Contains(supportedEntries, "chars") {
			modes = append(modes, authmodes.EntraMFACode)
		}
		return modes

	case "newpassword":
		if slices.Contains(supportedEntries, "chars_password") {
			return []string{authmodes.NewPassword}
		}
	}
	return nil
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

		log.Debug(ctx, "Sending Device Authorization Request to retrieve device code...")
		response, err := session.oauth2Config.DeviceAuth(ctx, authOpts...)
		if err != nil {
			return nil, fmt.Errorf("could not generate Device Authentication code layout: %v", err)
		}
		log.Debugf(ctx, "Retrieved device code. Device Authorization Response: %#v", response)
		session.deviceAuthResponse = response

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
			"label": "Enter your password",
			"entry": "chars_password",
		}

	case authmodes.EntraPassword:
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Enter your Entra ID password",
			"entry": "chars_password",
		}

	case authmodes.EntraMFAWait:
		mfaWaitLabel := "Waiting for MFA approval..."
		if session.mfaChallengeInfo != nil && session.mfaChallengeInfo.Message != "" {
			mfaWaitLabel = session.mfaChallengeInfo.Message
		}
		uiLayout = map[string]string{
			"type":  "form",
			"label": mfaWaitLabel,
			"wait":  "true",
		}

	case authmodes.EntraMFACode:
		uiLayout = map[string]string{
			"type":  "form",
			"entry": "chars",
			"label": "Enter your MFA code",
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
		if session.attemptsPerMode[session.selectedMode] >= maxAuthAttempts {
			access = AuthDeniedMaxTries
			if b.apiVersion < 2 {
				access = AuthDenied
			}
			iadResponse = errorMessage{Message: "Maximum number of authentication attempts reached"}
			// Free any in-progress MFA flow immediately rather than waiting for
			// EndSession — consistent with all other terminal paths.
			session.entraPasswordHash = ""
			clearEntraMFAState(&session)
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
	case authmodes.EntraPassword:
		return b.entraPasswordAuth(ctx, session, secret)
	case authmodes.EntraMFAWait:
		return b.entraMFAWaitAuth(ctx, session)
	case authmodes.EntraMFACode:
		return b.entraMFACodeAuth(ctx, session, secret)
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

	authInfo, access, data := b.populateAuthInfo(ctx, session, t, rawIDToken, nil)
	if authInfo == nil {
		return access, data
	}

	// Load existing device registration data if there is any, to avoid re-registering the device.
	var deviceRegistrationData []byte
	if oldAuthInfo, err := token.LoadAuthInfo(session.tokenPath); err == nil {
		deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}
	cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, t, deviceRegistrationData)
	defer cleanup()
	if access != "" {
		return access, data
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

	// Refresh the token on every online login (even if it has not expired) to
	// re-verify the account with the provider. This refresh is also the live
	// disabled/revoked-user check. Entra password + MFA tokens are issued by the
	// Microsoft Broker App and are refreshed as a public client (no client_secret)
	// via the provider; all other tokens use the OIDC app refresh. Both paths feed
	// the same error classification below.
	if b.cfg.forceAccessCheckWithProvider || !session.isOffline {
		oldAuthInfo := authInfo
		// Both refresh paths use the cached refresh token; without one we can't
		// perform the liveness check, so require re-authentication.
		if authInfo.Token.RefreshToken == "" {
			log.Warningf(context.Background(), "No refresh token available for user %q", session.username)
			session.nextAuthModes = reauthModes
			return AuthNext, errorMessage{Message: "Remote authentication failed: No refresh token. Please contact your administrator."}
		}
		if authInfo.ObtainedViaEntraPasswordAuth {
			authInfo, err = b.refreshEntraPasswordToken(ctx, session, authInfo)
		} else {
			authInfo, err = b.refreshToken(ctx, session, authInfo)
		}
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) {
			if isAADSTSGrantRevokedError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token revoked for user %q after a remote password change/reset", session.username)
				b.invalidateCachedCredentials(session)
				session.nextAuthModes = reauthModes
				return AuthNext, errorMessage{Message: "Your password was changed remotely. Please re-authenticate."}
			}
			if b.provider.IsTokenExpiredError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token expired for user %q, re-authentication required", session.username)
				session.nextAuthModes = reauthModes
				return AuthNext, errorMessage{Message: "Refresh token expired, please authenticate again."}
			}
			if udc, ok := providers.ProviderAs[providers.UserDisabledChecker](b.provider); ok && udc.IsUserDisabledError(retrieveErr) {
				log.Error(context.Background(), retrieveErr.Error())
				log.Errorf(context.Background(), "Login denied: user %q is disabled in %s", session.username, b.provider.DisplayName())

				// Store the information that the user is disabled, so that we can deny login on subsequent offline attempts.
				oldAuthInfo.UserIsDisabled = true
				if err = token.CacheAuthInfo(session.tokenPath, oldAuthInfo); err != nil {
					log.Errorf(context.Background(), "Failed to store token: %s", err)
					return AuthDenied, unexpectedErrMsg("failed to store token")
				}

				return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
			}
		}
		if err != nil {
			log.Errorf(context.Background(), "Failed to refresh token: %s", err)

			// Fall back to offline mode for transient network failures (e.g. timeout, DNS,
			// connection refused). Unless provider authentication is forced.
			var netErr net.Error
			if errors.As(err, &netErr) && !b.cfg.forceAccessCheckWithProvider {
				log.Warningf(context.Background(), "Network error during token refresh for user %q, skipping token refresh", session.username)
				authInfo = oldAuthInfo
				session.isOffline = true
			} else {
				return AuthDenied, errorMessage{Message: "Failed to refresh token"}
			}
		}
	}

	// Check disabled status. We have to do this after trying to refresh the token,
	// because if token refresh fails with a network error, the session falls back
	// to offline mode.
	if authInfo.UserIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: user %q is disabled in %s and session is offline", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s. Please contact your administrator or try again with a working network connection.", b.provider.DisplayName())}
	}

	if authInfo.DeviceIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: device %q is disabled in %s and session is offline", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("This device is disabled in %s. Please contact your administrator or try again with a working network connection.", b.provider.DisplayName())}
	}

	// If device registration is enabled, ensure that the device is registered.
	// Skipped when offline: registration requires a live provider connection.
	if !session.isOffline {
		cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, authInfo.Token, authInfo.DeviceRegistrationData)
		defer cleanup()
		if access != "" {
			return access, data
		}
	}

	// Try to refresh the groups
	groups, err := b.getGroups(ctx, session, authInfo)
	if errors.Is(err, providerErrors.ErrDeviceDisabled) {
		// The device is disabled, deny login
		log.Errorf(context.Background(), "Login denied: device is disabled in %s for user %q", b.provider.DisplayName(), session.username)

		// Store the information that the device is disabled, so that we can deny login on subsequent offline attempts.
		authInfo.DeviceIsDisabled = true
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		return AuthDenied, errorMessage{Message: fmt.Sprintf("This device is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
	}
	if errors.Is(err, providerErrors.ErrInvalidRedirectURI) {
		// Deny login if the redirect URI is invalid, so that users and administrators are aware of the issue.
		log.Errorf(context.Background(), "Login denied: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Invalid redirect URI")
	}
	var retryWithDeviceAuthError *providerErrors.RetryWithDeviceAuthError
	if errors.As(err, &retryWithDeviceAuthError) {
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

		session.nextAuthModes = reauthModes
		msg := "Authentication failed due to a token issue. Please try again."
		return AuthNext, errorMessage{Message: msg}
	}
	if err != nil {
		// We couldn't fetch the groups, but we have valid cached ones. The live
		// provider check (and force_access_check_with_provider enforcement) happens
		// at the token refresh above, the same as the device-auth flow, so a
		// group-fetch failure here falls back to cached groups for both flows.
		log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
	} else {
		authInfo.UserInfo.Groups = groups
	}

	return b.finishAuth(session, authInfo)
}

func (b *Broker) entraPasswordAuth(ctx context.Context, session *session, userPassword string) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_password mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra password authentication")
	}

	// A prior MFA flow may still be active if the password step is restarted
	// (e.g. the user navigates back to re-enter the password). Release it before
	// starting a new one so the libhimmelblau continuation it owns is not leaked.
	clearEntraMFAState(session)

	// Load the cached auth info once at the start of the flow and stash it on the
	// session, so the second step (entra_mfa_wait/entra_mfa_code → finishEntraAuth)
	// reuses it instead of re-reading the token from disk on every call.
	//
	// A load error is non-fatal: it is expected on a first login (no cached token
	// yet), and for any other reason (e.g. an unreadable token) the flow can still
	// proceed by treating it as "no prior device data". A nil session.authInfo is
	// the correct state in both cases; log it for visibility.
	cachedAuthInfo, err := token.LoadAuthInfo(session.tokenPath)
	if err != nil {
		log.Debugf(context.Background(), "No cached auth info for user %q (first login or unreadable token): %v", session.username, err)
	}
	session.authInfo = cachedAuthInfo

	// Existing device registration data for the MFA flow (from the cached info).
	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	// Use device-scoped MFA flow when we expect to register the device or
	// already have valid device data for PRT-based token exchange. The ||
	// short-circuits so we skip parsing the data when registration is enabled.
	withDeviceScope := b.cfg.registerDevice || himmelblau.ValidDeviceRegistrationDataJSON(deviceRegistrationData)

	flow, challengeInfo, err := entraProvider.InitiateEntraPasswordAuth(ctx, b.cfg.clientID, b.cfg.issuerURL, session.username, userPassword, deviceRegistrationData, withDeviceScope)
	if err != nil {
		var mfaErr *himmelblau.MFAError
		if errors.As(err, &mfaErr) {
			return b.routeMFAInitError(mfaErr, session)
		}
		// A non-MFAError here is unexpected (the provider should classify expected
		// failures as MFAError); surface it as a reportable bug.
		log.Errorf(context.Background(), "Entra password authentication failed: %v", err)
		return AuthDenied, unexpectedErrMsg("failed to initiate Entra password flow")
	}
	if flow == nil || challengeInfo == nil {
		himmelblau.FreeMFAFlowState(flow)
		log.Error(context.Background(), "Entra password authentication did not return a complete MFA challenge")
		return AuthDenied, unexpectedErrMsg("provider returned incomplete MFA challenge")
	}

	session.mfaFlowActive = flow
	session.mfaChallengeInfo = challengeInfo

	// Hash the password immediately to narrow the plaintext memory window.
	// The hash is written to disk in finishEntraAuth after MFA succeeds.
	passwordHash, hashErr := password.HashPassword(userPassword)
	if hashErr != nil {
		log.Errorf(context.Background(), "Failed to hash password: %v", hashErr)
		clearEntraMFAState(session)
		return AuthDenied, unexpectedErrMsg("failed to process password")
	}
	session.entraPasswordHash = passwordHash

	// Determine MFA challenge type.
	mfaMethod := challengeInfo.Method
	pollingInterval := challengeInfo.PollingIntervalMs

	// FIDO/security-key MFA is not yet wired up in this terminal-based flow.
	// This is an implementation gap, not a fundamental limitation: libhimmelblau
	// can do FIDO (see https://github.com/himmelblau-idm/himmelblau/blob/main/src/common/src/auth.rs).
	// TODO: support FIDO MFA directly without redirecting to Device Authentication.
	if isFIDOMethod(mfaMethod) {
		log.Noticef(context.Background(), "FIDO MFA method %q detected for user %q; redirecting to Device Authentication", mfaMethod, session.username)
		session.entraPasswordHash = ""
		clearEntraMFAState(session)
		if b.cfg.flows.DeviceAuth {
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "This account requires FIDO/security key authentication. Please complete authentication using Device Authentication."}
		}
		return AuthDenied, errorMessage{Message: "This account requires FIDO/security key authentication, which is not yet supported in this mode. Device Authentication is also unavailable. Please contact your administrator."}
	}

	switch {
	case isPromptMethod(mfaMethod):
		// Code-entry MFA: user must type a code (OTP, SMS, etc.).
		session.nextAuthModes = []string{authmodes.EntraMFACode}
	case isPollMethod(mfaMethod):
		// Poll-based MFA: approval happens out of band (push notification or
		// phone call), so wait and poll. The poll loop applies a default
		// interval if the challenge does not carry a positive one.
		session.nextAuthModes = []string{authmodes.EntraMFAWait}
	case pollingInterval > 0:
		// Unknown method: a polling interval hints that approval happens out of band.
		log.Warningf(context.Background(), "Unknown MFA method %q with polling interval %dms, treating it as a poll-based method", mfaMethod, pollingInterval)
		session.nextAuthModes = []string{authmodes.EntraMFAWait}
	default:
		log.Warningf(context.Background(), "Unknown MFA method %q without a polling interval, treating it as a code-entry method", mfaMethod)
		session.nextAuthModes = []string{authmodes.EntraMFACode}
	}

	return AuthNext, nil
}

func clearEntraMFAState(session *session) {
	himmelblau.FreeMFAFlowState(session.mfaFlowActive)
	session.mfaFlowActive = nil
	session.mfaChallengeInfo = nil
}

// cachedDeviceRegistrationData returns the device registration data from the
// session's cached auth info (loaded once at the start of the flow by
// entraPasswordAuth), or nil if there is no cached token or it carries none.
func (b *Broker) cachedDeviceRegistrationData(session *session) []byte {
	if session.authInfo != nil {
		return session.authInfo.DeviceRegistrationData
	}
	return nil
}

func (b *Broker) entraMFAWaitAuth(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_mfa_wait mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra MFA")
	}

	if session.mfaFlowActive == nil {
		log.Error(context.Background(), "MFA wait mode selected but no active MFA flow")
		return AuthDenied, unexpectedErrMsg("no active MFA flow")
	}
	if session.mfaChallengeInfo == nil {
		log.Error(context.Background(), "MFA wait mode selected but no MFA challenge metadata is available")
		return AuthDenied, unexpectedErrMsg("no active MFA challenge")
	}

	maxAttempts := session.mfaChallengeInfo.MaxPollAttempts

	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	pollCtx, pollCancel := context.WithTimeout(ctx, maxMFAPollDuration)
	defer pollCancel()

	// The first poll attempt is 1 for Himmelblau's poll-based MFA flow.
	// maxAttempts <= 0 means "no usable attempt budget from the challenge": -1 is
	// libhimmelblau's "no max defined", and 0 can result from its
	// expires_in/polling_interval integer division when expires_in < polling_interval.
	// In both cases poll until the wall-clock cap above rather than skipping every
	// poll and reporting an immediate (false) timeout.
	for attempt := 1; maxAttempts <= 0 || attempt <= maxAttempts; attempt++ {
		oauthToken, err := entraProvider.AcquireTokenByMFAFlow(
			pollCtx, b.cfg.clientID, b.cfg.issuerURL, session.username,
			session.mfaFlowActive, "", attempt,
			deviceRegistrationData,
		)
		if err != nil {
			var mfaErr *himmelblau.MFAError
			if errors.As(err, &mfaErr) && mfaErr.IsMFAPollContinue() {
				// MFA not yet approved, keep polling.
				pollingInterval := session.mfaChallengeInfo.PollingIntervalMs
				if pollingInterval <= 0 {
					pollingInterval = 1000
				}
				select {
				case <-pollCtx.Done():
					return b.endExpiredMFAPoll(ctx, session)
				case <-time.After(time.Duration(pollingInterval) * time.Millisecond):
					continue
				}
			}
			// A user denial is terminal — handle it first, even if our poll
			// deadline happened to elapse during this (non-preemptible) call.
			if errors.As(err, &mfaErr) && mfaErr.IsMFADenied() {
				session.entraPasswordHash = ""
				clearEntraMFAState(session)
				log.Noticef(context.Background(), "MFA authentication denied for user %q", session.username)
				return AuthDenied, errorMessage{Message: "MFA authentication was denied."}
			}
			// AcquireTokenByMFAFlow is a non-preemptible CGo call: our poll
			// deadline (or the caller's cancellation) can elapse while it is in
			// flight, after which it returns a generic error rather than a poll
			// continuation. Report that as the timeout/cancellation it really is,
			// keeping the underlying error in the log for diagnosis.
			if pollCtx.Err() != nil {
				log.Errorf(context.Background(), "MFA poll error at deadline for user %q: %v", session.username, err)
				return b.endExpiredMFAPoll(ctx, session)
			}
			// Genuine MFA failure.
			session.entraPasswordHash = ""
			clearEntraMFAState(session)
			log.Errorf(context.Background(), "MFA poll failed: %v", err)
			// MFA flow state was cleared; direct the client back to entra_password
			// so it can restart the flow rather than re-entering a dead MFA mode.
			session.nextAuthModes = []string{authmodes.EntraPassword}
			return AuthNext, errorMessage{Message: "MFA authentication failed. Please try again."}
		}

		// MFA approved — finish auth.
		clearEntraMFAState(session)
		return b.finishEntraAuth(ctx, session, oauthToken)
	}

	// Max poll attempts exceeded.
	return b.endExpiredMFAPoll(ctx, session)
}

// endExpiredMFAPoll handles a poll-loop exit caused by the internal poll
// deadline elapsing, the caller cancelling the request, or the maximum number
// of poll attempts being exhausted. It clears the now-dead MFA state and directs
// the client back to entra_password so it can restart the flow, distinguishing a
// caller cancellation (AuthCancelled) from a wall-clock timeout (AuthNext).
func (b *Broker) endExpiredMFAPoll(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	session.entraPasswordHash = ""
	clearEntraMFAState(session)
	session.nextAuthModes = []string{authmodes.EntraPassword}
	if ctx.Err() != nil {
		// The whole IsAuthenticated request was cancelled by the caller.
		log.Noticef(context.Background(), "MFA poll cancelled for user %q", session.username)
		return AuthCancelled, nil
	}
	log.Noticef(context.Background(), "MFA poll timed out for user %q", session.username)
	return AuthNext, errorMessage{Message: "MFA approval timed out. Please try again."}
}

func (b *Broker) entraMFACodeAuth(ctx context.Context, session *session, code string) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_mfa_code mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra MFA")
	}

	if session.mfaFlowActive == nil {
		log.Error(context.Background(), "MFA code mode selected but no active MFA flow")
		return AuthDenied, unexpectedErrMsg("no active MFA flow")
	}

	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	oauthToken, err := entraProvider.AcquireTokenByMFAFlow(
		ctx, b.cfg.clientID, b.cfg.issuerURL, session.username,
		session.mfaFlowActive, code, 0,
		deviceRegistrationData,
	)
	if err != nil {
		var mfaErr *himmelblau.MFAError
		if errors.As(err, &mfaErr) && mfaErr.IsMFADenied() {
			log.Noticef(context.Background(), "MFA code verification denied for user %q", session.username)
			session.entraPasswordHash = ""
			clearEntraMFAState(session)
			return AuthDenied, errorMessage{Message: "MFA authentication was denied."}
		}
		if errors.As(err, &mfaErr) && mfaErr.IsMFARetryableCode() {
			// An incorrect or expired one-time code: re-prompt for the code
			// rather than discarding the flow and forcing password re-entry.
			// The MFA flow remains valid on this path (libhimmelblau only
			// advances flow.ctx/flow_token on success), so the next code
			// submission reuses it. AuthRetry stays on the entra_mfa_code mode
			// and is capped by maxAuthAttempts, so repeated wrong codes still
			// end in denial.
			log.Noticef(context.Background(), "Incorrect MFA code for user %q, re-prompting", session.username)
			return AuthRetry, errorMessage{Message: "Incorrect or expired code. Please try again."}
		}
		log.Noticef(context.Background(), "MFA code verification failed for user %q: %v", session.username, err)
		session.entraPasswordHash = ""
		clearEntraMFAState(session)
		// MFA flow state was cleared; direct the client back to entra_password
		// so it can restart the flow rather than re-entering the dead code mode.
		session.nextAuthModes = []string{authmodes.EntraPassword}
		return AuthNext, errorMessage{Message: "MFA authentication failed. Please try again."}
	}

	clearEntraMFAState(session)
	return b.finishEntraAuth(ctx, session, oauthToken)
}

func (b *Broker) finishEntraAuth(ctx context.Context, session *session, mfaToken *oauth2.Token) (string, isAuthenticatedDataResponse) {
	// Ensure any cached password hash is cleared from memory on all exit paths.
	defer func() { session.entraPasswordHash = "" }()

	// AcquireTokenByMFAFlow returns (nil, nil) only on a provider contract
	// violation, but this is the trust boundary into the generic broker: guard
	// against it so a misbehaving provider denies rather than panicking (and
	// taking down the broker process) on the t.Extra dereference below.
	if mfaToken == nil {
		log.Error(context.Background(), "Entra MFA flow completed without returning a token")
		return AuthDenied, unexpectedErrMsg("MFA flow returned no token")
	}

	// handleIsAuthenticated runs in a goroutine; on cancellation IsAuthenticated
	// returns AuthCancelled without awaiting it. If the (non-preemptible) MFA call
	// completed but the request was cancelled in the meantime, stop here rather
	// than registering a device and persisting a token + password file for an
	// authentication the client already abandoned.
	if ctx.Err() != nil {
		log.Noticef(context.Background(), "Entra MFA succeeded but the request was cancelled; not persisting credentials for user %q", session.username)
		return AuthCancelled, nil
	}

	t := mfaToken
	// Reuse the auth info loaded once at the start of the flow (entraPasswordAuth)
	// rather than re-reading the token from disk.
	oldAuthInfo := session.authInfo

	// The MFA flow never returns an id_token: the libhimmelblau binding only
	// surfaces preferred_username/sub/name (from the access token) as token
	// extras. Carry over a cached RawIDToken from a previous login so we never
	// persist an empty one.
	var rawIDToken string
	if oldAuthInfo != nil {
		rawIDToken = oldAuthInfo.RawIDToken
	}

	// The MFA token is issued for the Entra native API audience, so standard OIDC
	// ID token verification (getUserInfo) would fail. Extract user info from the
	// token extras instead — see userInfoFromTokenExtras for the trust model.
	userInfo, err := b.userInfoFromTokenExtras(ctx, session, t)
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}
	authInfo, access, data := b.populateAuthInfo(ctx, session, t, rawIDToken, &userInfo)
	if authInfo == nil {
		return access, data
	}

	// Mark this token as having been obtained via the entra_password MFA flow so
	// that returning logins refresh it through the Microsoft Broker App public
	// refresh path (the liveness/revocation check) rather than the OIDC app
	// refresh.
	authInfo.ObtainedViaEntraPasswordAuth = true

	// Carry over device registration data from a previous login when we are not
	// (re-)registering the device in this one. authInfo is built fresh from the
	// MFA token, so without this the subsequent finishAuth would persist an empty
	// value and silently discard a device that was registered earlier. For a
	// first-time login (no cached token) it keeps its zero value, which is correct.
	if oldAuthInfo != nil {
		authInfo.DeviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}

	var deviceRegistrationData []byte
	if oldAuthInfo != nil {
		deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}
	cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, t, deviceRegistrationData)
	defer cleanup()
	if access != "" {
		return access, data
	}

	// Fetch groups. The MFA flow just performed a live provider verification, so a
	// group-fetch failure here is not a liveness signal: fall back to cached groups
	// on a returning auth, and only deny first-time logins that have no cached groups.
	groups, err := b.getGroups(ctx, session, authInfo)
	if err != nil {
		if oldAuthInfo != nil {
			log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
			authInfo.UserInfo.Groups = oldAuthInfo.UserInfo.Groups
		} else {
			log.Errorf(context.Background(), "failed to get groups: %s", err)
			return AuthDenied, errorMessageForDisplay(err, "Failed to retrieve groups from Microsoft Graph API")
		}
	} else {
		authInfo.UserInfo.Groups = groups
	}

	access, data = b.finishAuth(session, authInfo)
	if access != AuthGranted {
		return access, data
	}

	// Store the pre-computed password hash for offline authentication. This runs
	// after finishAuth so that a denial there cannot leave a password file on
	// disk without a cached token (token-then-password matches the ordering of
	// the device-auth flow).
	if session.entraPasswordHash != "" {
		if hashErr := password.StoreHashedPassword(session.entraPasswordHash, session.passwordPath); hashErr != nil {
			log.Errorf(context.Background(), "Failed to store password hash: %v", hashErr)
			return AuthDenied, unexpectedErrMsg("failed to store password")
		}
		session.entraPasswordHash = ""
	}

	return access, data
}

// routeMFAInitError routes the AADSTS errors returned by InitiateEntraPasswordAuth
// (the MFA init step) to appropriate broker responses.
func (b *Broker) routeMFAInitError(mfaErr *himmelblau.MFAError, session *session) (string, isAuthenticatedDataResponse) {
	switch mfaErr.AADSTS {
	case 50053:
		log.Noticef(context.Background(), "Account locked for user %q (AADSTS50053)", session.username)
		return AuthDenied, errorMessage{Message: "Your account is locked. Please try again later or contact your administrator."}
	case 50055:
		log.Noticef(context.Background(), "Entra password expired for user %q", session.username)
		return AuthDenied, errorMessage{Message: "Your password has expired. Please change it via the Entra portal."}
	case 50057:
		log.Noticef(context.Background(), "Login denied: user %q is disabled in %s (AADSTS50057)", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
	case 50072, 50079:
		log.Noticef(context.Background(), "MFA enrollment required for user %q (AADSTS%d)", session.username, mfaErr.AADSTS)
		if b.cfg.flows.DeviceAuth {
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "MFA registration required. Please complete setup using Device Authentication."}
		}
		return AuthDenied, errorMessage{Message: "MFA registration required, but Device Authentication is disabled. Please contact your administrator."}
	case 50126:
		log.Noticef(context.Background(), "Invalid credentials for user %q", session.username)
		return AuthRetry, errorMessage{Message: "Incorrect password, please try again."}
	case 50173:
		log.Noticef(context.Background(), "Password changed remotely for user %q, invalidating cached credentials", session.username)
		b.invalidateCachedCredentials(session)
		session.nextAuthModes = reauthModes
		return AuthNext, errorMessage{Message: "Your password was changed remotely. Please re-authenticate."}
	case 53003:
		log.Noticef(context.Background(), "Conditional Access blocked sign-in for user %q (AADSTS53003)", session.username)
		return AuthDenied, errorMessage{Message: "Access was blocked by your organization's Conditional Access policies. Please contact your administrator."}
	default:
		if mfaErr.IsMFARequired() {
			// The native password MFA flow could not be set up; redirect to Device
			// Authentication which handles MFA via a separate flow.
			log.Noticef(context.Background(), "MFA required for user %q; redirecting to Device Authentication", session.username)
			if b.cfg.flows.DeviceAuth {
				session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
				return AuthNext, errorMessage{Message: "MFA is required. Please complete authentication using Device Authentication."}
			}
			return AuthDenied, errorMessage{Message: "MFA is required but Device Authentication is disabled. Please contact your administrator."}
		}
		log.Errorf(context.Background(), "Unhandled AADSTS error %d: %s", mfaErr.AADSTS, mfaErr.Message)
		return AuthDenied, unexpectedErrMsg(mfaErr.Error())
	}
}

func (b *Broker) invalidateCachedCredentials(session *session) {
	for _, path := range []string{session.passwordPath, session.tokenPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warningf(context.Background(), "Failed to remove cached credential %q: %v", path, err)
		}
	}
}

func isAADSTSGrantRevokedError(err *oauth2.RetrieveError) bool {
	if err == nil || err.ErrorCode != "invalid_grant" {
		return false
	}
	return strings.HasPrefix(err.ErrorDescription, "AADSTS50173:")
}

// isFIDOMethod returns true if the MFA method is a FIDO/security key method.
func isFIDOMethod(method string) bool {
	method = strings.ToLower(method)
	return strings.Contains(method, "fido") || strings.Contains(method, "webauthn") || strings.Contains(method, "security_key")
}

// isPromptMethod reports whether the MFA method requires the user to enter a
// code (TOTP, SMS OTP, access-pass, etc.) rather than approve a push
// notification or answer a phone call.
//
// Method identifiers and their UX are derived from libhimmelblau's
// auth.rs MFA branch (third_party/libhimmelblau/src/auth.rs around L3340-L3360):
//   - AccessPass, PhoneAppOTP, OneWaySMS, ConsolidatedTelephony → user types a code (prompt)
//   - PhoneAppNotification, CompanionAppsNotification → push approval (no prompt)
//   - TwoWayVoiceMobile, TwoWayVoiceAlternateMobile, TwoWayVoiceOffice → answer a phone call (no prompt)
//   - FidoKey → handled separately via isFIDOMethod
func isPromptMethod(method string) bool {
	switch method {
	case "AccessPass", "PhoneAppOTP", "OneWaySMS", "ConsolidatedTelephony":
		return true
	}
	return false
}

// isPollMethod reports whether the MFA method is approved out of band (push
// notification or phone call), in which case the broker polls for completion
// instead of prompting the user for a code. See isPromptMethod for where the
// method identifiers come from.
func isPollMethod(method string) bool {
	switch method {
	case "PhoneAppNotification", "CompanionAppsNotification",
		"TwoWayVoiceMobile", "TwoWayVoiceAlternateMobile", "TwoWayVoiceOffice":
		return true
	}
	return false
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

	// Append extra groups from config, avoiding duplicates.
	existingGroups := make(map[string]struct{}, len(authInfo.UserInfo.Groups)+len(b.cfg.extraGroups)+len(b.cfg.ownerExtraGroups))
	for _, g := range authInfo.UserInfo.Groups {
		existingGroups[g.Name] = struct{}{}
	}
	for _, name := range b.cfg.extraGroups {
		if _, exists := existingGroups[name]; !exists {
			log.Debugf(context.Background(), "Adding extra group %q", name)
			authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
			existingGroups[name] = struct{}{}
		}
	}
	if b.isOwner(authInfo.UserInfo.Name) {
		for _, name := range b.cfg.ownerExtraGroups {
			if _, exists := existingGroups[name]; !exists {
				log.Debugf(context.Background(), "Adding owner extra group %q", name)
				authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
				existingGroups[name] = struct{}{}
			}
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
	// When a poll is in flight, cancelling lets that goroutine free the MFA flow
	// as it unwinds; otherwise we free it here. These two paths can race (the
	// finishing goroutine may nil isAuthenticating via CancelIsAuthenticated just
	// as we read our own session copy), so both could call FreeMFAFlowState on the
	// same pointer. That is safe: FreeMFAFlowState takes MFAFlowState.mu and nils
	// its release callback, so the underlying C free runs exactly once and a
	// second call is a no-op. Sessions are stored by value, so there is no shared
	// write to mfaFlowActive itself (confirmed race-clean under `go test -race`).
	if session.isAuthenticating != nil {
		b.CancelIsAuthenticated(sessionID)
	} else {
		himmelblau.FreeMFAFlowState(session.mfaFlowActive)
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

// DeleteUser removes all broker side data stored for the given user
// from the broker's data directory.
func (b *Broker) DeleteUser(username string) error {
	userDataDir, err := b.userDataDir(username)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(userDataDir); err != nil {
		return fmt.Errorf("could not remove user data directory %q: %w", userDataDir, err)
	}
	log.Infof(context.Background(), "Deleted broker data for user %q at %q", username, userDataDir)
	return nil
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

// refreshEntraPasswordToken refreshes an Entra password + MFA token for the
// liveness/revocation check on a returning login. The provider performs a public
// refresh (no client_secret) as the Microsoft Broker App; on success the rotated
// refresh token replaces the cached one (kept fresh on each login, like the
// device-auth refresh). Errors are returned unwrapped so the caller classifies them
// with the same checks it uses for device-auth (IsUserDisabledError → AADSTS50057,
// IsTokenExpiredError → AADSTS50173, isAADSTSGrantRevokedError, net.Error → offline).
func (b *Broker) refreshEntraPasswordToken(ctx context.Context, _ *session, oldToken *token.AuthCachedInfo) (*token.AuthCachedInfo, error) {
	ep, ok := providers.ProviderAs[himmelblau.EntraPasswordProvider](b.provider)
	if !ok {
		// The token was obtained via the entra_password flow, so the provider that
		// issued it must implement EntraPasswordProvider. If it no longer does, the
		// deployment is misconfigured: fail the login rather than skipping the
		// liveness/revocation check, which would let a deleted/disabled user keep
		// logging in with the cached token.
		return nil, fmt.Errorf("provider does not implement EntraPasswordProvider; cannot refresh entra_password token for user %q", oldToken.UserInfo.Name)
	}
	newTok, err := ep.RefreshEntraPasswordToken(ctx, b.cfg.issuerURL, oldToken.Token.RefreshToken)
	if err != nil {
		return oldToken, err
	}
	// Rotate the refresh token.
	oldToken.Token.RefreshToken = newTok.RefreshToken

	// Refresh the cached user info from the verified refreshed access token's
	// claims, mirroring how refreshToken re-derives it from the ID token on the
	// device-auth path. Keep the cached gecos if the refreshed token omits one,
	// and keep groups (those are refreshed separately by getGroups).
	if err := ep.VerifyAccessToken(ctx, b.cfg.issuerURL, newTok.AccessToken); err != nil {
		return oldToken, fmt.Errorf("access token verification failed: %w", err)
	}
	userInfo, err := ep.UserInfoFromAccessToken(newTok.AccessToken)
	if err != nil {
		return oldToken, fmt.Errorf("could not refresh user info from the refreshed Entra token: %w", err)
	}
	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}
	if userInfo.Gecos == "" {
		userInfo.Gecos = oldToken.UserInfo.Gecos
	}
	userInfo.Groups = oldToken.UserInfo.Groups
	oldToken.UserInfo = userInfo

	return oldToken, nil
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

	// Update the raw ID token. Treat an absent, null, or empty id_token the same:
	// keep the cached one rather than storing an empty value.
	rawIDToken, _ := oauthToken.Extra("id_token").(string)
	if rawIDToken == "" {
		log.Debug(context.Background(), "refreshed token does not contain an ID token, keeping the old one")
		rawIDToken = oldToken.RawIDToken
	}

	var extraFields map[string]interface{}
	if mp, ok := providers.ProviderAs[providers.MetadataProvider](b.provider); ok {
		extraFields = mp.GetExtraFields(oauthToken)
	}
	t := token.NewAuthCachedInfo(oauthToken, rawIDToken, extraFields)
	t.ProviderMetadata = oldToken.ProviderMetadata
	t.DeviceRegistrationData = oldToken.DeviceRegistrationData

	t.UserInfo, err = b.getUserInfo(ctx, session, oauthToken, rawIDToken, true)
	if err != nil {
		return nil, err
	}
	if t.UserInfo.Gecos == "" {
		t.UserInfo.Gecos = oldToken.UserInfo.Gecos
	}

	t.UserInfo.Groups = oldToken.UserInfo.Groups

	return t, nil
}

// getUserInfo verifies and parses the raw ID token and returns the user info from it.
// If any provider specific mandatory claims are not present in the ID token, the missing claims are
// fetched from the `/userinfo` endpoint and merged before extracting user info.
// Note that verifying the ID token requires a working network connection to the provider's JWKs endpoint,
// so make sure to only call this function if the session is online.
func (b *Broker) getUserInfo(ctx context.Context, session *session, token *oauth2.Token, rawIDToken string, isRefresh bool) (info.User, error) {
	var (
		claims   info.Claimer
		userInfo info.User
		idToken  *oidc.IDToken
		err      error
	)

	if rawIDToken == "" {
		claims, err = session.oidcServer.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil {
			return info.User{}, fmt.Errorf("could not get user info from UserInfo endpoint: %w", err)
		}
	} else {
		var verifyErr error
		idToken, verifyErr = session.oidcServer.Verifier(&b.oidcCfg).Verify(ctx, rawIDToken)
		if verifyErr != nil {
			return info.User{}, fmt.Errorf("could not verify token: %w", verifyErr)
		}
		claims = idToken
	}

	userInfo, err = b.provider.GetUserInfo(claims, isRefresh)
	var missingClaimErr *providerErrors.MissingClaimError
	if rawIDToken != "" && errors.As(err, &missingClaimErr) {
		// The ID token is missing a required claim. Try fetching the claims from the UserInfo endpoint.
		log.Infof(context.Background(), "ID token is missing claim %q. Fetching claims from UserInfo endpoint.", missingClaimErr.Claim)
		var userInfoClaims info.Claimer
		userInfoClaims, err = session.oidcServer.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil {
			return info.User{}, fmt.Errorf("could not get user info from UserInfo endpoint: %w", err)
		}

		// OIDC Core §5.3.2: if the UserInfo response provides a sub claim, it MUST
		// equal the sub from the verified ID token. A mismatch means the /userinfo
		// response is attempting to substitute a different ProviderID and must be
		// rejected to prevent UID takeover.
		var subClaimCheck struct {
			Sub string `json:"sub"`
		}
		if err = userInfoClaims.Claims(&subClaimCheck); err != nil {
			return info.User{}, fmt.Errorf("could not decode UserInfo endpoint claims: %w", err)
		}
		if subClaimCheck.Sub != "" && subClaimCheck.Sub != idToken.Subject {
			return info.User{}, fmt.Errorf("userinfo sub %q does not match ID token sub %q: rejecting potential identity substitution", subClaimCheck.Sub, idToken.Subject)
		}

		// Merge ID token claims with UserInfo claims.
		// UserInfo claims override ID token claims for the same key.
		claims, err = info.NewMergedClaimer(claims, userInfoClaims)
		if err != nil {
			return info.User{}, fmt.Errorf("could not merge ID token and UserInfo endpoint claims: %w", err)
		}
		userInfo, err = b.provider.GetUserInfo(claims, isRefresh)
	}
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

// maybeRegisterDevice registers the device when the provider supports it and
// register_device is enabled, updating and persisting authInfo.DeviceRegistrationData.
// regToken is the token used to perform the registration; existingData is any
// previously stored device-registration data, passed to avoid re-registering.
//
// The returned cleanup must be deferred by the caller until AFTER group retrieval,
// because the Graph token exchange depends on the registration state that cleanup
// releases. cleanup is always non-nil (a no-op when nothing was registered), so the
// caller can defer it unconditionally. When access is non-empty the caller must
// return (access, data); an empty access means "proceed".
func (b *Broker) maybeRegisterDevice(ctx context.Context, session *session, authInfo *token.AuthCachedInfo, regToken *oauth2.Token, existingData []byte) (cleanup func(), access string, data isAuthenticatedDataResponse) {
	cleanup = func() {}

	dr, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider)
	if !ok || !b.cfg.registerDevice {
		return cleanup, "", nil
	}

	var err error
	authInfo.DeviceRegistrationData, cleanup, err = dr.MaybeRegisterDevice(ctx, regToken,
		session.username,
		b.cfg.issuerURL,
		existingData,
	)
	if err != nil {
		log.Errorf(context.Background(), "error registering device: %s", err)
		return func() {}, AuthDenied, errorMessage{Message: "Error registering device"}
	}

	// Store the auth info, so that the device registration data is not lost if the login fails after this point.
	if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
		log.Errorf(context.Background(), "Failed to store token: %s", err)
		return cleanup, AuthDenied, unexpectedErrMsg("failed to store token")
	}

	return cleanup, "", nil
}

func (b *Broker) getGroups(ctx context.Context, session *session, t *token.AuthCachedInfo) ([]info.Group, error) {
	if session.isOffline {
		return nil, errors.New("session is in offline mode")
	}

	gf, ok := providers.ProviderAs[providers.GroupFetcher](b.provider)
	if !ok {
		return nil, nil
	}
	// A cached token that carries device-registration data has a PRT that must be
	// exchanged for a Graph-scoped token (strategy 2). Derive this from the
	// presence of that data rather than tracking a separate persisted flag.
	return gf.GetGroups(ctx,
		b.cfg.clientID,
		b.cfg.issuerURL,
		t.Token,
		t.ProviderMetadata,
		t.DeviceRegistrationData,
		len(t.DeviceRegistrationData) > 0,
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
