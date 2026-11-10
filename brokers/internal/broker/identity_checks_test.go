package broker_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/brokers/internal/broker"
	"github.com/canonical/authd/brokers/internal/broker/authmodes"
	"github.com/canonical/authd/brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/brokers/internal/password"
	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/brokers/internal/testutils"
	"github.com/canonical/authd/brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

var errUsernameRejected = errors.New("username rejected by the provider policy")

func rejectAllUsernames(string) error { return errUsernameRejected }

// entraRefreshProvider returns a provider whose refreshed access token carries userInfo.
func entraRefreshProvider(mock *testutils.MockProvider, userInfo info.User) *mockEntraAuthProvider {
	return &mockEntraAuthProvider{
		MockProvider:        mock,
		refreshResult:       &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
		accessTokenUserInfo: &userInfo,
	}
}

// refreshLogin starts a session and runs a returning Entra password login on it.
// The cached token keeps cachedProviderID, which may be empty.
func refreshLogin(t *testing.T, b *broker.Broker, username, providerID, unixUsername, cachedProviderID string) (sessionID, access, data string) {
	t.Helper()

	sessionID, key, err := b.NewSessionForUser(username, "en", sessionmode.Login, providerID, unixUsername)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.EndSession(sessionID) })

	cached := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL, obtainedViaEntraAuth: true})
	cached.UserInfo.ProviderID = cachedProviderID
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), cached))
	require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	access, data, err = b.IsAuthenticated(sessionID, fmt.Sprintf(`{"%s":"%s"}`,
		broker.AuthDataSecret, encryptSecret(t, "password", key)))
	require.NoError(t, err)
	return sessionID, access, data
}

func newRefreshBroker(t *testing.T, provider *mockEntraAuthProvider) *broker.Broker {
	t.Helper()
	return newBrokerForTests(t, &brokerForTestConfig{
		Config:          broker.Config{DataDir: t.TempDir()},
		allUsersAllowed: true,
		provider:        provider,
		issuerURL:       defaultIssuerURL,
	})
}

func requireRotatedTokenCached(t *testing.T, b *broker.Broker, sessionID string) {
	t.Helper()
	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "new-refresh-token", cached.Token.RefreshToken, "the rotated refresh token must stay cached")
}

func TestDeviceAuthFirstLoginDeniedByUsernameValidation(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		allUsersAllowed: true,
		provider:        &testutils.MockProvider{ValidateUsernameFunc: rejectAllUsernames},
	})
	sessionID, _ := newSessionForTests(t, b, "", sessionmode.Login)
	t.Cleanup(func() { require.NoError(t, b.EndSession(sessionID)) })
	updateAuthModes(t, b, sessionID, authmodes.Device)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a name rejected by ValidateUsername must be denied")
	require.NoFileExists(t, b.TokenPathForSession(sessionID))
}

func TestEntraRefreshDeniedByUsernameValidation(t *testing.T) {
	t.Parallel()

	provider := entraRefreshProvider(&testutils.MockProvider{ValidateUsernameFunc: rejectAllUsernames},
		info.User{Name: "test-user@email.com", ProviderID: "test-user-id"})
	b := newRefreshBroker(t, provider)

	sessionID, access, _ := refreshLogin(t, b, "test-user@email.com", "", "", "test-user-id")
	require.Equal(t, broker.AuthDenied, access, "a refreshed name rejected by ValidateUsername must be denied")
	requireRotatedTokenCached(t, b, sessionID)
}

func TestEntraMFAFirstLoginDeniedByUsernameValidation(t *testing.T) {
	t.Parallel()

	const username = "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraAuthProvider{
		MockProvider:        &testutils.MockProvider{ValidateUsernameFunc: rejectAllUsernames},
		flowState:           &himmelblau.MFAFlowState{},
		challengeInfo:       &himmelblau.MFAChallengeInfo{Message: "Approve", PollingIntervalMs: 1, MaxPollAttempts: 1},
		accessTokenUserInfo: &info.User{Name: username, ProviderID: "test-user-id"},
		mfaTokenResult:      newMFATokenResult(mfaAuthInfo.Token),
	}
	b := newRefreshBroker(t, provider)

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a name rejected by ValidateUsername must be denied")
	require.NoFileExists(t, b.TokenPathForSession(sessionID))
}

// The provider may change the case of a name; the session name is already normalized.
func TestEntraFirstLoginAcceptsCaseChangeWithoutBoundProviderID(t *testing.T) {
	t.Parallel()

	const username = "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraAuthProvider{
		MockProvider:        &testutils.MockProvider{},
		flowState:           &himmelblau.MFAFlowState{},
		challengeInfo:       &himmelblau.MFAChallengeInfo{Message: "Approve", PollingIntervalMs: 1, MaxPollAttempts: 1},
		accessTokenUserInfo: &info.User{Name: "Test-User@Email.com", ProviderID: "new-provider-id"},
		mfaTokenResult:      newMFATokenResult(mfaAuthInfo.Token),
	}
	b := newRefreshBroker(t, provider)

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "a case-only difference must be accepted, got %s", data)
}

