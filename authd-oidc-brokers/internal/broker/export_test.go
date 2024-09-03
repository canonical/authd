package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
)

func (cfg *Config) SetClientID(clientID string) {
	cfg.clientID = clientID
}

func (cfg *Config) SetIssuerURL(issuerURL string) {
	cfg.issuerURL = issuerURL
}

func (cfg *Config) SetHomeBaseDir(homeBaseDir string) {
	cfg.homeBaseDir = homeBaseDir
}

func (cfg *Config) SetAllowedSSHSuffixes(allowedSSHSuffixes []string) {
	cfg.allowedSSHSuffixes = allowedSSHSuffixes
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

// UpdateSessionAuthStep updates the current auth step for the given session.
func (b *Broker) UpdateSessionAuthStep(sessionID string, authStep int) {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return
	}

	session.currentAuthStep = authStep
	b.currentSessions[sessionID] = session
}

// SetAuthInfo sets the given key and value for the given session.AuthInfo.
func (b *Broker) SetAuthInfo(sessionID, key string, value any) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}

	s.authInfo[key] = value
	if err = b.updateSession(sessionID, s); err != nil {
		return err
	}

	return nil
}

func (b *Broker) SetAvailableMode(sessionID, mode string) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	s.authModes = []string{mode}

	return b.updateSession(sessionID, s)
}

type AuthCachedInfo = authCachedInfo

// CacheAuthInfo exposes the broker's cacheAuthInfo method for tests.
func (b *Broker) CacheAuthInfo(sessionID string, token *authCachedInfo) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}

	if token == nil {
		return writeTrashToken(s.tokenPath)
	}

	return b.cacheAuthInfo(&s, *token)
}

func writeTrashToken(path string) error {
	var err error
	content := []byte("This is a trash token that is not valid for authentication")

	// Create issuer specific cache directory if it doesn't exist.
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create token directory: %v", err)
	}

	return os.WriteFile(path, content, 0600)
}

// FetchUserInfo exposes the broker's fetchUserInfo method for tests.
func (b *Broker) FetchUserInfo(sessionID string, cachedInfo *authCachedInfo) (info.User, error) {
	s, err := b.getSession(sessionID)
	if err != nil {
		return info.User{}, err
	}

	uInfo, err := b.fetchUserInfo(context.TODO(), &s, cachedInfo)
	if err != nil {
		return info.User{}, err
	}

	return uInfo, err
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
