package broker_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"golang.org/x/oauth2"
)

// newBrokerForTests is a helper function to create a new broker for tests with the specified configuration.
//
// Note that the issuerURL is required in the configuration.
func newBrokerForTests(t *testing.T, cfg broker.Config) (b *broker.Broker) {
	t.Helper()

	require.NotEmpty(t, cfg.IssuerURL(), "Setup: issuerURL must not be empty")

	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.ClientID() == "" {
		cfg.SetClientID("test-client-id")
	}

	b, err := broker.New(
		cfg,
		broker.WithCustomProviderInfo(&testutils.MockProviderInfoer{
			Groups: []info.Group{
				{Name: "remote-group", UGID: "12345"},
				{Name: "linux-local-group", UGID: ""},
			},
		}),
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

var testTokens = map[string]broker.AuthCachedInfo{
	"valid": {
		Token: &oauth2.Token{
			AccessToken:  "accesstoken",
			RefreshToken: "refreshtoken",
			Expiry:       time.Now().Add(1000 * time.Hour),
		},
	},
	"expired": {
		Token: &oauth2.Token{
			AccessToken:  "accesstoken",
			RefreshToken: "refreshtoken",
			Expiry:       time.Now().Add(-1000 * time.Hour),
		},
	},
	"no-refresh": {
		Token: &oauth2.Token{
			AccessToken: "accesstoken",
			Expiry:      time.Now().Add(-1000 * time.Hour),
		},
	},
}

func generateCachedInfo(t *testing.T, preexistentToken, issuer string) *broker.AuthCachedInfo {
	t.Helper()

	if preexistentToken == "invalid" {
		return nil
	}

	var username string
	switch preexistentToken {
	case "no-name":
		username = ""
	case "other-name":
		username = "other-user@email.com"
	default:
		username = "test-user@email.com"
	}

	// This is to handle delay cases where we need to control the authentication ordering.
	if strings.HasPrefix(preexistentToken, "user-timeout-") {
		username = preexistentToken
	}

	idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":                issuer,
		"sub":                "saved-user-id",
		"aud":                "test-client-id",
		"exp":                9999999999,
		"name":               "test-user",
		"preferred_username": "test-user-preferred-username@email.com",
		"email":              username,
		"email_verified":     true,
	})
	encodedToken, err := idToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Setup: signing token should not have failed")

	tok, ok := testTokens[preexistentToken]
	if !ok {
		tok = testTokens["valid"]
	}

	tok.UserInfo = info.User{
		Name:  username,
		UUID:  "saved-user-id",
		Home:  "/home/" + username,
		Gecos: username,
		Shell: "/usr/bin/bash",
		Groups: []info.Group{
			{Name: "saved-remote-group", UGID: "12345"},
			{Name: "saved-local-group", UGID: ""},
		},
	}

	// This is to force the broker to query the provider for the user info.
	if strings.HasPrefix(preexistentToken, "user-timeout-") {
		tok.UserInfo = info.User{}
	}

	if preexistentToken == "invalid-id" {
		encodedToken = ".invalid."
		tok.UserInfo = info.User{}
	}

	tok.Token = tok.Token.WithExtra(map[string]string{"id_token": encodedToken})
	tok.RawIDToken = encodedToken

	return &tok
}