func TestEntraRefreshDeniesRenameWithoutBoundProviderID(t *testing.T) {
	t.Parallel()

	provider := entraRefreshProvider(&testutils.MockProvider{},
		info.User{Name: "someone-else@email.com", ProviderID: "other-id"})
	b := newRefreshBroker(t, provider)

	sessionID, access, _ := refreshLogin(t, b, "test-user@email.com", "", "", "")
	require.Equal(t, broker.AuthDenied, access, "an unbound session must not accept another name")
	requireRotatedTokenCached(t, b, sessionID)
}

// A session that has no provider ID from authd is still bound by the ID in the cache symlink.
func TestEntraRefreshUsesProviderIDFromCacheSymlink(t *testing.T) {
	t.Parallel()

	const (
		username = "test-user@email.com"
		cachedID = "saved-user-id"
	)
	for name, tc := range map[string]struct {
		providerID string
		want       string
	}{
		"Same_ID_new_name_granted": {providerID: cachedID, want: broker.AuthGranted},
		"Other_ID_denied":          {providerID: "other-id", want: broker.AuthDenied},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := entraRefreshProvider(&testutils.MockProvider{}, info.User{Name: "renamed@email.com", ProviderID: tc.providerID})
			b := newRefreshBroker(t, provider)

			// Create the cache as an earlier login would have: ID directory plus username symlink.
			usernameDir, err := b.UserDataDir(username)
			require.NoError(t, err)
			idDir, err := b.UserDataDir(cachedID)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(idDir, 0700))
			require.NoError(t, token.CacheAuthInfo(filepath.Join(idDir, "token.json"),
				generateCachedInfo(t, tokenOptions{username: username, providerID: cachedID, obtainedViaEntraAuth: true})))
			require.NoError(t, os.Symlink(filepath.Base(idDir), usernameDir))

			// No provider ID from authd, but the existing Unix account name is known.
			_, access, _ := refreshLogin(t, b, username, "", username, cachedID)
			require.Equal(t, tc.want, access)
		})
	}
}

func TestEntraRefreshDeniesRecycledProviderName(t *testing.T) {
	t.Parallel()

	provider := entraRefreshProvider(&testutils.MockProvider{}, info.User{Name: "alice@old", ProviderID: "bob-id"})
	b := newRefreshBroker(t, provider)

	_, access, data := refreshLogin(t, b, "alice@old", "alice-id", "alice@old", "alice-id")
	require.Equal(t, broker.AuthDenied, access, "a new account with a recycled name must not take over the old one")
	require.Contains(t, data, "account identity changed")
}

func TestFinishAuthDeniesWhenOwnerRegistrationFails(t *testing.T) {
	t.Parallel()

	configFile := filepath.Join(t.TempDir(), "authd.conf")
	require.NoError(t, os.WriteFile(configFile, []byte(fmt.Sprintf(
		"[oidc]\nissuer = %s\nclient_id = test-client-id\n", defaultIssuerURL)), 0600))
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{ConfigFile: configFile, DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              entraRefreshProvider(&testutils.MockProvider{}, info.User{Name: "test-user@email.com", ProviderID: "test-user-id"}),
		issuerURL:             defaultIssuerURL,
	})

	// Once the broker is up, a file in place of the drop-in directory makes the registration fail.
	require.NoError(t, os.WriteFile(broker.GetDropInDir(configFile), nil, 0600))

	_, access, _ := refreshLogin(t, b, "test-user@email.com", "", "", "test-user-id")
	require.Equal(t, broker.AuthDenied, access, "a login must fail when the owner cannot be persisted")
	require.Empty(t, b.Owner())
}

func TestFinishAuthRegistersUnixUsernameAsOwner(t *testing.T) {
	t.Parallel()

	configFile := filepath.Join(t.TempDir(), "authd.conf")
	require.NoError(t, os.WriteFile(configFile, []byte(fmt.Sprintf(
		"[oidc]\nissuer = %s\nclient_id = test-client-id\n", defaultIssuerURL)), 0600))
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{ConfigFile: configFile, DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              entraRefreshProvider(&testutils.MockProvider{}, info.User{Name: "test-user@email.com", ProviderID: "test-user-id"}),
		issuerURL:             defaultIssuerURL,
	})

	_, access, _ := refreshLogin(t, b, "test-user@email.com", "test-user-id", "unix-user", "test-user-id")
	require.Equal(t, broker.AuthGranted, access)

	dropIn, err := os.ReadFile(filepath.Join(broker.GetDropInDir(configFile), "20-owner-autoregistration.conf"))
	require.NoError(t, err)
	require.Contains(t, string(dropIn), `owner = """unix-user"""`)
	require.Equal(t, "unix-user", b.Owner())
}
