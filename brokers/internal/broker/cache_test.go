package broker

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type cacheMetadataMarshaler func() ([]byte, error)

func (m cacheMetadataMarshaler) MarshalJSON() ([]byte, error) {
	return m()
}

func TestDeleteUserDuringTokenReplacement(t *testing.T) {
	t.Parallel()

	const (
		username   = "user@example.com"
		providerID = "tenant/subject"
	)
	b := &Broker{
		cfg:     Config{DataDir: t.TempDir()},
		cacheMu: &sync.Mutex{},
	}
	b.cfg.issuerURL = "https://issuer.example"
	dir, err := b.providerIDDataDir(providerID)
	require.NoError(t, err)
	tokenPath := filepath.Join(dir, "token.json")
	cached := &token.AuthCachedInfo{
		Token:    &oauth2.Token{AccessToken: "cached-token"},
		UserInfo: info.User{Name: username, ProviderID: providerID},
	}
	require.NoError(t, token.CacheAuthInfo(tokenPath, cached))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("cached-password"), 0600))

	started, resume := make(chan struct{}), make(chan struct{})
	cached.ProviderMetadata = map[string]interface{}{
		"blocking": cacheMetadataMarshaler(func() ([]byte, error) {
			// Pause the writer with an incomplete token on disk, as after truncation.
			err := os.WriteFile(tokenPath, []byte("{"), 0600)
			close(started)
			<-resume
			return []byte("null"), err
		}),
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- b.cacheAuthInfo(tokenPath, cached) }()
	<-started

	writerLocked := !b.cacheMu.TryLock()
	if !writerLocked {
		b.cacheMu.Unlock()
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- b.DeleteUser(username, providerID) }()
	close(resume)

	writeErr, deleteErr := <-writeDone, <-deleteDone
	require.True(t, writerLocked, "token replacement must hold the cache lock until the write completes")
	require.NoError(t, writeErr)
	require.NoError(t, deleteErr)
	require.NoDirExists(t, dir, "deletion must remove the completed token and password")
}
