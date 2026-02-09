package broker

import (
	"sync"
)

func (cfg *Config) Init() {
	cfg.ownerMutex = &sync.RWMutex{}
}

func (cfg *Config) SetClientID(clientID string) {
	cfg.clientID = clientID
}

func (cfg *Config) SetIssuerURL(issuerURL string) {
	cfg.issuerURL = issuerURL
}

func (cfg *Config) SetForceProviderAuthentication(value bool) {
	cfg.forceProviderAuthentication = value
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

// MaxRequestDuration exposes the broker's maxRequestDuration for tests.
const MaxRequestDuration = maxRequestDuration
