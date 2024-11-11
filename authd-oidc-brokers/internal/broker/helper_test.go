package broker_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"github.com/ubuntu/authd-oidc-brokers/internal/token"
	"golang.org/x/oauth2"
)

// newBrokerForTests is a helper function to create a new broker for tests with the specified configuration.
//
// Note that the issuerURL is required in the configuration.
func newBrokerForTests(t *testing.T, cfg broker.Config, providerInfoer *testutils.MockProviderInfoer) (b *broker.Broker) {
	t.Helper()

	require.NotEmpty(t, cfg.IssuerURL(), "Setup: issuerURL must not be empty")

	if providerInfoer == nil {
		providerInfoer = &testutils.MockProviderInfoer{}
	}
	if providerInfoer.Groups == nil {
		providerInfoer.Groups = []info.Group{
			{Name: "new-broker-for-tests-remote-group", UGID: "12345"},
			{Name: "linux-new-broker-for-tests-local-group", UGID: ""},
		}
	}

	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.ClientID() == "" {
		cfg.SetClientID("test-client-id")
	}

	b, err := broker.New(
		cfg,
		broker.WithCustomProviderInfo(providerInfoer),
	)
	require.NoError(t, err, "Setup: New should not have returned an error")
	return b
}

// newSessionForTests is a helper function to easily create a new session for tests.
// If kept empty, username and mode will be assigned default values.
func newSessionForTests(t *testing.T, b *broker.Broker, username, mode string) (id, key string) {
	t.Helper()

	if username == "" {
		username = "test-user@email.com"
	}
	if mode == "" {
		mode = "auth"
	}

	id, key, err := b.NewSession(username, "some lang", mode)
	require.NoError(t, err, "Setup: NewSession should not have returned an error")

	return id, key
}

func encryptChallenge(t *testing.T, challenge, strKey string) string {
	t.Helper()

	if strKey == "" {
		return challenge
	}

	pubASN1, err := base64.StdEncoding.DecodeString(strKey)
	require.NoError(t, err, "Setup: base64 decoding should not have failed")

	pubKey, err := x509.ParsePKIXPublicKey(pubASN1)
	require.NoError(t, err, "Setup: parsing public key should not have failed")

	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	require.True(t, ok, "Setup: public key should be an RSA key")

	ciphertext, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, rsaPubKey, []byte(challenge), nil)
	require.NoError(t, err, "Setup: encryption should not have failed")

	// encrypt it to base64 and replace the challenge with it
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func updateAuthModes(t *testing.T, b *broker.Broker, sessionID, selectedMode string) {
	t.Helper()

	err := b.SetAvailableMode(sessionID, selectedMode)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	_, err = b.SelectAuthenticationMode(sessionID, selectedMode)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")
}

func generateAndStoreCachedInfo(t *testing.T, options tokenOptions, path string) {
	t.Helper()

	tok := generateCachedInfo(t, options)
	if tok == nil {
		writeTrashToken(t, path)
		return
	}
	err := token.CacheAuthInfo(path, *tok)
	require.NoError(t, err, "Setup: storing token should not have failed")
}

type tokenOptions struct {
	username string
	issuer   string

	expired        bool
	noRefreshToken bool
	noIDToken      bool
	invalid        bool
	invalidClaims  bool
	noUserInfo     bool
}

func generateCachedInfo(t *testing.T, options tokenOptions) *token.AuthCachedInfo {
	t.Helper()

	if options.invalid {
		return nil
	}

	if options.username == "" {
		options.username = "test-user@email.com"
	}
	if options.username == "-" {
		options.username = ""
	}

	idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":                options.issuer,
		"sub":                "saved-user-id",
		"aud":                "test-client-id",
		"exp":                9999999999,
		"name":               "test-user",
		"preferred_username": "test-user-preferred-username@email.com",
		"email":              options.username,
		"email_verified":     true,
	})
	encodedToken, err := idToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Setup: signing token should not have failed")

	tok := token.AuthCachedInfo{
		Token: &oauth2.Token{
			AccessToken:  "accesstoken",
			RefreshToken: "refreshtoken",
			Expiry:       time.Now().Add(1000 * time.Hour),
		},
	}

	if options.expired {
		tok.Token.Expiry = time.Now().Add(-1000 * time.Hour)
	}
	if options.noRefreshToken {
		tok.Token.RefreshToken = ""
	}

	if !options.noUserInfo {
		tok.UserInfo = info.User{
			Name:  options.username,
			UUID:  "saved-user-id",
			Home:  "/home/" + options.username,
			Gecos: options.username,
			Shell: "/usr/bin/bash",
			Groups: []info.Group{
				{Name: "saved-remote-group", UGID: "12345"},
				{Name: "saved-local-group", UGID: ""},
			},
		}
	}

	if options.invalidClaims {
		encodedToken = ".invalid."
		tok.UserInfo = info.User{}
	}

	if !options.noIDToken {
		tok.Token = tok.Token.WithExtra(map[string]string{"id_token": encodedToken})
		tok.RawIDToken = encodedToken
	}

	return &tok
}

func writeTrashToken(t *testing.T, path string) {
	t.Helper()

	content := []byte("This is a trash token that is not valid for authentication")

	// Create issuer specific cache directory if it doesn't exist.
	err := os.MkdirAll(filepath.Dir(path), 0700)
	require.NoError(t, err, "Setup: creating token directory should not have failed")

	err = os.WriteFile(path, content, 0600)
	require.NoError(t, err, "Setup: writing trash token should not have failed")
}
