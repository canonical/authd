package broker

import (
	"sync"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
)

// IsFIDOMethod and IsPromptMethod expose the unexported MFA method classifiers for tests.
var (
	IsFIDOMethod   = isFIDOMethod
	IsPromptMethod = isPromptMethod
)

// SetFIDODeviceWaitTimeout overrides how long entraAuthFidoAuth waits for a
// security key before falling back to the device code flow, so tests need not
// wait the production timeout. It returns a func that restores the default.
func SetFIDODeviceWaitTimeout(d time.Duration) (restore func()) {
	prev := fidoDeviceWaitTimeout
	fidoDeviceWaitTimeout = d
	return func() { fidoDeviceWaitTimeout = prev }
}

func (cfg *Config) Init() {
	cfg.ownerMutex = &sync.RWMutex{}
	cfg.flows = defaultFlowsConfig()
}

func (cfg *Config) SetClientID(clientID string) {
	cfg.clientID = clientID
}

func (cfg *Config) SetClientSecret(clientSecret string) {
	cfg.clientSecret = clientSecret
}

func (cfg *Config) SetIssuerURL(issuerURL string) {
	cfg.issuerURL = issuerURL
}

func (cfg *Config) SetforceAccessCheckWithProvider(value bool) {
	cfg.forceAccessCheckWithProvider = value
}

func (cfg *Config) SetRegisterDevice(value bool) {
	cfg.registerDevice = value
}

func (cfg *Config) SetHomeBaseDir(homeBaseDir string) {
	cfg.homeBaseDir = homeBaseDir
}

func (cfg *Config) SetAllowedUsers(allowedUsers map[string]struct{}) {
	cfg.allowedUsers = allowedUsers
}

func (cfg *Config) SetOwner(owner string) {
	cfg.ownerMutex.Lock()
	defer cfg.ownerMutex.Unlock()

	cfg.owner = owner
}

func (cfg *Config) SetFirstUserBecomesOwner(firstUserBecomesOwner bool) {
	cfg.ownerMutex.Lock()
	defer cfg.ownerMutex.Unlock()

	cfg.firstUserBecomesOwner = firstUserBecomesOwner
}

func (cfg *Config) SetAllUsersAllowed(allUsersAllowed bool) {
	cfg.allUsersAllowed = allUsersAllowed
}

func (cfg *Config) SetOwnerAllowed(ownerAllowed bool) {
	cfg.ownerMutex.Lock()
	defer cfg.ownerMutex.Unlock()

	cfg.ownerAllowed = ownerAllowed
}

func (cfg *Config) SetExtraGroups(extraGroups []string) {
	cfg.extraGroups = extraGroups
}

func (cfg *Config) SetOwnerExtraGroups(ownerExtraGroups []string) {
	cfg.ownerExtraGroups = ownerExtraGroups
}

func (cfg *Config) SetAllowedSSHSuffixes(allowedSSHSuffixes []string) {
	cfg.allowedSSHSuffixes = allowedSSHSuffixes
}

func (cfg *Config) SetFlows(deviceAuth, entraAuth bool) {
	cfg.flows = defaultFlowsConfig()
	cfg.flows.DeviceAuth = deviceAuth
	cfg.flows.EntraAuth = entraAuth
}

func (cfg *Config) SetProvider(provider provider) {
	cfg.provider = provider
}

func (cfg *Config) ClientID() string {
	return cfg.clientID
}

func (cfg *Config) IssuerURL() string {
	return cfg.issuerURL
}

// TokenPathForSession returns the path to the token file for the given session.
func (b *Broker) TokenPathForSession(sessionID string) string {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return ""
	}

	return session.tokenPath
}

// PasswordFilepathForSession returns the path to the password file for the given session.
func (b *Broker) PasswordFilepathForSession(sessionID string) string {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return ""
	}

	return session.passwordPath
}

// UserDataDirForSession returns the path to the user data directory for the given session.
func (b *Broker) UserDataDirForSession(sessionID string) string {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return ""
	}

	return session.userDataDir
}

// DataDir returns the path to the data directory for tests.
func (b *Broker) DataDir() string {
	return b.cfg.DataDir
}

// UserDataDir exposes the broker's userDataDir method for tests.
func (b *Broker) UserDataDir(username string) (string, error) {
	return b.userDataDir(username)
}

// EnsuredCachePaths captures the cache-related fields of a session after running
// ensureProviderIDCacheDir, so that tests can assert on the resulting state.
type EnsuredCachePaths struct {
	ProviderID   string
	UserDataDir  string
	TokenPath    string
	PasswordPath string
}

// EnsureProviderIDCacheDir builds a session for username whose cache paths are
// rooted at currentDataDir, runs ensureProviderIDCacheDir for providerID and
// returns the resulting cache-related session fields.
func (b *Broker) EnsureProviderIDCacheDir(username, currentDataDir, providerID string) EnsuredCachePaths {
	s := &session{username: username}
	setCachePaths(s, currentDataDir)
	b.ensureProviderIDCacheDir(s, providerID)
	return EnsuredCachePaths{
		ProviderID:   s.providerID,
		UserDataDir:  s.userDataDir,
		TokenPath:    s.tokenPath,
		PasswordPath: s.passwordPath,
	}
}

// NormalizedIssuer exposes the broker's normalizedIssuer method for tests.
func (b *Broker) NormalizedIssuer(issuerURL string) string {
	return normalizedIssuer(issuerURL)
}

// GetNextAuthModes returns the next auth mode of the specified session.
func (b *Broker) GetNextAuthModes(sessionID string) []string {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return nil
	}
	return session.nextAuthModes
}

// SetNextAuthModes sets the next auth mode of the specified session.
func (b *Broker) SetNextAuthModes(sessionID string, authModes []string) {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return
	}

	session.nextAuthModes = authModes
	b.currentSessions[sessionID] = session
}

func (b *Broker) SetAvailableMode(sessionID, mode string) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	s.authModes = []string{mode}

	return b.updateSession(sessionID, s)
}

// IsOffline returns whether the given session is offline or an error if the session does not exist.
func (b *Broker) IsOffline(sessionID string) (bool, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return false, err
	}
	return session.isOffline, nil
}

func (b *Broker) SetAttemptsPerMode(sessionID, mode string, attempts int) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	if s.attemptsPerMode == nil {
		s.attemptsPerMode = make(map[string]int)
	}
	s.attemptsPerMode[mode] = attempts

	return b.updateSession(sessionID, s)
}

// MaxRequestDuration exposes the broker's maxRequestDuration for tests.
const MaxRequestDuration = maxRequestDuration

// MaxAuthAttempts exposes the broker's maxAuthAttempts for tests.
const MaxAuthAttempts = maxAuthAttempts

// CachedPasswordMessage exposes the broker's cachedPasswordMessage for tests.
const CachedPasswordMessage = cachedPasswordMessage

// SetSessionMFAFlowActive lets tests set mfaFlowActive on a session without
// going through entraAuth. The challenge info is left nil so that
// tests can exercise the "flow active but no challenge metadata" guard in
// entraAuthWaitAuth.
func (b *Broker) SetSessionMFAFlowActive(sessionID string, flow *himmelblau.MFAFlowState) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	s.mfaFlowActive = flow
	return b.updateSession(sessionID, s)
}
