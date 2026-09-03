package broker_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type brokerForTestConfig struct {
	broker.Config
	issuerURL                    string
	clientSecret                 string
	forceAccessCheckWithProvider bool
	registerDevice               bool
	deviceAuthFlowDisabled       bool
	entraAuthFlowDisabled        bool
	allowedUsers                 map[string]struct{}
	allUsersAllowed              bool
	ownerAllowed                 bool
	firstUserBecomesOwner        bool
	owner                        string
	extraGroups                  []string
	ownerExtraGroups             []string
	homeBaseDir                  string
	allowedSSHSuffixes           []string
	provider                     providers.Provider
	fidoAuthenticator            broker.FIDOAuthenticator
	apiVersion                   uint

	getGroupsFails                bool
	supportsDeviceRegistration    bool
	supportsFetchingGroups        bool
	supportsMetadata              bool
	metadataGetErr                error
	supportsUserDisabledCheck     bool
	userDisabledErrorCode         string
	requireNameClaimOnInitialAuth bool
	firstCallDelay                int
	secondCallDelay               int
	getGroupsFunc                 func() ([]info.Group, error)

	listenAddress       string
	tokenHandlerOptions *testutils.TokenHandlerOptions
	customHandlers      map[string]testutils.EndpointHandler
}

func brokerProviderWithOptionalCapabilities(provider *testutils.MockProvider, cfg *brokerForTestConfig) providers.Provider {
	capabilities := testutils.ProviderCapabilities{}
	if cfg.supportsFetchingGroups {
		capabilities.GroupFetcher = provider
	}
	if cfg.supportsDeviceRegistration {
		capabilities.DeviceRegisterer = &testutils.MockDeviceRegistererProvider{MockProvider: provider}
	}
	if cfg.supportsMetadata {
		capabilities.MetadataProvider = &testutils.MockMetadataProvider{MockProvider: provider, GetMetadataErr: cfg.metadataGetErr}
	}
	if cfg.supportsUserDisabledCheck {
		capabilities.UserDisabledChecker = &testutils.MockUserDisabledCheckerProvider{
			MockProvider:          provider,
			UserDisabledErrorCode: cfg.userDisabledErrorCode,
		}
	}

	return testutils.ComposeProvider(provider, capabilities)
}

// newBrokerForTests is a helper function to easily create a new broker for tests.
func newBrokerForTests(t *testing.T, cfg *brokerForTestConfig) (b *broker.Broker) {
	t.Helper()

	cfg.Init()
	if cfg.issuerURL != "" {
		cfg.SetIssuerURL(cfg.issuerURL)
	}
	if cfg.clientSecret != "" {
		cfg.SetClientSecret(cfg.clientSecret)
	}
	if cfg.forceAccessCheckWithProvider {
		cfg.SetforceAccessCheckWithProvider(cfg.forceAccessCheckWithProvider)
	}
	if cfg.registerDevice {
		cfg.SetRegisterDevice(cfg.registerDevice)
	}
	if cfg.deviceAuthFlowDisabled || cfg.entraAuthFlowDisabled {
		cfg.SetFlows(!cfg.deviceAuthFlowDisabled, !cfg.entraAuthFlowDisabled)
	} else {
		// Most broker tests exercise the authentication flows explicitly.
		cfg.SetFlows(true, true)
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

	provider := cfg.provider
	if provider == nil {
		mockProvider := &testutils.MockProvider{
			GetGroupsFails:                cfg.getGroupsFails,
			RequireNameClaimOnInitialAuth: cfg.requireNameClaimOnInitialAuth,
			FirstCallDelay:                cfg.firstCallDelay,
			SecondCallDelay:               cfg.secondCallDelay,
			GetGroupsFunc:                 cfg.getGroupsFunc,
		}
		provider = brokerProviderWithOptionalCapabilities(mockProvider, cfg)
	}
	cfg.SetProvider(provider)
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.ClientID() == "" {
		cfg.SetClientID("test-client-id")
	}
	if !cfg.entraAuthFlowDisabled && cfg.clientSecret == "" && !cfg.registerDevice {
		if _, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](provider); ok {
			// Most Entra auth broker tests are not exercising startup validation;
			// give them a minimal Graph group source so they keep building a valid
			// broker after New() started rejecting unusable entra_auth configs.
			cfg.SetClientSecret("test-client-secret")
		}
	}

	if cfg.IssuerURL() == "" {
		var serverOpts []testutils.ProviderServerOption
		for endpoint, handler := range cfg.customHandlers {
			serverOpts = append(serverOpts, testutils.WithHandler(endpoint, handler))
		}
		if cfg.tokenHandlerOptions != nil {
			// Clone before mutating: tests run in parallel and must not share
			// option maps. This aligns live-token identity with the cached
			// identity the broker tests use. A test that needs an empty
			// "sub" claim can opt out by setting claims["sub"] = "".
			opts := *cfg.tokenHandlerOptions
			opts.IDTokenClaims = make([]map[string]interface{}, len(cfg.tokenHandlerOptions.IDTokenClaims))
			for i, claims := range cfg.tokenHandlerOptions.IDTokenClaims {
				// maps.Clone returns nil for a nil map, which would panic on
				// the assignment below.
				cloned := maps.Clone(claims)
				if cloned == nil {
					cloned = map[string]interface{}{}
				}
				if _, exists := cloned["sub"]; !exists {
					cloned["sub"] = "saved-user-id"
				}
				opts.IDTokenClaims[i] = cloned
			}
			cfg.tokenHandlerOptions = &opts
		}
		issuerURL, cleanup := testutils.StartMockProviderServer(
			cfg.listenAddress,
			cfg.tokenHandlerOptions,
			serverOpts...,
		)
		t.Cleanup(cleanup)
		cfg.SetIssuerURL(issuerURL)
	}

	apiVersion := broker.LatestAPIVersion
	if cfg.apiVersion != 0 {
		apiVersion = cfg.apiVersion
	}

	opts := []broker.Option{broker.WithCustomProvider(provider)}
	// Override the FIDO authenticator when a mock is provided: the real one
	// enumerates USB devices, which would make tests depend on the hardware
	// plugged into the machine running them. When no mock is provided, the
	// default stays nil (in non-withmsentraid builds), so the broker's
	// b.fido == nil guards keep the FIDO auth modes disabled.
	if cfg.fidoAuthenticator != nil {
		opts = append(opts, broker.WithCustomFIDOAuthenticator(cfg.fidoAuthenticator))
	}

	b, err := broker.New(cfg.Config, apiVersion, opts...)
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

	id, key, err := b.NewSession(username, "some lang", mode, "")
	require.NoError(t, err, "Setup: NewSession should not have returned an error")

	return id, key
}

