package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// TokenPathForSession returns the path to the token file for the given session.
func (b *Broker) TokenPathForSession(sessionID string) string {
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()

	session, ok := b.currentSessions[sessionID]
	if !ok {
		return ""
	}

	return session.cachePath
}

// CachePath returns the path to the cache directory for tests.
func (b *Broker) CachePath() string {
	return b.auth.cachePath
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
func (b *Broker) CacheAuthInfo(sessionID string, token *authCachedInfo, password string) error {
	s, err := b.getSession(sessionID)
	if err != nil {
		return err
	}

	if token == nil {
		return writeTrashToken(s.cachePath, password)
	}

	return b.cacheAuthInfo(&s, *token, password)
}

func writeTrashToken(path, challenge string) error {
	content, err := encrypt([]byte("This is a trash token that is not valid for authentication"), []byte(challenge))
	if err != nil {
		return err
	}
	// Create issuer specific cache directory if it doesn't exist.
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create token directory: %v", err)
	}

	return os.WriteFile(path, content, 0600)
}

// FetchUserInfo exposes the broker's fetchUserInfo method for tests.
func (b *Broker) FetchUserInfo(sessionID string, cachedInfo *authCachedInfo) (string, error) {
	s, err := b.getSession(sessionID)
	if err != nil {
		return "", err
	}

	uInfo, groups, err := b.fetchUserInfo(context.TODO(), &s, cachedInfo)
	if err != nil {
		return "", err
	}

	return b.userInfoFromClaims(uInfo, groups)
}
