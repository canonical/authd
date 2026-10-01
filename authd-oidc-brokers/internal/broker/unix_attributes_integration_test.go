package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type mockUnixAttributeProvider struct {
	*testutils.MockProvider
	uid             uint32
	gid             uint32
	enrichmentCalls int
	attributeConfig info.UnixAttributeConfig
}

func (p *mockUnixAttributeProvider) EnrichUserWithUnixAttributes(_ context.Context, user info.User, _ string, _ string, _ *oauth2.Token, _ map[string]interface{}, _ []byte, config info.UnixAttributeConfig) (info.User, error) {
	p.enrichmentCalls++
	p.attributeConfig = config
	uid := p.uid
	gid := p.gid
	user.UID = &uid
	user.Groups = []info.Group{{Name: "enriched-remote-group", UGID: "enriched-group-id", GID: &gid}}
	return user, nil
}

func requireCachedUnixAttributes(t *testing.T, b *broker.Broker, sessionID string, calls int, config info.UnixAttributeConfig) {
	t.Helper()

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.NotNil(t, cached.UserInfo.UID)
	require.Equal(t, uint32(1001), *cached.UserInfo.UID)
	require.Len(t, cached.UserInfo.Groups, 1)
	require.NotNil(t, cached.UserInfo.Groups[0].GID)
	require.Equal(t, uint32(2001), *cached.UserInfo.Groups[0].GID)
	require.NotNil(t, cached.UnixAttributesEnriched)
	require.True(t, *cached.UnixAttributesEnriched)
	require.Equal(t, 1, calls)
	require.Equal(t, unixAttributeConfig(), config)
}