// newDeviceAuthBrokerForTests returns a broker that enrolls devices and whose
// group lookup is provided by provider, plus a login session for it. The mock
// token endpoint gives the first two token acquisitions the broker app
// audience, which the device-auth flows need when they acquire a token again
// after registering a device.
func newDeviceAuthBrokerForTests(t *testing.T, provider *mockEntraAuthProvider) (b *broker.Broker, sessionID, key string) {
	t.Helper()

	b = newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		registerDevice:        true,
		tokenHandlerOptions: &testutils.TokenHandlerOptions{
			IDTokenClaims: []map[string]interface{}{
				{"aud": consts.MicrosoftBrokerAppID},
				{"aud": consts.MicrosoftBrokerAppID},
			},
		},
	})

	sessionID, key = newSessionForTests(t, b, "", "")
	return b, sessionID, key
}

// newEntraMFABrokerForTests returns a broker whose group lookup is provided by
// provider, plus a login session for it. withDeviceRegistration enables device
// registration for the flows that register a device during the login.
func newEntraMFABrokerForTests(t *testing.T, provider *mockEntraAuthProvider, withDeviceRegistration bool) (b *broker.Broker, sessionID, key string) {
	t.Helper()

	b = newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		registerDevice:        withDeviceRegistration,
	})

	sessionID, key = newSessionForTests(t, b, "", "")
	return b, sessionID, key
}

// newDeviceAuthProviderForTests returns a mock Entra provider whose group
// lookup fails with groupErr, or returns a single remote group when groupErr is
// nil.
func newDeviceAuthProviderForTests(groupErr error) *mockEntraAuthProvider {
	return &mockEntraAuthProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: groupsOrError(groupErr)},
	}
}

// newEntraMFAProviderForTests returns a mock Entra provider whose group lookup
// fails with groupErr and whose MFA flow answers the code step.
func newEntraMFAProviderForTests(t *testing.T, groupErr error) *mockEntraAuthProvider {
	t.Helper()

	provider := newDeviceAuthProviderForTests(groupErr)
	provider.flowState = &himmelblau.MFAFlowState{}
	provider.challengeInfo = &himmelblau.MFAChallengeInfo{
		Message:           "Please type in the code displayed on your authenticator app from your device:",
		Method:            "PhoneAppOTP",
		PollingIntervalMs: 5000,
		MaxPollAttempts:   10,
	}
	provider.mfaTokenResult = newMFATokenResult(generateCachedInfo(t, tokenOptions{
		username: "test-user@email.com",
		issuer:   defaultIssuerURL,
	}).Token)
	return provider
}

// disabledDeviceFlagCases drives the flows that keep a persisted
// disabled-device flag until a group lookup succeeds: a refresh or a
// group-fetch failure is not evidence that the device is valid again.
var disabledDeviceFlagCases = map[string]struct {
	groupsErr    error
	wantDisabled bool
}{
	"Group_lookup_failure_keeps_the_flag": {
		groupsErr:    errors.New("temporary group lookup failure"),
		wantDisabled: true,
	},
	"Successful_group_lookup_clears_the_flag": {
		wantDisabled: false,
	},
}

