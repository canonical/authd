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
	"github.com/ubuntu/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
	"github.com/ubuntu/authd-oidc-brokers/internal/token"
	"golang.org/x/oauth2"
)

type brokerForTestConfig struct {
	broker.Config
	issuerURL                   string
	forceProviderAuthentication bool
	registerDevice              bool
	allowedUsers                map[string]struct{}
	allUsersAllowed             bool
	ownerAllowed                bool
	firstUserBecomesOwner       bool
	owner                       string
	extraGroups                 []string
	ownerExtraGroups            []string
	homeBaseDir                 string
	allowedSSHSuffixes          []string
	provider                    providers.Provider

	getGroupsFails             bool
	supportsDeviceRegistration bool
	firstCallDelay             int
	secondCallDelay            int
	getGroupsFunc              func() ([]info.Group, error)

	listenAddress       string
	tokenHandlerOptions *testutils.TokenHandlerOptions
	customHandlers      map[string]testutils.EndpointHandler
}

// newBrokerForTests is a helper function to easily create a new broker for tests.
func newBrokerForTests(t *testing.T, cfg *brokerForTestConfig) (b *broker.Broker) {
	t.Helper()

	cfg.Init()
	if cfg.issuerURL != "" {
		cfg.SetIssuerURL(cfg.issuerURL)
	}
	if cfg.forceProviderAuthentication {
		cfg.SetForceProviderAuthentication(cfg.forceProviderAuthentication)
	}
	if cfg.registerDevice {
		cfg.SetRegisterDevice(cfg.registerDevice)
	}
	if cfg.homeBaseDir != "" {
		cfg.SetHomeBaseDir(cfg.homeBaseDir)
	}
	if cfg.allowedSSHSuffixes != nil {
		cfg.SetAllowedSSHSuffixes(cfg.allowedSSHSuffixes)
	}
	if cfg.allowedUsers != nil {
		cfg.SetAllowedUsers(cfg.allowedUsers)
	}
	if cfg.owner != "" {
		cfg.SetOwner(cfg.owner)
	}
	if cfg.firstUserBecomesOwner != false {
		cfg.SetFirstUserBecomesOwner(cfg.firstUserBecomesOwner)
	}
	if cfg.allUsersAllowed != false {
		cfg.SetAllUsersAllowed(cfg.allUsersAllowed)
	}
	if cfg.ownerAllowed != false {
		cfg.SetOwnerAllowed(cfg.ownerAllowed)
	}
	if cfg.extraGroups != nil {
		cfg.SetExtraGroups(cfg.extraGroups)
	}
	if cfg.ownerExtraGroups != nil {
		cfg.SetOwnerExtraGroups(cfg.ownerExtraGroups)
	}

	provider := &testutils.MockProvider{
		GetGroupsFails:                     cfg.getGroupsFails,
		ProviderSupportsDeviceRegistration: cfg.supportsDeviceRegistration,
		FirstCallDelay:                     cfg.firstCallDelay,
		SecondCallDelay:                    cfg.secondCallDelay,
		GetGroupsFunc:                      cfg.getGroupsFunc,
	}

	if cfg.provider == nil {
		cfg.SetProvider(provider)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.ClientID() == "" {
		cfg.SetClientID("test-client-id")
	}

	if cfg.IssuerURL() == "" {
		var serverOpts []testutils.ProviderServerOption
		for endpoint, handler := range cfg.customHandlers {
			serverOpts = append(serverOpts, testutils.WithHandler(endpoint, handler))
		}
		issuerURL, cleanup := testutils.StartMockProviderServer(
			cfg.listenAddress,
			cfg.tokenHandlerOptions,
			serverOpts...,
		)
		t.Cleanup(cleanup)
		cfg.SetIssuerURL(issuerURL)
	}

	b, err := broker.New(cfg.Config, broker.WithCustomProvider(provider))
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
		mode = sessionmode.Login
	}

	id, key, err := b.NewSession(username, "some lang", mode)
	require.NoError(t, err, "Setup: NewSession should not have returned an error")

	return id, key
}

func encryptSecret(t *testing.T, secret, strKey string) string {
	t.Helper()

	if strKey == "" {
		return secret
	}

	pubASN1, err := base64.StdEncoding.DecodeString(strKey)
	require.NoError(t, err, "Setup: base64 decoding should not have failed")

	pubKey, err := x509.ParsePKIXPublicKey(pubASN1)
	require.NoError(t, err, "Setup: parsing public key should not have failed")

	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	require.True(t, ok, "Setup: public key should be an RSA key")

	ciphertext, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, rsaPubKey, []byte(secret), nil)
	require.NoError(t, err, "Setup: encryption should not have failed")

	// encrypt it to base64 and replace the secret with it
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
	err := token.CacheAuthInfo(path, tok)
	require.NoError(t, err, "Setup: storing token should not have failed")
}

type tokenOptions struct {
	username string
	issuer   string
	groups   []info.Group

	expired                   bool
	noRefreshToken            bool
	refreshTokenExpired       bool
	noIDToken                 bool
	invalid                   bool
	invalidClaims             bool
	noUserInfo                bool
	isForDeviceRegistration   bool
	noIsForDeviceRegistration bool
	deviceIsDisabled          bool
	userIsDisabled            bool
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
	encodedIDToken, err := idToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Setup: signing ID token should not have failed")

	tok := token.AuthCachedInfo{
		Token: &oauth2.Token{
			AccessToken:  "accesstoken",
			RefreshToken: "refreshtoken",
			Expiry:       time.Now().Add(1000 * time.Hour),
		},
		DeviceIsDisabled: options.deviceIsDisabled,
		UserIsDisabled:   options.userIsDisabled,
	}

	if options.expired {
		tok.Token.Expiry = time.Now().Add(-1000 * time.Hour)
	}
	if options.noRefreshToken {
		tok.Token.RefreshToken = ""
	}
	if options.refreshTokenExpired {
		tok.Token.RefreshToken = testutils.ExpiredRefreshToken
	}
	if !options.noIsForDeviceRegistration {
		tok.ExtraFields = map[string]any{testutils.IsForDeviceRegistrationClaim: options.isForDeviceRegistration}
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
		if options.groups != nil {
			tok.UserInfo.Groups = options.groups
		}
	}

	if options.invalidClaims {
		encodedIDToken = ".invalid."
		tok.UserInfo = info.User{}
	}

	if !options.noIDToken {
		tok.Token = tok.Token.WithExtra(map[string]string{"id_token": encodedIDToken})
		tok.RawIDToken = encodedIDToken
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