func requireUnixAttributesHiddenFromResponse(t *testing.T, data string) {
	t.Helper()

	var payload struct {
		UserInfo info.User `json:"userinfo"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Nil(t, payload.UserInfo.UID)
	require.Len(t, payload.UserInfo.Groups, 1)
	require.Nil(t, payload.UserInfo.Groups[0].GID)
}

func unixAttributeConfig() info.UnixAttributeConfig {
	return info.UnixAttributeConfig{
		UIDAttribute: "extension_uidNumber",
		GIDAttribute: "extension_gidNumber",
	}
}

func TestDeviceAuthEnrichesAndCachesUnixAttributes(t *testing.T) {
	t.Parallel()

	provider := &mockUnixAttributeProvider{
		MockProvider: &testutils.MockProvider{},
		uid:          1001,
		gid:          2001,
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		unixUIDAttribute:      "extension_uidNumber",
		unixGIDAttribute:      "extension_gidNumber",
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.DeviceQr)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.NewPassword}, b.GetNextAuthModes(sessionID))

	updateAuthModes(t, b, sessionID, authmodes.NewPassword)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	requireUnixAttributesHiddenFromResponse(t, data)
	requireCachedUnixAttributes(t, b, sessionID, provider.enrichmentCalls, provider.attributeConfig)
}

func TestPasswordAuthEnrichesAndCachesUnixAttributes(t *testing.T) {
	t.Parallel()

	provider := &mockUnixAttributeProvider{
		MockProvider: &testutils.MockProvider{},
		uid:          1001,
		gid:          2001,
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		unixUIDAttribute:      "extension_uidNumber",
		unixGIDAttribute:      "extension_gidNumber",
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	requireUnixAttributesHiddenFromResponse(t, data)
	requireCachedUnixAttributes(t, b, sessionID, provider.enrichmentCalls, provider.attributeConfig)
}

func TestEntraAuthEnrichesAndCachesUnixAttributes(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraAuthProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		unixUID:        1001,
		unixGID:        2001,
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		unixUIDAttribute:      "extension_uidNumber",
		unixGIDAttribute:      "extension_gidNumber",
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraAuth)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	updateAuthModes(t, b, sessionID, authmodes.EntraMFAWait)
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	requireUnixAttributesHiddenFromResponse(t, data)
	requireCachedUnixAttributes(t, b, sessionID, provider.unixEnrichmentCalls, provider.unixAttributeConfig)
}

func TestUnixAttributeChangeDropsStaleIDs(t *testing.T) {
	t.Parallel()

	for _, flow := range []string{"offline", "fallback", "forced"} {
		for _, requireIDs := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/required=%t", flow, requireIDs), func(t *testing.T) {
				t.Parallel()
				const username = "test-user@email.com"
				mfaInfo := generateCachedInfo(t, tokenOptions{username: username})
				provider := &unixFallbackProvider{
					mockEntraAuthProvider: &mockEntraAuthProvider{
						MockProvider: &testutils.MockProvider{},
						flowState:    &himmelblau.MFAFlowState{},
						challengeInfo: &himmelblau.MFAChallengeInfo{
							Message: "Approve sign-in", Method: "PhoneAppNotification",
							PollingIntervalMs: 1, MaxPollAttempts: 1,
						},
						mfaTokenResult: newMFATokenResult(mfaInfo.Token),
						unixUID:        1001, unixGID: 2001,
					},
					enrichmentErr: errors.New("Graph request failed"),
				}
				b := newBrokerForTests(t, &brokerForTestConfig{
					Config:          broker.Config{DataDir: t.TempDir()},
					allUsersAllowed: true, provider: provider,
					unixUIDAttribute: "extension_uidNumber", unixGIDAttribute: "extension_gidNumber",
					unixUIDRequired: requireIDs, unixGIDRequired: requireIDs,
					forceAccessCheckWithProvider: flow == "forced",
					tokenHandlerOptions: &testutils.TokenHandlerOptions{
						IDTokenClaims: []map[string]interface{}{{"aud": "test-client-id"}, {"aud": "test-client-id"}},
					},
				})
				sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
				cached := generateCachedInfo(t, tokenOptions{username: username})
				uid, gid := uint32(1001), uint32(2001)
				cached.UserInfo.UID = &uid
				cached.UserInfo.Groups = []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}, {Name: "sudo"}}
				enrichmentComplete := true
				cached.UnixAttributesEnriched = &enrichmentComplete
				// The cached IDs were read from attributes that are no longer configured.
				cached.UnixAttributeNames = &token.UnixAttributeNames{UID: "extension_oldUidNumber", GID: "extension_oldGidNumber"}
				cached.Token.RefreshToken = "old-refresh-token"
				require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), cached))
				require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

				loginID, loginKey := sessionID, key
				if flow == "offline" {
					loginID, loginKey = newSessionForTests(t, b, username, sessionmode.Login)
					require.NoError(t, b.SetOffline(loginID))
					provider.beforeEnrichment = func() { t.Error("offline login must not request Graph data") }
				}
				updateAuthModes(t, b, loginID, authmodes.Password)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", loginKey))
				access, data, err := b.IsAuthenticated(loginID, authData)
				require.NoError(t, err)
				// A forced provider check never falls back to the cached snapshot.
				if requireIDs || flow == "forced" {
					require.Equal(t, broker.AuthDenied, access, data)
				} else {
					require.Equal(t, broker.AuthGranted, access, data)
				}

				stored, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
				require.NoError(t, err)
				require.Nil(t, stored.UserInfo.UID)
				require.Equal(t, []info.Group{{Name: "engineering", UGID: "group-id"}, {Name: "sudo"}}, stored.UserInfo.Groups)
			})
		}
	}
}

type unixFallbackProvider struct {
	*mockEntraAuthProvider
	enrichmentErr    error
	beforeEnrichment func()
}

func (p *unixFallbackProvider) EnrichUserWithUnixAttributes(ctx context.Context, user info.User, clientID, issuerURL string, graphToken *oauth2.Token, metadata map[string]interface{}, registration []byte, config info.UnixAttributeConfig) (info.User, error) {
	if p.beforeEnrichment != nil {
		p.beforeEnrichment()
	}
	if p.enrichmentErr != nil {
		return info.User{}, p.enrichmentErr
	}
	return p.mockEntraAuthProvider.EnrichUserWithUnixAttributes(ctx, user, clientID, issuerURL, graphToken, metadata, registration, config)
}

func TestUnixAttributeFallbackPreservesCredentialsAndSnapshot(t *testing.T) {
	t.Parallel()

	for _, flow := range []string{"device", "password", "entra", "entra-refresh"} {
		for _, registerDevice := range []bool{false, true} {
			for _, tc := range []struct {
				name                string
				missingUID          bool
				missingGID          bool
				forceCheck          bool
				requiredErr         error
				deviceAuthOnly      bool
				passwordRefreshOnly bool
				wantDenied          bool
				wantRetry           bool
			}{
				{name: "complete snapshot"},
				{name: "missing required UID", missingUID: true, wantDenied: true},
				{name: "missing required GID", missingGID: true, wantDenied: true},
				{name: "forced check", forceCheck: true, wantDenied: true},
				{name: "required UID removed", requiredErr: info.ErrUnixUIDRequired, wantDenied: true},
				{name: "required GID removed", requiredErr: info.ErrUnixGIDRequired, wantDenied: true},
				{name: "device disabled", requiredErr: providerErrors.ErrDeviceDisabled, deviceAuthOnly: true, wantDenied: true},
				{name: "invalid redirect URI", requiredErr: &providerErrors.ForDisplayError{Message: "invalid redirect URI", Err: providerErrors.ErrInvalidRedirectURI}, deviceAuthOnly: true, wantDenied: true},
				{name: "retry with device auth", requiredErr: &providerErrors.RetryWithDeviceAuthError{Err: errors.New("retry with device auth")}, deviceAuthOnly: true, wantRetry: true},
				{name: "device disabled after password refresh", requiredErr: providerErrors.ErrDeviceDisabled, passwordRefreshOnly: true, wantDenied: true},
				{name: "retry with device auth after password refresh", requiredErr: &providerErrors.RetryWithDeviceAuthError{Err: errors.New("retry with device auth")}, passwordRefreshOnly: true, wantRetry: true},
			} {
				if tc.deviceAuthOnly && (flow != "device" || registerDevice) {
					continue
				}
				if tc.passwordRefreshOnly && (flow != "password" || registerDevice) {
					continue
				}
				t.Run(fmt.Sprintf("%s/register=%t/%s", flow, registerDevice, tc.name), func(t *testing.T) {
					t.Parallel()
					const username = "test-user@email.com"
					mfaInfo := generateCachedInfo(t, tokenOptions{username: username})
					mfaInfo.Token.RefreshToken = "fresh-mfa-refresh-token"
					provider := &unixFallbackProvider{
						mockEntraAuthProvider: &mockEntraAuthProvider{
							MockProvider: &testutils.MockProvider{},
							flowState:    &himmelblau.MFAFlowState{},
							challengeInfo: &himmelblau.MFAChallengeInfo{
								Message: "Approve sign-in", Method: "PhoneAppNotification",
								PollingIntervalMs: 1, MaxPollAttempts: 1,
							},
							mfaTokenResult: newMFATokenResult(mfaInfo.Token),
							unixUID:        1001, unixGID: 2001,
						},
						enrichmentErr: errors.New("Graph request failed"),
					}
					if tc.requiredErr != nil {
						provider.enrichmentErr = tc.requiredErr
					}
					audience := "test-client-id"
					if registerDevice {
						audience = consts.MicrosoftBrokerAppID
					}
					b := newBrokerForTests(t, &brokerForTestConfig{
						Config:          broker.Config{DataDir: t.TempDir()},
						allUsersAllowed: true, provider: provider, registerDevice: registerDevice,
						forceAccessCheckWithProvider: tc.forceCheck,
						unixUIDAttribute:             "extension_uidNumber", unixGIDAttribute: "extension_gidNumber",
						unixUIDRequired: true, unixGIDRequired: true,
						tokenHandlerOptions: &testutils.TokenHandlerOptions{
							IDTokenClaims: []map[string]interface{}{{"aud": audience}, {"aud": audience}},
						},
					})
					sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
					cached := generateCachedInfo(t, tokenOptions{username: username, obtainedViaEntraAuth: flow == "entra-refresh"})
					uid, gid := uint32(1001), uint32(2001)
					cached.UserInfo.UID = &uid
					cached.UserInfo.Groups = []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}, {Name: "sudo"}}
					enrichmentComplete := true
					cached.UnixAttributesEnriched = &enrichmentComplete
					cached.UnixAttributeNames = &token.UnixAttributeNames{UID: "extension_uidNumber", GID: "extension_gidNumber"}
					cached.Token.RefreshToken = "old-refresh-token"
					if tc.missingUID {
						cached.UserInfo.UID = nil
					}
					if tc.missingGID {
						cached.UserInfo.Groups[0].GID = nil
					}
					require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), cached))
					require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))
					wantRefreshToken := "refreshtoken"
					switch flow {
					case "entra":
						wantRefreshToken = "fresh-mfa-refresh-token"
					case "entra-refresh":
						wantRefreshToken = "mock-rotated-refresh-token"
					}
					checkCache := func() {
						stored, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
						require.NoError(t, err)
						require.Equal(t, cached.UserInfo, stored.UserInfo)
						require.Equal(t, cached.UnixAttributesEnriched, stored.UnixAttributesEnriched)
						require.Equal(t, wantRefreshToken, stored.Token.RefreshToken)
						require.Equal(t, flow == "entra" || flow == "entra-refresh", stored.ObtainedViaEntraAuth)
						if registerDevice {
							require.Equal(t, mockDeviceRegistrationData, stored.DeviceRegistrationData)
						}
					}
					provider.beforeEnrichment = checkCache
					authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
					var access, data string
					var err error
					switch flow {
					case "device":
						updateAuthModes(t, b, sessionID, authmodes.DeviceQr)
						access, data, err = b.IsAuthenticated(sessionID, "{}")
						if !tc.wantDenied && !tc.wantRetry {
							require.NoError(t, err)
							require.Equal(t, broker.AuthNext, access)
							updateAuthModes(t, b, sessionID, authmodes.NewPassword)
							access, data, err = b.IsAuthenticated(sessionID, authData)
						}
					case "entra":
						updateAuthModes(t, b, sessionID, authmodes.EntraAuth)
						access, _, err = b.IsAuthenticated(sessionID, authData)
						require.NoError(t, err)
						require.Equal(t, broker.AuthNext, access)
						updateAuthModes(t, b, sessionID, authmodes.EntraMFAWait)
						access, data, err = b.IsAuthenticated(sessionID, "{}")
					default:
						updateAuthModes(t, b, sessionID, authmodes.Password)
						access, data, err = b.IsAuthenticated(sessionID, authData)
					}
					require.NoError(t, err)
					if tc.wantRetry {
						require.Equal(t, broker.AuthNext, access, data)
						require.ElementsMatch(t, []string{authmodes.EntraAuth, authmodes.Device, authmodes.DeviceQr}, b.GetNextAuthModes(sessionID))
					} else if tc.wantDenied {
						require.Equal(t, broker.AuthDenied, access, data)
					} else {
						require.Equal(t, broker.AuthGranted, access, data)
					}
					checkCache()
					if tc.forceCheck || tc.deviceAuthOnly || tc.passwordRefreshOnly {
						return
					}
					offlineID, offlineKey := newSessionForTests(t, b, username, sessionmode.Login)
					require.NoError(t, b.SetOffline(offlineID))
					provider.beforeEnrichment = func() { t.Error("offline login must not request Graph data") }
					updateAuthModes(t, b, offlineID, authmodes.Password)
					offlineData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", offlineKey))
					access, data, err = b.IsAuthenticated(offlineID, offlineData)
					require.NoError(t, err)
					if tc.missingUID || tc.missingGID {
						require.Equal(t, broker.AuthDenied, access, data)
					} else {
						require.Equal(t, broker.AuthGranted, access, data)
					}
				})
			}
		}
	}
}