// groupsOrError returns a group lookup that fails with err, or returns a single
// remote group when err is nil.
func groupsOrError(err error) func() ([]info.Group, error) {
	return func() ([]info.Group, error) {
		if err != nil {
			return nil, err
		}
		return []info.Group{{Name: "remote-group"}}, nil
	}
}

// assertDisabledDeviceFlag asserts whether the persisted disabled-device flag
// survived the last login: only a successful group lookup is evidence that the
// device is valid again.
func assertDisabledDeviceFlag(t *testing.T, b *broker.Broker, sessionID string, wantDisabled bool) {
	t.Helper()

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	if wantDisabled {
		require.True(t, cached.DeviceIsDisabled,
			"a refresh or a group-fetch failure is not evidence the device is valid: the disabled flag must survive until a group lookup succeeds")
		return
	}
	require.False(t, cached.DeviceIsDisabled,
		"a successful group lookup proves the device is valid and must clear the disabled flag")
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

// requireAuthModes asserts the authentication modes offered to the client, in
// order. It also primes the session: GetAuthenticationModes replaces the
// session's valid-mode list, which a later SelectAuthenticationMode validates
// against.
func requireAuthModes(t *testing.T, b *broker.Broker, sessionID string, want ...string) {
	t.Helper()

	modes, err := b.GetAuthenticationModes(sessionID, supportedLayouts)
	require.NoError(t, err, "GetAuthenticationModes should not have returned an error")
	got := make([]string, 0, len(modes))
	for _, mode := range modes {
		got = append(got, mode["id"])
	}
	require.Equal(t, want, got, "the client must be offered these modes, in this order")
}

// cancelWhileGroupLookupRuns starts an authentication whose group lookup blocks
// until the test releases it, cancels the request while the lookup runs, and
// asserts that the cancellation ends the request without waiting for the
// lookup. Callers then assert that the cancelled request left no side effect
// behind.
func cancelWhileGroupLookupRuns(t *testing.T, b *broker.Broker, sessionID, authData string, groupsStarted, releaseGroups, groupsDone chan struct{}) {
	t.Helper()

	authDone := make(chan struct{})
	var access, data string
	var authErr error
	go func() {
		access, data, authErr = b.IsAuthenticated(sessionID, authData)
		close(authDone)
	}()

	select {
	case <-groupsStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("group lookup did not start")
	}

	b.CancelIsAuthenticated(sessionID)
	select {
	case <-authDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled authentication did not return")
	}
	require.Equal(t, broker.AuthCancelled, access)
	require.Contains(t, data, "Authentication request cancelled")
	require.ErrorIs(t, authErr, context.Canceled)

	close(releaseGroups)
	select {
	case <-groupsDone:
	case <-time.After(time.Second):
		t.Fatal("group lookup did not finish")
	}
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
	username   string
	issuer     string
	gecos      string
	groups     []info.Group
	providerID string
	// registrationAge, when non-zero, seeds how long ago the device
	// registration data was obtained. That marks the data as older than the
	// replication window, so a confirmed failure discards it.
	registrationAge time.Duration

	expired                     bool
	noRefreshToken              bool
	refreshTokenExpired         bool
	refreshTokenInactiveExpired bool
	refreshTokenStale           bool
	noIDToken                   bool
	invalid                     bool
	invalidClaims               bool
	noUserInfo                  bool
	isForDeviceRegistration     bool
	deviceIsDisabled            bool
	userIsDisabled              bool
	obtainedViaEntraAuth        bool
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
	if options.providerID == "" {
		options.providerID = "saved-user-id"
	}

	idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":                options.issuer,
		"sub":                options.providerID,
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
		DeviceIsDisabled:     options.deviceIsDisabled,
		UserIsDisabled:       options.userIsDisabled,
		ObtainedViaEntraAuth: options.obtainedViaEntraAuth,
		// A seeded cache simulates a previously successful login, so its
		// groups count as resolved.
		GroupsResolved: true,
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
	if options.refreshTokenInactiveExpired {
		tok.Token.RefreshToken = testutils.InactiveExpiredRefreshToken
	}
	if options.refreshTokenStale {
		tok.Token.RefreshToken = testutils.StaleRefreshToken
	}
	if options.isForDeviceRegistration {
		tok.DeviceRegistrationData = []byte("device-registration-data")
	}
	if options.registrationAge > 0 {
		tok.DeviceRegistrationDataObtainedAt = time.Now().Add(-options.registrationAge).Unix()
	}

	if !options.noUserInfo {
		if options.gecos == "" {
			options.gecos = options.username
		}
		tok.UserInfo = info.User{
			Name:       options.username,
			ProviderID: options.providerID,
			Home:       "/home/" + options.username,
			Gecos:      options.gecos,
			Shell:      "/usr/bin/bash",
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
