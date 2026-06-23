package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

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
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

var defaultIssuerURL string

func newTrackedMFAFlowState(release func()) *himmelblau.MFAFlowState {
	flow := &himmelblau.MFAFlowState{}
	releaseField := reflect.ValueOf(flow).Elem().FieldByName("release")
	//nolint:gosec // G103: unsafe pointer required to set unexported field for testing purposes only.
	reflect.NewAt(releaseField.Type(), unsafe.Pointer(releaseField.UnsafeAddr())).Elem().Set(reflect.ValueOf(release))
	return flow
}

type mockEntraPasswordProvider struct {
	*testutils.MockProvider
	flowState             *himmelblau.MFAFlowState
	challengeInfo         *himmelblau.MFAChallengeInfo
	mfaTokenResult        *oauth2.Token
	initErr               error
	recordedPollAttempts  []int
	recordedChallengeData []string
	refreshResult         *oauth2.Token // returned by RefreshEntraPasswordToken (defaults to a rotated token)
	refreshErr            error         // when set, RefreshEntraPasswordToken returns it (e.g. AADSTS50057)
	refreshDelay          time.Duration
	refreshCtxDeadline    time.Time
	verifyCtxDeadline     time.Time
	userDisabledErrorCode string     // when set, IsUserDisabledError matches an *oauth2.RetrieveError with this code
	verifyAccessTokenErr  error      // when set, VerifyAccessToken returns it (signature verification failure)
	accessTokenUserInfo   *info.User // when set, UserInfoFromAccessToken returns this user info
	userInfoFromTokenErr  error      // when set, UserInfoFromAccessToken returns this error
}

func (p *mockEntraPasswordProvider) VerifyAccessToken(ctx context.Context, _, _ string) error {
	if deadline, ok := ctx.Deadline(); ok {
		p.verifyCtxDeadline = deadline
	}
	return p.verifyAccessTokenErr
}

func (p *mockEntraPasswordProvider) UserInfoFromAccessToken(_ string) (info.User, error) {
	if p.userInfoFromTokenErr != nil {
		return info.User{}, p.userInfoFromTokenErr
	}
	if p.accessTokenUserInfo != nil {
		return *p.accessTokenUserInfo, nil
	}
	return info.NewUser("test-user@email.com", "", "saved-user-id", "", "test-user", nil), nil
}

type mockProviderWithEntraModes struct {
	*testutils.MockProvider
}

func (p *mockProviderWithEntraModes) SupportedOnlineAuthModes() []string {
	return []string{authmodes.Device, authmodes.DeviceQr, authmodes.EntraPassword}
}

type mockGrantRevokedProvider struct {
	*mockProviderWithEntraModes
}

func (p *mockGrantRevokedProvider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	return err != nil && err.ErrorCode == "invalid_grant" && strings.HasPrefix(err.ErrorDescription, "AADSTS50173:")
}

var mockDeviceRegistrationData = []byte(`{"device_id":"test-device-id","cert_key":"Y2VydA==","transport_key":"dHJhbnNwb3J0","auth_value":"test-auth-value","tpm_machine_key":"dHBtLW1hY2hpbmUta2V5"}`)

func (p *mockEntraPasswordProvider) InitiateEntraPasswordAuth(_ context.Context, _, _ string, _, _ string, _ []byte, _ bool) (*himmelblau.MFAFlowState, *himmelblau.MFAChallengeInfo, error) {
	if p.initErr != nil {
		return nil, nil, p.initErr
	}
	return p.flowState, p.challengeInfo, nil
}

func (p *mockEntraPasswordProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, authData string, pollAttempt int, _ []byte) (*oauth2.Token, error) {
	p.recordedPollAttempts = append(p.recordedPollAttempts, pollAttempt)
	p.recordedChallengeData = append(p.recordedChallengeData, authData)
	if p.mfaTokenResult == nil {
		return nil, fmt.Errorf("missing MFA token result")
	}
	return p.mfaTokenResult, nil
}

func (p *mockEntraPasswordProvider) RefreshEntraPasswordToken(ctx context.Context, _, _ string) (*oauth2.Token, error) {
	if deadline, ok := ctx.Deadline(); ok {
		p.refreshCtxDeadline = deadline
	}
	if p.refreshDelay > 0 {
		timer := time.NewTimer(p.refreshDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if p.refreshErr != nil {
		return nil, p.refreshErr
	}
	tok := p.refreshResult
	if tok == nil {
		// Default: an active user — a successful refresh that rotates the refresh token.
		tok = &oauth2.Token{AccessToken: "mock-access-token", RefreshToken: "mock-rotated-refresh-token"}
	}
	return tok, nil
}

// IsUserDisabledError lets the mock stand in as a providers.UserDisabledChecker so
// broker tests can exercise the refresh-rejection classification. It matches on a
// sentinel error code, mirroring testutils.MockUserDisabledCheckerProvider; the real
// AADSTS50057 detection is covered by the provider-level tests.
func (p *mockEntraPasswordProvider) IsUserDisabledError(err *oauth2.RetrieveError) bool {
	return p.userDisabledErrorCode != "" && err != nil && err.ErrorCode == p.userDisabledErrorCode
}

func (p *mockEntraPasswordProvider) IsTokenForDeviceRegistration(authInfo *token.AuthCachedInfo) bool {
	return authInfo != nil && len(authInfo.DeviceRegistrationData) > 0
}

func (p *mockEntraPasswordProvider) MaybeRegisterDevice(_ context.Context, _ *oauth2.Token, _ string, _ string, oldData []byte) ([]byte, func(), error) {
	if len(oldData) > 0 {
		return oldData, func() {}, nil
	}
	return mockDeviceRegistrationData, func() {}, nil
}

// mockMFADeniedProvider simulates MFA push notification being denied by the user.
type mockMFADeniedProvider struct {
	*mockEntraPasswordProvider
}

// mockDeviceRegistrationFailProvider simulates a first-time login where device
// registration fails at the network level (e.g. no connectivity to
// enterpriseregistration.windows.net).
type mockDeviceRegistrationFailProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockDeviceRegistrationFailProvider) MaybeRegisterDevice(_ context.Context, _ *oauth2.Token, _ string, _ string, oldData []byte) ([]byte, func(), error) {
	if len(oldData) > 0 {
		// Re-use existing registration — failure is only on first registration.
		return oldData, func() {}, nil
	}
	return nil, func() {}, fmt.Errorf("failed to enroll device: Request failed: error sending request for url (https://enterpriseregistration.windows.net/EnrollmentServer/device/?api-version=2.0)")
}

func (p *mockMFADeniedProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	// Simulate the user denying the push notification: ACQUIRE_TOKEN_FAILED without AADSTS.
	return nil, &himmelblau.MFAError{Category: himmelblau.MFAErrorDenied, Message: "MFA denied by user"}
}

// mockMFATimeoutProvider simulates MFA poll continuing until max attempts are exhausted.
type mockMFATimeoutProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockMFATimeoutProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	// Always return poll-continue so the loop exhausts max attempts.
	return nil, &himmelblau.MFAError{Category: himmelblau.MFAErrorPollContinue, Message: "MFA poll continue"}
}

// mockMFAWrongCodeThenSuccessProvider simulates an incorrect or expired
// one-time code on the first code submission followed by a correct code on the
// second. libhimmelblau reports a wrong code as an MFAInvalidCode error (which
// authd maps to MFAErrorRetryableCode via the C enum code), while leaving the
// flow intact. This is what production consumers see.
type mockMFAWrongCodeThenSuccessProvider struct {
	*mockEntraPasswordProvider
	codeAttempts int
}

func (p *mockMFAWrongCodeThenSuccessProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, authData string, _ int, _ []byte) (*oauth2.Token, error) {
	p.recordedChallengeData = append(p.recordedChallengeData, authData)
	p.codeAttempts++
	if p.codeAttempts == 1 {
		return nil, &himmelblau.MFAError{
			Category: himmelblau.MFAErrorRetryableCode,
			Message:  "AuthResponse indicates failure: Your sign-in was blocked by a One-Time Passcode mismatch.",
		}
	}
	return p.mfaTokenResult, nil
}

// mockMFANilTokenProvider violates the provider contract by returning (nil, nil)
// from AcquireTokenByMFAFlow, exercising the broker's defensive nil-token guard
// (a misbehaving provider must deny, not panic the broker).
type mockMFANilTokenProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockMFANilTokenProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, _ string, _ int, _ []byte) (*oauth2.Token, error) {
	return nil, nil
}

// mockMFAAlwaysWrongCodeProvider simulates every submitted one-time code being
// incorrect or expired (MFAErrorRetryableCode), while the MFA flow itself stays
// valid. This is used to exercise the maxAuthAttempts lockout path: repeated
// retryable wrong codes must eventually return AuthDeniedMaxTries and release
// the in-progress MFA flow.
type mockMFAAlwaysWrongCodeProvider struct {
	*mockEntraPasswordProvider
}

func (p *mockMFAAlwaysWrongCodeProvider) AcquireTokenByMFAFlow(_ context.Context, _, _ string, _ string, _ *himmelblau.MFAFlowState, authData string, _ int, _ []byte) (*oauth2.Token, error) {
	p.recordedChallengeData = append(p.recordedChallengeData, authData)
	return nil, &himmelblau.MFAError{
		Category: himmelblau.MFAErrorRetryableCode,
		Message:  "AuthResponse indicates failure: Your sign-in was blocked by a One-Time Passcode mismatch.",
	}
}

// newMFATokenResult builds an oauth2.Token with access-token-style extras like
// himmelblau.AcquireTokenByMFAFlow returns. Broker identity binding must come
// from UserInfoFromAccessToken after VerifyAccessToken, not from these extras.
func newMFATokenResult(t *oauth2.Token) *oauth2.Token {
	return t.WithExtra(map[string]any{
		"preferred_username": "test-user@email.com",
		"sub":                "saved-user-id",
		"name":               "test-user",
	})
}

func requireIssuerCacheTree(t *testing.T, issuerDir string, want map[string]string) {
	t.Helper()

	got := make(map[string]string)
	err := filepath.WalkDir(issuerDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == issuerDir {
			return nil
		}

		rel, err := filepath.Rel(issuerDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			targetPath := target
			if !filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(filepath.Dir(path), targetPath)
			}
			if targetRel, err := filepath.Rel(issuerDir, targetPath); err == nil && targetRel != "." && targetRel != ".." && !strings.HasPrefix(targetRel, ".."+string(os.PathSeparator)) {
				target = targetRel
			}
			got[rel] = "symlink -> " + filepath.ToSlash(target)
			return nil
		}

		if entry.IsDir() {
			got[rel] = "dir"
			return nil
		}
		got[rel] = "file"
		return nil
	})
	if errors.Is(err, os.ErrNotExist) && len(want) == 0 {
		return
	}
	require.NoError(t, err, "Walking issuer cache directory should not fail")
	require.Equal(t, want, got, "Issuer cache directory tree does not match")
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuer   string
		clientID string
		dataDir  string

		wantErr bool
	}{
		"Successfully_create_new_broker":                              {},
		"Successfully_create_new_even_if_can_not_connect_to_provider": {issuer: "https://notavailable"},

		"Error_if_issuer_is_not_provided":   {issuer: "-", wantErr: true},
		"Error_if_clientID_is_not_provided": {clientID: "-", wantErr: true},
		"Error_if_dataDir_is_not_provided":  {dataDir: "-", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			switch tc.issuer {
			case "":
				tc.issuer = defaultIssuerURL
			case "-":
				tc.issuer = ""
			}

			if tc.clientID == "-" {
				tc.clientID = ""
			} else {
				tc.clientID = "test-client-id"
			}

			if tc.dataDir == "-" {
				tc.dataDir = ""
			} else {
				tc.dataDir = t.TempDir()
			}

			bCfg := &broker.Config{DataDir: tc.dataDir}
			bCfg.SetIssuerURL(tc.issuer)
			bCfg.SetClientID(tc.clientID)
			b, err := broker.New(*bCfg, broker.LatestAPIVersion)
			if tc.wantErr {
				require.Error(t, err, "New should have returned an error")
				return
			}
			require.NoError(t, err, "New should not have returned an error")
			require.NotNil(t, b, "New should have returned a non-nil broker")
		})
	}
}

// TestNewRejectsUnusableEntraPasswordWithoutGroupSource verifies that New fails
// fast when entra_password is enabled but can't retrieve groups from Microsoft
// Graph (no device registration, no client secret) — rather than starting
// successfully and only failing once a user logs in.
func TestNewRejectsUnusableEntraPasswordWithoutGroupSource(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deviceCodeFlowEnabled bool
		registerDevice        bool
		clientSecret          string

		wantErr bool
	}{
		"Error_when_entra_password_is_the_only_flow_and_unusable": {wantErr: true},
		"Error_when_device_code_is_also_enabled_but_entra_password_is_still_unusable": {
			deviceCodeFlowEnabled: true,
			wantErr:               true,
		},
		"No_error_when_device_registration_makes_it_usable": {registerDevice: true},
		"No_error_when_a_client_secret_makes_it_usable":     {clientSecret: "test-client-secret"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bCfg := &broker.Config{DataDir: t.TempDir()}
			bCfg.Init()
			bCfg.SetIssuerURL(defaultIssuerURL)
			bCfg.SetClientID("test-client-id")
			bCfg.SetFlows(tc.deviceCodeFlowEnabled, true)
			bCfg.SetRegisterDevice(tc.registerDevice)
			bCfg.SetClientSecret(tc.clientSecret)

			provider := &mockEntraPasswordProvider{MockProvider: &testutils.MockProvider{}}
			b, err := broker.New(*bCfg, broker.LatestAPIVersion, broker.WithCustomProvider(provider))
			if tc.wantErr {
				require.Error(t, err, "New should have returned an error")
				return
			}
			require.NoError(t, err, "New should not have returned an error")
			require.NotNil(t, b, "New should have returned a non-nil broker")
		})
	}
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username                     string
		emptyUsername                bool
		issuerURL                    string
		customHandlers               map[string]testutils.EndpointHandler
		forceAccessCheckWithProvider bool

		wantOffline bool
		wantErr     bool
	}{
		"Successfully_create_new_session": {},
		"Creates_new_session_in_offline_mode_if_provider_is_not_available": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			wantOffline: true,
		},
		"Creates_new_session_in_offline_mode_if_provider_connection_times_out": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantOffline: true,
		},
		"Creates_new_session_with_schemeless_issuer_URL": {
			issuerURL:   "example.com",
			wantOffline: true,
		},

		"Error_when_provider_authentication_is_forced_and_provider_is_not_available": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			forceAccessCheckWithProvider: true,
			wantErr:                      true,
		},
		"Error_when_username_is_empty": {
			emptyUsername: true,
			wantErr:       true,
		},
		"Error_when_user_directory_path_could_not_be_derived": {
			username: "invalid/../user",
			wantErr:  true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				customHandlers:               tc.customHandlers,
				forceAccessCheckWithProvider: tc.forceAccessCheckWithProvider,
				issuerURL:                    tc.issuerURL,
			})

			username := tc.username
			if tc.emptyUsername {
				username = ""
			} else if username == "" {
				username = "test-user"
			}

			id, _, err := b.NewSession(username, "lang", sessionmode.Login, "")
			t.Logf("NewSession returned id: %q, err: %v", id, err)
			if tc.wantErr {
				require.Error(t, err, "NewSession should have returned an error")
				return
			}
			require.NoError(t, err, "NewSession should not have returned an error")

			gotOffline, err := b.IsOffline(id)
			require.NoError(t, err, "Session should have been created")

			require.Equal(t, tc.wantOffline, gotOffline, "Session should have been created in the expected mode")
		})
	}
}

// TestNewSessionRecoversFromDanglingCacheSymlink verifies that when the username cache path is a
// dangling compatibility symlink (its provider ID-keyed target was removed, e.g. by a partial
// DeleteUser), NewSession removes the broken link and falls back to a fresh username-based cache
// directory. Without this, the resolved cache path stays under the dangling symlink and every
// subsequent token/password write fails, denying the login with no way to recover.
func TestNewSessionRecoversFromDanglingCacheSymlink(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

	const username = "user@example.com"
	userDataDir, err := b.UserDataDir(username)
	require.NoError(t, err, "Setup: deriving the username data dir should not fail")
	require.NoError(t, os.MkdirAll(filepath.Dir(userDataDir), 0700), "Setup: create the issuer dir")

	// A compatibility symlink left behind by a prior migration, whose provider ID-keyed target no
	// longer exists.
	missingTarget, err := b.UserDataDir("provider-id-removed")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(missingTarget, userDataDir), "Setup: create dangling compat symlink")
	_, statErr := os.Stat(userDataDir)
	require.Error(t, statErr, "Setup: the symlink should be dangling")

	sessionID, _, err := b.NewSession(username, "lang", sessionmode.Login, "")
	require.NoError(t, err, "NewSession should not error on a dangling symlink")

	// The dangling symlink must have been removed so the path can be recreated as a real directory.
	_, lstatErr := os.Lstat(userDataDir)
	require.ErrorIs(t, lstatErr, os.ErrNotExist, "the dangling cache symlink should have been removed")

	// Caching the token must now succeed (it would have failed through the dangling link).
	tokenPath := b.TokenPathForSession(sessionID)
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700), "creating the fresh cache dir should succeed")
	require.NoError(t, os.WriteFile(tokenPath, []byte("{}"), 0600),
		"writing the token to the recovered cache path should succeed")
	requireIssuerCacheTree(t, filepath.Dir(userDataDir), map[string]string{
		"user@example.com":            "dir",
		"user@example.com/token.json": "file",
	})
}

func TestNewSessionWithProviderIDRepairsUsernameCompatibilityPath(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

	const (
		username   = "user@example.com"
		providerID = "provider-id-123"
	)
	usernameDir, err := b.UserDataDir(username)
	require.NoError(t, err, "Setup: deriving the username data dir should not fail")
	providerIDDir, err := b.UserDataDir(providerID)
	require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")

	require.NoError(t, os.MkdirAll(usernameDir, 0700), "Setup: creating stale username cache dir should not fail")
	require.NoError(t, os.WriteFile(filepath.Join(usernameDir, "token.json"), []byte("cached-token-marker"), 0600),
		"Setup: writing stale username token should not fail")
	require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating provider ID cache dir should not fail")
	require.NoError(t, os.WriteFile(filepath.Join(providerIDDir, "token.json"), []byte("cached-token-marker"), 0600),
		"Setup: writing provider ID token should not fail")

	sessionID, _, err := b.NewSession(username, "lang", sessionmode.Login, providerID)
	require.NoError(t, err, "NewSession should not error when repairing a stale username cache dir")
	require.Equal(t, providerIDDir, b.UserDataDirForSession(sessionID), "Session should use the provider ID cache dir")

	info, err := os.Lstat(usernameDir)
	require.NoError(t, err, "The username compatibility path should exist")
	require.NotZero(t, info.Mode()&os.ModeSymlink, "The username compatibility path should be a symlink")
	rawLink, err := os.Readlink(usernameDir)
	require.NoError(t, err, "os.Readlink on the compatibility symlink should not fail")
	require.False(t, filepath.IsAbs(rawLink), "compatibility symlink should be stored as a relative path, got %q", rawLink)
	target, err := filepath.EvalSymlinks(usernameDir)
	require.NoError(t, err, "The username compatibility symlink should resolve")
	require.Equal(t, providerIDDir, target, "The username compatibility symlink should target the provider ID cache dir")
	requireIssuerCacheTree(t, filepath.Dir(usernameDir), map[string]string{
		"provider-id-123":            "dir",
		"provider-id-123/token.json": "file",
		"user@example.com":           "symlink -> provider-id-123",
	})
}

// TestNewSessionRemovesUnsafeCacheSymlink verifies that when the username cache path is a
// compatibility symlink resolving to an unsafe target (outside the issuer cache directory or a
// non-directory), NewSession removes the link and falls back to a fresh username-based cache
// directory instead of writing through it.
func TestNewSessionRemovesUnsafeCacheSymlink(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		// targetOutsideIssuer points the symlink at a directory outside the issuer cache tree.
		targetOutsideIssuer bool
		// targetIsFile points the symlink at a regular file instead of a directory.
		targetIsFile bool
	}{
		"Target_points_outside_issuer_cache_directory": {targetOutsideIssuer: true},
		"Target_is_not_a_directory":                    {targetIsFile: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

			const username = "user@example.com"
			userDataDir, err := b.UserDataDir(username)
			require.NoError(t, err, "Setup: deriving the username data dir should not fail")
			require.NoError(t, os.MkdirAll(filepath.Dir(userDataDir), 0700), "Setup: create the issuer dir")

			var target string
			switch {
			case tc.targetOutsideIssuer:
				target = filepath.Join(t.TempDir(), "outside")
				require.NoError(t, os.MkdirAll(target, 0700), "Setup: create the out-of-tree target")
			case tc.targetIsFile:
				target, err = b.UserDataDir("provider-id-file")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(target, []byte("not a dir"), 0600), "Setup: create the file target")
			}
			require.NoError(t, os.Symlink(target, userDataDir), "Setup: create unsafe compat symlink")

			sessionID, _, err := b.NewSession(username, "lang", sessionmode.Login, "")
			require.NoError(t, err, "NewSession should not error on an unsafe symlink")

			_, lstatErr := os.Lstat(userDataDir)
			require.ErrorIs(t, lstatErr, os.ErrNotExist, "the unsafe cache symlink should have been removed")

			tokenPath := b.TokenPathForSession(sessionID)
			require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700), "creating the fresh cache dir should succeed")
			require.NoError(t, os.WriteFile(tokenPath, []byte("{}"), 0600),
				"writing the token to the recovered cache path should succeed")
		})
	}
}

var supportedUILayouts = map[string]map[string]string{
	"form": {
		"type":  "form",
		"entry": "chars_password",
	},
	"form-without-entry": {
		"type": "form",
	},

	"qrcode": {
		"type": "qrcode",
		"wait": "true",
	},
	"qrcode-without-wait": {
		"type": "qrcode",
	},
	"qrcode-without-qrcode": {
		"type":           "qrcode",
		"renders_qrcode": "false",
		"wait":           "true",
	},
	"qrcode-without-wait-and-qrcode": {
		"type":           "qrcode",
		"renders_qrcode": "false",
	},

	"newpassword": {
		"type":  "newpassword",
		"entry": "chars_password",
	},
	"newpassword-without-entry": {
		"type": "newpassword",
	},
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionMode      string
		sessionID        string
		supportedLayouts []string

		providerAddress                    string
		token                              *tokenOptions
		noPasswordFile                     bool
		nextAuthMode                       string
		unavailableProvider                bool
		deviceAuthUnsupported              bool
		registerDevice                     bool
		providerSupportsDeviceRegistration bool

		wantErr   bool
		wantModes []string
	}{
		// === Authentication session ===
		"Get_only_device_auth_qr_if_there_is_no_token": {
			token:     nil,
			wantModes: []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_token_exists": {
			token:     &tokenOptions{},
			wantModes: []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_token_is_invalid": {
			token:     &tokenOptions{invalid: true},
			wantModes: []string{authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_there_is_no_password_file": {
			token:          &tokenOptions{},
			noPasswordFile: true,
			wantModes:      []string{authmodes.DeviceQr},
		},

		// --- Next auth mode ---
		"Get_only_newpassword_if_next_auth_mode_is_newpassword": {
			nextAuthMode: authmodes.NewPassword,
			wantModes:    []string{authmodes.NewPassword},
		},
		"Get_only_device_auth_qr_if_next_auth_mode_is_device_qr": {
			nextAuthMode: authmodes.DeviceQr,
			wantModes:    []string{authmodes.DeviceQr},
		},

		// --- Device registration ---
		"Get_password_and_device_auth_qr_if_device_should_be_registered_and_token_is_for_device_registration": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_device_should_be_registered_and_token_is_not_for_device_registration": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_device_should_be_registered_and_token_is_not_for_device_registration_and_provider_does_not_support_it": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: false,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_only_device_auth_qr_if_device_should_not_be_registered_and_token_is_for_device_registration": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			wantModes:                          []string{authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_device_should_not_be_registered_and_token_is_not_for_device_registration": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		"Get_password_and_device_auth_qr_if_token_is_not_for_device_registration_but_provider_does_not_support_it": {
			registerDevice:                     false,
			providerSupportsDeviceRegistration: false,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			wantModes:                          []string{authmodes.Password, authmodes.DeviceQr},
		},
		// Note: We don't care about the weird case that the token is for device registration but the provider doesn't
		//       support it, because that never happens (providers which don't support device registration always return
		//       false for IsTokenForDeviceRegistration).

		"Get_only_password_if_device_should_be_registered_and_token_is_not_for_device_registration_but_provider_is_not_available": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: false},
			unavailableProvider:                true,
			// TODO: Automatically set providerAddress if unavailableProvider or deviceAuthUnsupported is set
			providerAddress: "127.0.0.1:31308",
			wantModes:       []string{authmodes.Password},
		},
		"Get_only_password_if_device_should_not_be_registered_and_token_is_for_device_registration_but_provider_is_not_available": {
			registerDevice:                     true,
			providerSupportsDeviceRegistration: true,
			token:                              &tokenOptions{isForDeviceRegistration: true},
			unavailableProvider:                true,
			providerAddress:                    "127.0.0.1:31309",
			wantModes:                          []string{authmodes.Password},
		},

		"Get_only_password_if_token_exists_and_provider_is_not_available": {
			token:               &tokenOptions{},
			providerAddress:     "127.0.0.1:31310",
			unavailableProvider: true,
			wantModes:           []string{authmodes.Password},
		},
		"Get_only_password_if_token_exists_and_provider_does_not_support_device_auth_qr": {
			token:                 &tokenOptions{},
			providerAddress:       "127.0.0.1:31311",
			deviceAuthUnsupported: true,
			wantModes:             []string{authmodes.Password},
		},

		// === Change password session ===
		"Get_only_password_if_token_exists_and_session_is_for_changing_password": {
			sessionMode: sessionmode.ChangePassword,
			token:       &tokenOptions{},
			wantModes:   []string{authmodes.Password},
		},
		"Get_only_newpassword_if_session_is_for changing_password_and_next_auth_mode_is_newpassword": {
			sessionMode:  sessionmode.ChangePassword,
			token:        &tokenOptions{},
			nextAuthMode: authmodes.NewPassword,
			wantModes:    []string{authmodes.NewPassword},
		},
		"Get_only_password_if_token_exists_and_session_mode_is_the_old_passwd_value": {
			sessionMode: sessionmode.ChangePasswordOld,
			token:       &tokenOptions{},
			wantModes:   []string{authmodes.Password},
		},

		// === Errors ===
		// --- General errors ---
		"Error_if_there_is_no_session": {
			sessionID: "-",
			wantErr:   true,
		},
		"Error_if_no_authentication_mode_is_supported": {
			providerAddress:       "127.0.0.1:31312",
			deviceAuthUnsupported: true,
			wantErr:               true,
		},
		"Error_if_expecting_device_auth_qr_but_not_supported": {
			supportedLayouts: []string{"qrcode-without-wait"},
			wantErr:          true,
		},
		"Error_if_expecting_device_auth_but_not_supported": {
			supportedLayouts: []string{"qrcode-without-wait-and-qrcode"},
			wantErr:          true,
		},
		"Error_if_expecting_newpassword_but_not_supported": {
			supportedLayouts: []string{"newpassword-without-entry"},
			wantErr:          true,
		},
		"Error_if_expecting_password_but_not_supported": {
			supportedLayouts: []string{"form-without-entry"},
			wantErr:          true,
		},
		"Error_if_next_auth_mode_is_invalid": {
			nextAuthMode: "invalid",
			wantErr:      true,
		},

		// --- Change password session errors ---
		"Error_if_session_is_for_changing_password_but_password_file_does_not_exist": {
			sessionMode:    sessionmode.ChangePassword,
			noPasswordFile: true,
			wantErr:        true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = sessionmode.Login
			}

			cfg := &brokerForTestConfig{
				registerDevice:             tc.registerDevice,
				supportsDeviceRegistration: tc.providerSupportsDeviceRegistration,
			}
			if tc.providerAddress == "" {
				// Use the default provider URL if no address is provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.listenAddress = tc.providerAddress

				const wellKnown = "/.well-known/openid-configuration"
				if tc.deviceAuthUnsupported {
					cfg.customHandlers = map[string]testutils.EndpointHandler{
						wellKnown: testutils.OpenIDHandlerWithNoDeviceEndpoint("http://" + tc.providerAddress),
					}
				}
				if tc.unavailableProvider {
					cfg.customHandlers = map[string]testutils.EndpointHandler{
						wellKnown: testutils.UnavailableHandler(),
					}
				}
			}
			b := newBrokerForTests(t, cfg)

			sessionID, _ := newSessionForTests(t, b, "", tc.sessionMode)
			if tc.sessionID == "-" {
				sessionID = ""
			}
			if tc.token != nil {
				generateAndStoreCachedInfo(t, *tc.token, b.TokenPathForSession(sessionID))
			}
			if !tc.noPasswordFile && sessionID != "" {
				err := password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}
			if tc.nextAuthMode != "" {
				b.SetNextAuthModes(sessionID, []string{tc.nextAuthMode})
			}

			if tc.supportedLayouts == nil {
				tc.supportedLayouts = []string{"form", "qrcode", "newpassword"}
			}
			var layouts []map[string]string
			for _, layout := range tc.supportedLayouts {
				layouts = append(layouts, supportedUILayouts[layout])
			}

			modes, err := b.GetAuthenticationModes(sessionID, layouts)
			if tc.wantErr {
				require.Error(t, err, "GetAuthenticationModes should have returned an error")
				return
			}
			require.NoError(t, err, "GetAuthenticationModes should not have returned an error")

			var modeIDs []string
			for _, mode := range modes {
				id, exists := mode["id"]
				require.True(t, exists, "Each mode should have an 'id' field. Mode: %v", mode)
				modeIDs = append(modeIDs, id)
			}
			require.Equal(t, tc.wantModes, modeIDs, "GetAuthenticationModes should have returned the expected modes")

			golden.CheckOrUpdateYAML(t, modes)
		})
	}
}

var supportedLayouts = []map[string]string{
	supportedUILayouts["form"],
	supportedUILayouts["qrcode"],
	supportedUILayouts["newpassword"],
}

var supportedLayoutsWithoutQrCode = []map[string]string{
	supportedUILayouts["form"],
	supportedUILayouts["qrcode-without-qrcode"],
	supportedUILayouts["newpassword"],
}

func TestSelectAuthenticationMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modeName string

		tokenExists      bool
		nextAuthMode     string
		passwdSession    bool
		customHandlers   map[string]testutils.EndpointHandler
		supportedLayouts []map[string]string

		wantErr bool
	}{
		"Successfully_select_password":       {modeName: authmodes.Password, tokenExists: true},
		"Successfully_select_device_auth_qr": {modeName: authmodes.DeviceQr},
		"Successfully_select_device_auth":    {supportedLayouts: supportedLayoutsWithoutQrCode, modeName: authmodes.Device},
		"Successfully_select_newpassword":    {modeName: authmodes.NewPassword, nextAuthMode: authmodes.NewPassword},

		"Selected_newpassword_shows_correct_label_in_passwd_session": {modeName: authmodes.NewPassword, passwdSession: true, tokenExists: true, nextAuthMode: authmodes.NewPassword},

		"Error_when_selecting_invalid_mode": {modeName: "invalid", wantErr: true},
		"Error_when_selecting_device_auth_qr_but_provider_is_unavailable": {modeName: authmodes.DeviceQr, wantErr: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.UnavailableHandler(),
			},
		},
		"Error_when_selecting_device_auth_but_provider_is_unavailable": {
			supportedLayouts: supportedLayoutsWithoutQrCode,
			modeName:         authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.UnavailableHandler(),
			},
			wantErr: true,
		},
		"Error_when_selecting_device_auth_qr_but_request_times_out": {modeName: authmodes.DeviceQr, wantErr: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_selecting_device_auth_but_request_times_out": {
			supportedLayouts: supportedLayoutsWithoutQrCode,
			modeName:         authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := &brokerForTestConfig{}
			if tc.customHandlers == nil {
				// Use the default provider URL if no custom handlers are provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.customHandlers = tc.customHandlers
			}
			b := newBrokerForTests(t, cfg)

			sessionType := sessionmode.Login
			if tc.passwdSession {
				sessionType = sessionmode.ChangePassword
			}
			sessionID, _ := newSessionForTests(t, b, "", sessionType)

			if tc.tokenExists {
				generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
				err := password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}
			if tc.nextAuthMode != "" {
				b.SetNextAuthModes(sessionID, []string{tc.nextAuthMode})
			}
			if tc.supportedLayouts == nil {
				tc.supportedLayouts = supportedLayouts
			}

			// We need to do a GAM call first to get all the modes.
			_, err := b.GetAuthenticationModes(sessionID, tc.supportedLayouts)
			require.NoError(t, err, "Setup: GetAuthenticationModes should not have returned an error")

			got, err := b.SelectAuthenticationMode(sessionID, tc.modeName)
			if tc.wantErr {
				require.Error(t, err, "SelectAuthenticationMode should have returned an error")
				return
			}
			require.NoError(t, err, "SelectAuthenticationMode should not have returned an error")

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

type isAuthenticatedResponse struct {
	Access string
	Data   string
	Err    string
}

func TestIsAuthenticated(t *testing.T) {
	t.Parallel()

	correctPassword := "password"

	tests := map[string]struct {
		sessionMode                        string
		sessionOffline                     bool
		username                           string
		forceAccessCheckWithProvider       bool
		userDoesNotBecomeOwner             bool
		allUsersAllowed                    bool
		extraGroups                        []string
		ownerExtraGroups                   []string
		providerSupportsDeviceRegistration bool
		registerDevice                     bool
		requireNameClaimOnInitialAuth      bool
		providerSupportsMetadata           bool
		metadataGetErr                     error
		providerSupportsUserDisabledCheck  bool
		userDisabledErrorCode              string
		providerHasNoGroupFetcher          bool

		firstMode                string
		firstSecret              string
		badFirstKey              bool
		getGroupsFails           bool
		useOldNameForSecretField bool
		groupsReturnedByProvider []info.Group
		getGroupsFunc            func() ([]info.Group, error)

		customHandlers      map[string]testutils.EndpointHandler
		address             string
		tokenHandlerOptions *testutils.TokenHandlerOptions

		wantSecondCall bool
		secondMode     string
		secondSecret   string

		token                *tokenOptions
		invalidAuthData      bool
		dontWaitForFirstCall bool
		readOnlyDataDir      bool
		wantGroups           []info.Group
		wantGecos            string
		wantNextAuthModes    []string
		wantOffline          bool
	}{
		"Successfully_authenticate_user_with_device_auth_and_newpassword": {firstSecret: "-", wantSecondCall: true},
		"Successfully_authenticate_user_with_password":                    {firstMode: authmodes.Password, token: &tokenOptions{}},
		"Successfully_authenticate_with_device_auth_when_provider_uses_thin_id_token": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				// Remove "must-have-claim" from the ID token.
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				// Provide "must-have-claim" via the userinfo endpoint.
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{
					"must-have-claim": "present",
				}),
			},
		},

		"Authenticating_with_qrcode_reacquires_token":          {firstSecret: "-", wantSecondCall: true, token: &tokenOptions{}},
		"Authenticating_with_password_refreshes_expired_token": {firstMode: authmodes.Password, token: &tokenOptions{expired: true}},
		"Authenticating_with_password_keeps_old_gecos_if_name_claim_missing_on_refresh_for_name_claim_provider": {
			firstMode:                     authmodes.Password,
			token:                         &tokenOptions{expired: true, gecos: "Saved Name"},
			requireNameClaimOnInitialAuth: true,
			wantGecos:                     "Saved Name",
			customHandlers:                map[string]testutils.EndpointHandler{},
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"name"},
			},
		},
		"Successfully_authenticate_with_name_claim_provider_when_name_is_only_in_userinfo": {
			firstSecret:                   "-",
			wantSecondCall:                true,
			requireNameClaimOnInitialAuth: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"name"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{
					"must-have-claim": "present",
					"name":            "Full Name from UserInfo",
				}),
			},
		},
		"Authenticating_with_password_still_allowed_if_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
		},
		"Authenticating_with_password_still_allowed_if_token_is_expired_and_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
		},
		"Authenticating_still_allowed_if_token_is_missing_scopes": {
			firstSecret:    "-",
			wantSecondCall: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31313", nil),
			},
			address: "127.0.0.1:31313",
		},
		"Authenticating_with_password_refreshes_groups": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "refreshed-group"}},
			wantGroups:               []info.Group{{Name: "refreshed-group"}},
		},
		"Authenticating_with_password_keeps_old_groups_if_fetching_groups_fails": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{groups: []info.Group{{Name: "old-group"}}},
			getGroupsFails: true,
			wantGroups:     []info.Group{{Name: "old-group"}},
		},
		"Authenticating_with_password_keeps_old_groups_if_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{groups: []info.Group{{Name: "old-group"}}},
			sessionOffline: true,
			wantGroups:     []info.Group{{Name: "old-group"}},
		},
		"Authenticating_when_the_auth_data_secret_field_uses_the_old_name": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			useOldNameForSecretField: true,
		},
		"Authenticating_to_change_password_still_allowed_if_fetching_groups_fails": {
			sessionMode:       sessionmode.ChangePassword,
			firstMode:         authmodes.Password,
			wantNextAuthModes: []string{authmodes.NewPassword},
			token:             &tokenOptions{noUserInfo: true},
			getGroupsFails:    true,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenExpired: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_due_to_inactivity_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenInactiveExpired: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_refresh_token_is_expired_due_to_ca_sign_in_frequency_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{refreshTokenStale: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_when_no_refresh_token_results_in_device_auth_as_next_mode": {
			firstMode:         authmodes.Password,
			token:             &tokenOptions{noRefreshToken: true},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr},
			wantSecondCall:    true,
			secondMode:        authmodes.DeviceQr,
		},
		"Authenticating_with_password_still_allowed_if_no_refresh_token_and_server_is_unreachable": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{noRefreshToken: true, groups: []info.Group{{Name: "old-group"}}},
			customHandlers: map[string]testutils.EndpointHandler{
				"/.well-known/openid-configuration": testutils.UnavailableHandler(),
			},
			wantGroups: []info.Group{{Name: "old-group"}},
		},
		"Authenticating_with_password_when_provider_authentication_is_forced": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{},
			forceAccessCheckWithProvider: true,
		},
		// Note: the entra_password group-fetch fallback (a returning login whose
		// liveness refresh succeeds but whose group fetch fails must use cached
		// groups, not deny) is covered by the dedicated
		// TestIsAuthenticatedPasswordEntraTokenFallsBackToCachedGroupsOnGroupFetchError,
		// which uses a provider that implements EntraPasswordProvider so the refresh
		// path is actually exercised rather than the misconfiguration no-op.
		"Extra_groups_configured": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			extraGroups:              []string{"extra-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}, {Name: "extra-group"}},
		},
		"Owner_extra_groups_configured": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			ownerExtraGroups:         []string{"owner-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}, {Name: "owner-group"}},
		},
		"Extra_and_owner_extra_groups_configured_with_existing_extra_group_in_cached_user_info": {
			firstMode: authmodes.Password,
			token: &tokenOptions{groups: []info.Group{
				{Name: "remote-group"},
				{Name: "extra-group"},
			}},
			sessionOffline:   true,
			extraGroups:      []string{"extra-group", "other-extra-group"},
			ownerExtraGroups: []string{"owner-group"},
			wantGroups:       []info.Group{{Name: "remote-group"}, {Name: "extra-group"}, {Name: "other-extra-group"}, {Name: "owner-group"}},
		},
		"Extra_and_owner_extra_groups_configured_but_already_in_cached_user_info": {
			firstMode: authmodes.Password,
			token: &tokenOptions{groups: []info.Group{
				{Name: "remote-group"},
				{Name: "extra-group"},
				{Name: "owner-group"},
			}},
			sessionOffline:   true,
			extraGroups:      []string{"extra-group"},
			ownerExtraGroups: []string{"owner-group"},
			wantGroups:       []info.Group{{Name: "remote-group"}, {Name: "extra-group"}, {Name: "owner-group"}},
		},
		"Owner_extra_groups_configured_but_user_does_not_become_owner": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			groupsReturnedByProvider: []info.Group{{Name: "remote-group"}},
			userDoesNotBecomeOwner:   true,
			allUsersAllowed:          true,
			ownerExtraGroups:         []string{"owner-group"},
			wantGroups:               []info.Group{{Name: "remote-group"}},
		},
		"Authenticating_with_device_auth_when_provider_supports_device_registration": {
			firstSecret:                        "-",
			wantSecondCall:                     true,
			providerSupportsDeviceRegistration: true,
			registerDevice:                     true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31314", &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"aud": consts.MicrosoftBrokerAppID},
					},
				}),
			},
			address: "127.0.0.1:31314",
		},
		"Authenticating_with_password_when_provider_supports_device_registration": {
			firstMode:                          authmodes.Password,
			token:                              &tokenOptions{},
			providerSupportsDeviceRegistration: true,
			registerDevice:                     true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.TokenHandler("http://127.0.0.1:31315", &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"aud": consts.MicrosoftBrokerAppID},
					},
				}),
			},
			address: "127.0.0.1:31315",
		},

		"Error_when_authentication_data_is_invalid": {invalidAuthData: true},
		"Error_when_secret_can_not_be_decrypted":    {firstMode: authmodes.Password, badFirstKey: true},
		"Error_when_provided_wrong_secret":          {firstMode: authmodes.Password, token: &tokenOptions{}, firstSecret: "wrongpassword"},
		"Authenticating_when_can_not_cache_token_without_forced_provider_access_check": {
			firstMode:       authmodes.Password,
			token:           &tokenOptions{},
			readOnlyDataDir: true,
		},
		"Error_when_can_not_cache_token_with_forced_provider_access_check": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{},
			forceAccessCheckWithProvider: true,
			readOnlyDataDir:              true,
		},
		"Error_when_IsAuthenticated_is_ongoing_for_session": {dontWaitForFirstCall: true, wantSecondCall: true},

		"Error_when_mode_is_password_and_token_does_not_exist": {firstMode: authmodes.Password},
		"Error_when_mode_is_password_but_server_returns_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.BadRequestHandler(),
			},
		},
		"Error_when_mode_is_password_and_token_is_invalid":       {firstMode: authmodes.Password, token: &tokenOptions{invalid: true}},
		"Error_when_mode_is_password_and_no_refresh_token":       {firstMode: authmodes.Password, token: &tokenOptions{noRefreshToken: true}},
		"Error_when_token_is_expired_and_refreshing_token_fails": {firstMode: authmodes.Password, token: &tokenOptions{expired: true, noRefreshToken: true}},
		"Authenticating_with_password_skips_token_refresh_network_error": {firstMode: authmodes.Password, token: &tokenOptions{expired: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
			wantOffline: true,
		},
		"Error_when_mode_is_password_and_token_refresh_times_out_with_forced_provider_auth": {
			firstMode:                    authmodes.Password,
			token:                        &tokenOptions{expired: true},
			forceAccessCheckWithProvider: true,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},

		"Error_when_mode_is_qrcode_and_link_expires": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.ExpiryDeviceAuthHandler(),
			},
		},
		"Error_when_mode_is_qrcode_and_can_not_get_token": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Error_when_mode_is_qrcode_and_can_not_get_token_due_to_timeout": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_mode_is_link_code_and_link_expires": {
			customHandlers: map[string]testutils.EndpointHandler{
				"/device_auth": testutils.ExpiryDeviceAuthHandler(),
			},
		},
		"Error_when_mode_is_link_code_and_can_not_get_token": {
			firstMode: authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.UnavailableHandler(),
			},
		},
		"Error_when_mode_is_link_code_and_can_not_get_token_due_to_timeout": {
			firstMode: authmodes.Device,
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_empty_secret_is_provided_for_local_password":  {firstSecret: "-", wantSecondCall: true, secondSecret: "-"},
		"Error_when_mode_is_newpassword_and_session_has_no_token": {firstMode: authmodes.NewPassword},
		// This test case also tests that errors with double quotes are marshaled to JSON correctly.
		"Error_when_selected_username_does_not_match_the_provider_one": {username: "not-matching", firstSecret: "-"},
		"Error_when_user_is_disabled_and_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{userIsDisabled: true},
			sessionOffline: true,
		},
		"Error_when_device_is_disabled_and_session_is_offline": {
			firstMode:      authmodes.Password,
			token:          &tokenOptions{deviceIsDisabled: true},
			sessionOffline: true,
		},
		// disabled user/device must be denied even when the session transitions from online to offline mid-auth (network error during token refresh).
		"Error_when_user_is_disabled_and_session_transitions_to_offline_due_to_network_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{userIsDisabled: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_device_is_disabled_and_session_transitions_to_offline_due_to_network_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{deviceIsDisabled: true},
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.HangingHandler(broker.MaxRequestDuration + 1),
			},
		},
		"Error_when_mode_is_invalid": {firstMode: "invalid"},
		"Error_when_thin_id_token_and_userinfo_endpoint_is_unavailable": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UnavailableHandler(),
			},
		},
		"Error_when_thin_id_token_and_userinfo_does_not_have_must_have_claim": {
			firstSecret:    "-",
			wantSecondCall: true,
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"must-have-claim"},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{}),
			},
		},
		// OIDC Core §5.3.2: /userinfo sub must equal the verified ID-token sub.
		// A malicious/MITM'd IdP that omits a required claim (triggering the UserInfo
		// fallback) and then supplies sub = victim_provider_id must be rejected.
		"Error_when_thin_id_token_and_userinfo_sub_does_not_match_id_token_sub": {
			firstSecret: "-",
			tokenHandlerOptions: &testutils.TokenHandlerOptions{
				DeleteClaims: []string{"must-have-claim"},
				IDTokenClaims: []map[string]interface{}{
					{"sub": "attacker-provider-id"},
				},
			},
			customHandlers: map[string]testutils.EndpointHandler{
				"/userinfo": testutils.UserInfoHandler(map[string]interface{}{
					"sub":             "victim-provider-id",
					"must-have-claim": "present",
				}),
			},
		},

		// MetadataProvider: the broker should call GetExtraFields and GetMetadata when the
		// provider implements MetadataProvider.
		"Successfully_authenticate_with_device_auth_when_provider_supports_metadata": {
			firstSecret:              "-",
			wantSecondCall:           true,
			providerSupportsMetadata: true,
		},
		"Error_when_device_auth_metadata_provider_fails_to_get_metadata": {
			firstSecret:              "-",
			wantSecondCall:           true,
			providerSupportsMetadata: true,
			metadataGetErr:           errors.New("metadata unavailable"),
		},
		"Authenticating_with_password_when_provider_supports_metadata": {
			firstMode:                authmodes.Password,
			token:                    &tokenOptions{},
			providerSupportsMetadata: true,
		},

		// NoGroupFetcher: when the provider does not implement GroupFetcher, getGroups
		// returns nil and the user is authenticated without remote groups.
		"Authenticating_with_password_when_provider_has_no_group_fetcher": {
			firstMode:                 authmodes.Password,
			token:                     &tokenOptions{},
			providerHasNoGroupFetcher: true,
			wantGroups:                []info.Group{},
		},

		// UserDisabledChecker: when a token refresh fails with a provider-specific
		// "user disabled" error code, login is denied and the token is marked disabled.
		"Error_when_user_is_disabled_according_to_user_disabled_checker": {
			firstMode:                         authmodes.Password,
			token:                             &tokenOptions{},
			providerSupportsUserDisabledCheck: true,
			userDisabledErrorCode:             "user_disabled",
			customHandlers: map[string]testutils.EndpointHandler{
				"/token": testutils.ErrorResponseHandler(http.StatusBadRequest,
					`{"error":"user_disabled","error_description":"User account is disabled"}`),
			},
		},

		// getGroups error cases: test that errors returned from the GroupFetcher are
		// handled correctly.
		"Error_when_getgroups_returns_device_disabled_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, providerErrors.ErrDeviceDisabled
			},
		},
		"Error_when_getgroups_returns_invalid_redirect_uri_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, providerErrors.ErrInvalidRedirectURI
			},
		},
		"Error_when_getgroups_returns_retry_with_device_auth_error": {
			firstMode: authmodes.Password,
			token:     &tokenOptions{},
			getGroupsFunc: func() ([]info.Group, error) {
				return nil, &providerErrors.RetryWithDeviceAuthError{Err: errors.New("token acquisition failed")}
			},
			wantNextAuthModes: []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.sessionMode == "" {
				tc.sessionMode = sessionmode.Login
			}

			if tc.sessionOffline {
				tc.customHandlers = map[string]testutils.EndpointHandler{
					"/.well-known/openid-configuration": testutils.UnavailableHandler(),
				}
			}

			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")

			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			cfg := &brokerForTestConfig{
				Config:                        broker.Config{DataDir: dataDir},
				getGroupsFails:                tc.getGroupsFails,
				ownerAllowed:                  true,
				firstUserBecomesOwner:         !tc.userDoesNotBecomeOwner,
				allUsersAllowed:               tc.allUsersAllowed,
				forceAccessCheckWithProvider:  tc.forceAccessCheckWithProvider,
				extraGroups:                   tc.extraGroups,
				ownerExtraGroups:              tc.ownerExtraGroups,
				supportsDeviceRegistration:    tc.providerSupportsDeviceRegistration,
				requireNameClaimOnInitialAuth: tc.requireNameClaimOnInitialAuth,
				registerDevice:                tc.registerDevice,
				supportsMetadata:              tc.providerSupportsMetadata,
				metadataGetErr:                tc.metadataGetErr,
				supportsUserDisabledCheck:     tc.providerSupportsUserDisabledCheck,
				userDisabledErrorCode:         tc.userDisabledErrorCode,
				supportsFetchingGroups:        !tc.providerHasNoGroupFetcher,
				tokenHandlerOptions:           tc.tokenHandlerOptions,
			}
			if tc.customHandlers == nil {
				// Use the default provider URL if no custom handlers are provided.
				cfg.issuerURL = defaultIssuerURL
			} else {
				cfg.customHandlers = tc.customHandlers
				cfg.listenAddress = tc.address
			}
			if tc.groupsReturnedByProvider != nil {
				cfg.getGroupsFunc = func() ([]info.Group, error) {
					return tc.groupsReturnedByProvider, nil
				}
			}
			if tc.getGroupsFunc != nil {
				cfg.getGroupsFunc = tc.getGroupsFunc
			}
			b := newBrokerForTests(t, cfg)

			sessionID, key := newSessionForTests(t, b, tc.username, tc.sessionMode)

			if tc.token != nil {
				generateAndStoreCachedInfo(t, *tc.token, b.TokenPathForSession(sessionID))
				err = password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")
			}

			var readOnlyDataCleanup, readOnlyTokenCleanup func()
			if tc.readOnlyDataDir {
				if tc.token != nil {
					readOnlyTokenCleanup = testutils.MakeReadOnly(t, b.TokenPathForSession(sessionID))
					t.Cleanup(readOnlyTokenCleanup)
				}
				readOnlyDataCleanup = testutils.MakeReadOnly(t, b.DataDir())
				t.Cleanup(readOnlyDataCleanup)
			}

			switch tc.firstSecret {
			case "":
				tc.firstSecret = correctPassword
			case "-":
				tc.firstSecret = ""
			}

			authData := "{}"
			if tc.firstSecret != "" {
				eKey := key
				if tc.badFirstKey {
					eKey = ""
				}
				secret := encryptSecret(t, tc.firstSecret, eKey)
				field := broker.AuthDataSecret
				if tc.useOldNameForSecretField {
					field = broker.AuthDataSecretOld
				}
				authData = fmt.Sprintf(`{"%s":"%s"}`, field, secret)
			}
			if tc.invalidAuthData {
				authData = "invalid json"
			}

			firstCallDone := make(chan struct{})
			go func() {
				defer close(firstCallDone)

				if tc.firstMode == "" {
					tc.firstMode = authmodes.DeviceQr
				}
				updateAuthModes(t, b, sessionID, tc.firstMode)

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_call"), out, 0600)
				require.NoError(t, err, "Failed to write first response")

				if tc.wantNextAuthModes != nil {
					nextAuthModes := b.GetNextAuthModes(sessionID)
					require.ElementsMatch(t, tc.wantNextAuthModes, nextAuthModes, "Next auth modes should match")
				}

				if tc.wantGroups != nil {
					type userInfoMsgType struct {
						UserInfo info.User `json:"userinfo"`
					}
					userInfoMsg := userInfoMsgType{}
					err = json.Unmarshal([]byte(data), &userInfoMsg)
					require.NoError(t, err, "Failed to unmarshal user info message")
					userInfo := userInfoMsg.UserInfo
					require.ElementsMatch(t, tc.wantGroups, userInfo.Groups, "Groups should match")
				}
				if tc.wantGecos != "" {
					type userInfoMsgType struct {
						UserInfo info.User `json:"userinfo"`
					}
					userInfoMsg := userInfoMsgType{}
					err = json.Unmarshal([]byte(data), &userInfoMsg)
					require.NoError(t, err, "Failed to unmarshal user info message")
					require.Equal(t, tc.wantGecos, userInfoMsg.UserInfo.Gecos, "GECOS should match")
				}
			}()

			if !tc.dontWaitForFirstCall {
				<-firstCallDone
			}

			if tc.wantOffline {
				gotOffline, err := b.IsOffline(sessionID)
				require.NoError(t, err, "IsOffline should not have returned an error")
				require.True(t, gotOffline, "Session should be offline after token refresh network error")
			}

			// When forceAccessCheckWithProvider is set, offline fallback must never happen,
			// even for transient network errors. Verify the session was not flipped to offline.
			// (Skip if the session was already offline at creation, which is a separate scenario.)
			if tc.forceAccessCheckWithProvider && !tc.sessionOffline {
				gotOffline, err := b.IsOffline(sessionID)
				require.NoError(t, err, "IsOffline should not have returned an error")
				require.False(t, gotOffline, "Session should not be offline when forceAccessCheckWithProvider is true")
			}

			if tc.wantSecondCall {
				// Give some time for the first call
				time.Sleep(10 * time.Millisecond)

				secret := "passwordpassword"
				if tc.secondSecret == "-" {
					secret = ""
				}

				secret = encryptSecret(t, secret, key)
				field := broker.AuthDataSecret
				if tc.useOldNameForSecretField {
					field = broker.AuthDataSecretOld
				}
				secondAuthData := fmt.Sprintf(`{"%s":"%s"}`, field, secret)
				if tc.invalidAuthData {
					secondAuthData = "invalid json"
				}

				if tc.secondMode == "" {
					tc.secondMode = authmodes.NewPassword
				}

				secondCallDone := make(chan struct{})
				go func() {
					defer close(secondCallDone)

					updateAuthModes(t, b, sessionID, tc.secondMode)

					access, data, err := b.IsAuthenticated(sessionID, secondAuthData)
					require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

					got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
					out, err := yaml.Marshal(got)
					require.NoError(t, err, "Failed to marshal second response")

					err = os.WriteFile(filepath.Join(outDir, "second_call"), out, 0600)
					require.NoError(t, err, "Failed to write second response")
				}()
				<-secondCallDone
			}
			<-firstCallDone

			// We need to restore some permissions in order to save the golden files.
			if tc.readOnlyDataDir {
				readOnlyDataCleanup()
				if tc.token != nil {
					readOnlyTokenCleanup()
				}
			}

			// Ensure that the token content is generic to avoid golden file conflicts
			if _, err := os.Stat(b.TokenPathForSession(sessionID)); err == nil {
				err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely a token"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic token file")
			}
			passwordPath := b.PasswordFilepathForSession(sessionID)
			if _, err := os.Stat(passwordPath); err == nil {
				err := os.WriteFile(passwordPath, []byte("Definitely a hashed password"), 0600)
				require.NoError(t, err, "Teardown: Failed to write generic password file")
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			if _, err := os.Stat(filepath.Dir(b.TokenPathForSession(sessionID))); err == nil {
				issuerDir := filepath.Dir(filepath.Dir(b.TokenPathForSession(sessionID)))
				newIssuerDir := filepath.Join(filepath.Dir(issuerDir), "provider_url")
				err := os.Rename(issuerDir, newIssuerDir)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename token directory")
					t.Logf("Failed to rename token directory: %v", err)
				}

				// Remove compatibility symlinks left by cache migration.
				// These point to temp directories and break golden file comparison.
				entries, _ := os.ReadDir(newIssuerDir)
				for _, entry := range entries {
					if entry.Type()&os.ModeSymlink != 0 {
						os.Remove(filepath.Join(newIssuerDir, entry.Name()))
					}
				}
			}

			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

// Due to ordering restrictions, this test can not be run in parallel, otherwise the routines would not be ordered as expected.
func TestConcurrentIsAuthenticated(t *testing.T) {
	tests := map[string]struct {
		firstCallDelay        int
		secondCallDelay       int
		ownerAllowed          bool
		allUsersAllowed       bool
		firstUserBecomesOwner bool

		timeBetween time.Duration
	}{
		"First_auth_starts_and_finishes_before_second": {
			secondCallDelay: 1,
			timeBetween:     2 * time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_but_second_finishes_first": {
			firstCallDelay:  3,
			timeBetween:     time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_then_second_starts_and_first_finishes": {
			firstCallDelay:  2,
			secondCallDelay: 3,
			timeBetween:     time.Second,
			allUsersAllowed: true,
		},
		"First_auth_starts_first_but_second_finishes_first_and_is_registered_as_the_owner": {
			firstCallDelay:        3,
			timeBetween:           time.Second,
			ownerAllowed:          true,
			firstUserBecomesOwner: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")
			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			username1 := "user1@example.com"
			username2 := "user2@example.com"

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                 broker.Config{DataDir: dataDir},
				allUsersAllowed:        tc.allUsersAllowed,
				ownerAllowed:           tc.ownerAllowed,
				firstUserBecomesOwner:  tc.firstUserBecomesOwner,
				firstCallDelay:         tc.firstCallDelay,
				secondCallDelay:        tc.secondCallDelay,
				supportsFetchingGroups: true,
				tokenHandlerOptions: &testutils.TokenHandlerOptions{
					IDTokenClaims: []map[string]interface{}{
						{"sub": "user1", "name": "user1", "email": username1},
						{"sub": "user2", "name": "user2", "email": username2},
					},
				},
			})

			firstSession, firstKey := newSessionForTests(t, b, username1, "")
			firstToken := tokenOptions{username: username1}
			generateAndStoreCachedInfo(t, firstToken, b.TokenPathForSession(firstSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(firstSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			secondSession, secondKey := newSessionForTests(t, b, username2, "")
			secondToken := tokenOptions{username: username2}
			generateAndStoreCachedInfo(t, secondToken, b.TokenPathForSession(secondSession))
			err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(secondSession))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			firstCallDone := make(chan struct{})
			go func() {
				t.Logf("%s: First auth starting", t.Name())
				defer close(firstCallDone)

				updateAuthModes(t, b, firstSession, authmodes.Password)

				secret := encryptSecret(t, "password", firstKey)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

				access, data, err := b.IsAuthenticated(firstSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal first response")

				err = os.WriteFile(filepath.Join(outDir, "first_auth"), out, 0600)
				require.NoError(t, err, "Failed to write first response")

				t.Logf("%s: First auth done", t.Name())
			}()

			time.Sleep(tc.timeBetween)

			secondCallDone := make(chan struct{})
			go func() {
				t.Logf("%s: Second auth starting", t.Name())
				defer close(secondCallDone)

				updateAuthModes(t, b, secondSession, authmodes.Password)

				secret := encryptSecret(t, "password", secondKey)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

				access, data, err := b.IsAuthenticated(secondSession, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")

				got := isAuthenticatedResponse{Access: access, Data: data, Err: fmt.Sprint(err)}
				out, err := yaml.Marshal(got)
				require.NoError(t, err, "Failed to marshal second response")

				err = os.WriteFile(filepath.Join(outDir, "second_auth"), out, 0600)
				require.NoError(t, err, "Failed to write second response")

				t.Logf("%s: Second auth done", t.Name())
			}()

			<-firstCallDone
			<-secondCallDone

			for _, sessionID := range []string{firstSession, secondSession} {
				// Ensure that the token content is generic to avoid golden file conflicts
				if _, err := os.Stat(b.TokenPathForSession(sessionID)); err == nil {
					err := os.WriteFile(b.TokenPathForSession(sessionID), []byte("Definitely a token"), 0600)
					require.NoError(t, err, "Teardown: Failed to write generic token file")
				}
				passwordPath := b.PasswordFilepathForSession(sessionID)
				if _, err := os.Stat(passwordPath); err == nil {
					err := os.WriteFile(passwordPath, []byte("Definitely a hashed password"), 0600)
					require.NoError(t, err, "Teardown: Failed to write generic password file")
				}
			}

			// Ensure that the directory structure is generic to avoid golden file conflicts
			issuerDataDir := filepath.Dir(b.UserDataDirForSession(firstSession))
			if _, err := os.Stat(issuerDataDir); err == nil {
				newIssuerDir := filepath.Join(filepath.Dir(issuerDataDir), "provider_url")
				err := os.Rename(issuerDataDir, newIssuerDir)
				if err != nil {
					require.ErrorIs(t, err, os.ErrNotExist, "Teardown: Failed to rename issuer data directory")
					t.Logf("Failed to rename issuer data directory: %v", err)
				}

				// Remove compatibility symlinks left by cache migration.
				entries, _ := os.ReadDir(newIssuerDir)
				for _, entry := range entries {
					if entry.Type()&os.ModeSymlink != 0 {
						os.Remove(filepath.Join(newIssuerDir, entry.Name()))
					}
				}
			}
			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

func TestIsAuthenticatedAllowedUsersConfig(t *testing.T) {
	t.Parallel()

	u1 := "u1"
	u2 := "u2"
	u3 := "U3"
	allUsers := []string{u1, u2, u3}

	idTokenClaims := []map[string]interface{}{}
	for _, uname := range allUsers {
		idTokenClaims = append(idTokenClaims, map[string]interface{}{"sub": "user", "name": "user", "email": uname})
	}

	tests := map[string]struct {
		allowedUsers          map[string]struct{}
		owner                 string
		ownerAllowed          bool
		allUsersAllowed       bool
		firstUserBecomesOwner bool

		wantAllowedUsers   []string
		wantUnallowedUsers []string
	}{
		"No_users_allowed": {
			wantUnallowedUsers: allUsers,
		},
		"No_users_allowed_when_owner_is_allowed_but_not_set": {
			ownerAllowed:       true,
			wantUnallowedUsers: allUsers,
		},
		"No_users_allowed_when_owner_is_set_but_not_allowed": {
			owner:              u1,
			wantUnallowedUsers: allUsers,
		},

		"All_users_are_allowed": {
			allUsersAllowed:  true,
			wantAllowedUsers: allUsers,
		},
		"Only_owner_allowed": {
			ownerAllowed:       true,
			owner:              u1,
			wantAllowedUsers:   []string{u1},
			wantUnallowedUsers: []string{u2, u3},
		},
		"Only_first_user_allowed": {
			ownerAllowed:          true,
			firstUserBecomesOwner: true,
			wantAllowedUsers:      []string{u1},
			wantUnallowedUsers:    []string{u2, u3},
		},
		"Specific_users_allowed": {
			allowedUsers:       map[string]struct{}{u1: {}, u2: {}},
			wantAllowedUsers:   []string{u1, u2},
			wantUnallowedUsers: []string{u3},
		},
		"Specific_users_and_owner": {
			ownerAllowed:       true,
			allowedUsers:       map[string]struct{}{u1: {}},
			owner:              u2,
			wantAllowedUsers:   []string{u1, u2},
			wantUnallowedUsers: []string{u3},
		},
		"Usernames_are_normalized": {
			ownerAllowed:       true,
			allowedUsers:       map[string]struct{}{u3: {}},
			owner:              strings.ToLower(u3),
			wantAllowedUsers:   []string{u3},
			wantUnallowedUsers: []string{u1, u2},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			outDir := t.TempDir()
			dataDir := filepath.Join(outDir, "data")
			err := os.Mkdir(dataDir, 0700)
			require.NoError(t, err, "Setup: Mkdir should not have returned an error")

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                broker.Config{DataDir: dataDir},
				allowedUsers:          tc.allowedUsers,
				owner:                 tc.owner,
				ownerAllowed:          tc.ownerAllowed,
				allUsersAllowed:       tc.allUsersAllowed,
				firstUserBecomesOwner: tc.firstUserBecomesOwner,
				tokenHandlerOptions: &testutils.TokenHandlerOptions{
					IDTokenClaims: idTokenClaims,
				},
			})

			for _, u := range allUsers {
				sessionID, key := newSessionForTests(t, b, u, "")
				token := tokenOptions{username: u}
				generateAndStoreCachedInfo(t, token, b.TokenPathForSession(sessionID))
				err = password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID))
				require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

				updateAuthModes(t, b, sessionID, authmodes.Password)

				secret := encryptSecret(t, "password", key)
				authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)

				access, data, err := b.IsAuthenticated(sessionID, authData)
				require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be a valid JSON")
				require.NoError(t, err)
				if slices.Contains(tc.wantAllowedUsers, u) {
					require.Equal(t, broker.AuthGranted, access, "authentication failed")
					continue
				}
				if slices.Contains(tc.wantUnallowedUsers, u) {
					require.Equal(t, broker.AuthDenied, access, "authentication failed")
					continue
				}
				t.Fatalf("user %s is not in the allowed or unallowed users list", u)
			}
		})
	}
}

func TestCancelIsAuthenticated(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": testutils.HangingHandler(3 * time.Second),
		},
	})
	sessionID, _ := newSessionForTests(t, b, "", "")

	updateAuthModes(t, b, sessionID, authmodes.DeviceQr)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _, err := b.IsAuthenticated(sessionID, `{}`)
		require.Error(t, err, "IsAuthenticated should have returned an error")
	}()

	// Wait for the call to hang
	time.Sleep(50 * time.Millisecond)

	b.CancelIsAuthenticated(sessionID)
	<-stopped
}

func TestIsAuthenticatedMaxAttempts(t *testing.T) {
	t.Parallel()

	correctPassword := "password"

	tests := map[string]struct {
		apiVersion uint
		wantAccess string
	}{
		"Returns_denied-max-tries_when_api_version_is_2": {
			apiVersion: 2,
			wantAccess: broker.AuthDeniedMaxTries,
		},
		"Returns_denied-max-tries_when_api_version_is_greater_than_2": {
			apiVersion: 3,
			wantAccess: broker.AuthDeniedMaxTries,
		},
		"Returns_denied_when_api_version_is_less_than_2": {
			apiVersion: 1,
			wantAccess: broker.AuthDenied,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL:  defaultIssuerURL,
				apiVersion: tc.apiVersion,
			})

			sessionID, key := newSessionForTests(t, b, "", "")

			// Store a valid token and password so that password auth mode is available.
			generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
			err := password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
			require.NoError(t, err, "Setup: HashAndStorePassword should not have returned an error")

			wrongSecret := encryptSecret(t, "wrongpassword", key)
			authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, wrongSecret)

			err = b.SetAttemptsPerMode(sessionID, authmodes.Password, broker.MaxAuthAttempts-1)
			require.NoError(t, err, "Setup: Failed to set auth attempts")
			updateAuthModes(t, b, sessionID, authmodes.Password)

			access, data, err := b.IsAuthenticated(sessionID, authData)
			require.NoError(t, err, "IsAuthenticated should not have returned an error")

			require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
			require.Equal(t, tc.wantAccess, access, "Final attempt should return %s", tc.wantAccess)
			golden.CheckOrUpdateYAML(t, isAuthenticatedResponse{Access: access, Data: data, Err: ""})
		})
	}
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		issuerURL: defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "", "")

	// Try to end a session that does not exist
	err := b.EndSession("nonexistent")
	require.Error(t, err, "EndSession should have returned an error when ending a nonexistent session")

	// End a session that exists
	err = b.EndSession(sessionID)
	require.NoError(t, err, "EndSession should not have returned an error when ending an existent session")
}

func TestEndSessionReleasesPendingMFAFlow(t *testing.T) {
	t.Parallel()

	released := 0
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    newTrackedMFAFlowState(func() { released++ }),
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 5000,
			MaxPollAttempts:   10,
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, 0, released, "MFA flow should still be active before ending the session")

	err = b.EndSession(sessionID)
	require.NoError(t, err)
	require.Equal(t, 1, released, "EndSession should release any pending MFA flow state")
}

func TestIsAuthenticatedEntraMFAWaitStartsPollingAtOne(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, "{}", data, "AuthNext after password should carry no message (avoids PAM read-delay)")
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")
	require.Equal(t, "Approve the sign-in request in Microsoft Authenticator", layout["label"],
		"entra_mfa_wait layout label should reflect the MFA challenge message")

	access, data, err = b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	require.Equal(t, []int{1}, provider.recordedPollAttempts)
	require.Equal(t, []string{""}, provider.recordedChallengeData)

	var grantPayload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &grantPayload))
	require.Equal(t, broker.CachedPasswordMessage, grantPayload.Message,
		"Entra MFA completion should attach the offline-password caching notice")

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the offline password")
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the refreshed token")
}

// advanceToEntraMFAWait submits the Entra password for the session and selects the
// entra_mfa_wait mode, leaving the session ready for the polling IsAuthenticated("{}").
func advanceToEntraMFAWait(t *testing.T, b *broker.Broker, sessionID, key string) {
	t.Helper()

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	require.NoError(t, b.SetAvailableMode(sessionID, authmodes.EntraMFAWait))
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
}

// TestIsAuthenticatedEntraMFADeniesOnAccessTokenVerificationFailure verifies that
// when the MFA access token fails signature verification (the TLS-MITM defense),
// the login is denied rather than trusting the token's identity claims.
func TestIsAuthenticatedEntraMFADeniesOnAccessTokenVerificationFailure(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult:       newMFATokenResult(mfaAuthInfo.Token),
		verifyAccessTokenErr: errors.New("token signature verification failed"),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"an access token that fails signature verification must be denied")
}

// TestIsAuthenticatedEntraMFAWaitPollsWhenMaxPollAttemptsZero verifies that a
// MaxPollAttempts value of 0 (which libhimmelblau can produce from
// expires_in/polling_interval flooring to zero) still polls rather than returning
// an immediate, false "MFA timed out".
func TestIsAuthenticatedEntraMFAWaitPollsWhenMaxPollAttemptsZero(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider:   &testutils.MockProvider{},
		flowState:      &himmelblau.MFAFlowState{},
		challengeInfo:  &himmelblau.MFAChallengeInfo{Message: "Approve the sign-in request", PollingIntervalMs: 1, MaxPollAttempts: 0},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "MaxPollAttempts==0 must still poll, not instant-timeout")
	require.Equal(t, []int{1}, provider.recordedPollAttempts, "the poll loop must run at least once when MaxPollAttempts==0")
}

// TestIsAuthenticatedEntraMFADeniesOnNilToken verifies the defensive nil-token
// guard: a provider returning (nil, nil) from AcquireTokenByMFAFlow must deny
// rather than panic the broker on the token dereference in finishEntraAuth.
func TestIsAuthenticatedEntraMFADeniesOnNilToken(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	provider := &mockMFANilTokenProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider:  &testutils.MockProvider{},
			flowState:     &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{Message: "Approve the sign-in request", PollingIntervalMs: 1, MaxPollAttempts: 1},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a (nil, nil) MFA result must deny, not panic")
}

// TestIsAuthenticatedEntraMFAWaitNumberMatchingLabelShown verifies that when the MFA
// challenge message from libhimmelblau includes a number-matching code (e.g.
// PhoneAppNotification with entropy), that message is used as the entra_mfa_wait
// layout label so the user can see the number to match in the Authenticator app.
func TestIsAuthenticatedEntraMFAWaitNumberMatchingLabelShown(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	// Simulate the message libhimmelblau returns for PhoneAppNotification with number
	// matching: "Open your Authenticator app, and enter the number '60' to sign in."
	numberMatchingMsg := "Open your Authenticator app, and enter the number '60' to sign in."
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           numberMatchingMsg,
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 5000,
			MaxPollAttempts:   10,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Submit password – broker should offer entra_mfa_wait for PhoneAppNotification.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	require.Equal(t, numberMatchingMsg, layout["label"],
		"entra_mfa_wait label must show the number-matching message so the user can approve in the Authenticator app")
}

// TestIsAuthenticatedEntraMFAWaitDeniedWhenDeviceRegistrationFails verifies
// that authentication is denied when device registration fails, even after
// successful MFA. Without device registration the token exchange cannot be
// completed and group membership cannot be resolved, so granting access would
// leave the user in a broken state.
func TestIsAuthenticatedEntraMFAWaitDeniedWhenDeviceRegistrationFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockDeviceRegistrationFailProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:           "Approve the sign-in request in Microsoft Authenticator",
				Method:            "PhoneAppNotification",
				PollingIntervalMs: 5000,
				MaxPollAttempts:   10,
			},
			mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
		registerDevice:        true,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — should initiate MFA.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFAWait}, b.GetNextAuthModes(sessionID))

	// Step 2: MFA poll — device registration fails and auth should be denied.
	updateAuthModes(t, b, sessionID, authmodes.EntraMFAWait)
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"device registration failure must deny auth: without device registration the token exchange and group resolution cannot succeed")
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
}

func TestIsAuthenticatedEntraMFADeniedWhenInitialGroupFetchFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFails: true},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "initial Entra MFA logins must be denied when groups cannot be resolved")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "Failed to retrieve groups")
}

func TestIsAuthenticatedEntraMFAUsesCachedGroupsWhenRefreshFails(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	oldAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	oldAuthInfo.UserInfo.Groups = []info.Group{{Name: "cached-group", UGID: "cached-id"}}
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFails: true},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), oldAuthInfo))

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "cached groups should permit re-authentication when Graph refresh fails")

	var payload struct {
		UserInfo struct {
			Groups []info.Group `json:"groups"`
		} `json:"userinfo"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, []info.Group{{Name: "cached-group", UGID: "cached-id"}}, payload.UserInfo.Groups)
}

// TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin verifies that on a
// first Entra MFA login (no cached groups to fall back to) a group fetch that fails
// with a user-displayable ForDisplayError (e.g. a missing GroupMember.Read.All
// permission — a configuration problem) is surfaced verbatim by finishEntraAuth
// instead of being replaced by a misleading generic network hint. This is
// independent of force_access_check_with_provider (left unset here on purpose): the
// surfacing is driven by there being no cached groups, not by the forced check.
func TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	const graphPermMsg = "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{
			GetGroupsFunc: func() ([]info.Group, error) {
				return nil, &providerErrors.ForDisplayError{Message: graphPermMsg}
			},
		},
		flowState: &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, graphPermMsg, payload.Message,
		"a ForDisplayError from the group fetch must be surfaced verbatim, not replaced by the generic network message")
	require.NotContains(t, payload.Message, "network connection")
}

func TestGetAuthenticationModesFiltersNextAuthModesByFlows(t *testing.T) {
	t.Parallel()

	// Use a provider that implements EntraPasswordProvider so that
	// authModeIsAvailable can confirm the capability before offering the mode.
	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{},
		flowState:     &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{},
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                 broker.Config{DataDir: t.TempDir()},
		provider:               provider,
		issuerURL:              defaultIssuerURL,
		ownerAllowed:           true,
		firstUserBecomesOwner:  true,
		deviceAuthFlowDisabled: true,
		// Provide a group source (device registration) so the entra_password
		// flow passes the group-lookup availability check in authModeIsAvailable.
		registerDevice: true,
	})

	sessionID, _ := newSessionForTests(t, b, "", sessionmode.Login)
	b.SetNextAuthModes(sessionID, []string{authmodes.EntraPassword, authmodes.DeviceQr})

	modes, err := b.GetAuthenticationModes(sessionID, []map[string]string{
		supportedUILayouts["form"],
		supportedUILayouts["qrcode"],
	})
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{
		"id":    authmodes.EntraPassword,
		"label": authmodes.Label[authmodes.EntraPassword],
	}}, modes)
}

// TestGetAuthenticationModesEntraPasswordRequiresGroupSource verifies that once
// the broker has started successfully, the entra_password mode is offered only
// when a Microsoft Graph group source is available, i.e. device registration or
// a client secret. The missing-group-source case is rejected earlier by New().
func TestGetAuthenticationModesEntraPasswordRequiresGroupSource(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		registerDevice bool
		clientSecret   string
	}{
		"Offered_with_device_registration": {registerDevice: true},
		"Offered_with_client_secret":       {clientSecret: "test-client-secret"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &mockEntraPasswordProvider{
				MockProvider:  &testutils.MockProvider{},
				flowState:     &himmelblau.MFAFlowState{},
				challengeInfo: &himmelblau.MFAChallengeInfo{},
			}
			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                broker.Config{DataDir: t.TempDir()},
				provider:              provider,
				issuerURL:             defaultIssuerURL,
				clientSecret:          tc.clientSecret,
				ownerAllowed:          true,
				firstUserBecomesOwner: true,
				registerDevice:        tc.registerDevice,
			})

			sessionID, _ := newSessionForTests(t, b, "", sessionmode.Login)
			b.SetNextAuthModes(sessionID, []string{authmodes.EntraPassword, authmodes.DeviceQr})

			modes, err := b.GetAuthenticationModes(sessionID, []map[string]string{
				supportedUILayouts["form"],
				supportedUILayouts["qrcode"],
			})
			require.NoError(t, err)

			var ids []string
			for _, m := range modes {
				ids = append(ids, m["id"])
			}
			require.Contains(t, ids, authmodes.EntraPassword, "entra_password should be offered when a group source is available")
		})
	}
}

func TestIsAuthenticatedPasswordGrantRevokedInvalidatesCachedCredentials(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config: broker.Config{DataDir: t.TempDir()},
		provider: &mockGrantRevokedProvider{mockProviderWithEntraModes: &mockProviderWithEntraModes{
			MockProvider: &testutils.MockProvider{},
		}},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS50173: The provided grant has been revoked due to a password reset."}`))
			},
		},
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{}, b.TokenPathForSession(sessionID))
	err := password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err)

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	// reauthModes includes EntraPassword, but the provider does not implement
	// EntraPasswordProvider, so authModeIsAvailable filters it out — only
	// Device/DeviceQr survive into the actual offer.
	require.Equal(t, []string{authmodes.EntraPassword, authmodes.Device, authmodes.DeviceQr}, b.GetNextAuthModes(sessionID))

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.ErrorIs(t, err, os.ErrNotExist)

	nextSessionID, _ := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	modes, err := b.GetAuthenticationModes(nextSessionID, []map[string]string{
		supportedUILayouts["form"],
		supportedUILayouts["qrcode"],
	})
	require.NoError(t, err)

	var modeIDs []string
	for _, mode := range modes {
		modeIDs = append(modeIDs, mode["id"])
	}
	// entra_password is in reauthModes but filtered by the capability check; only device modes offered.
	require.ElementsMatch(t, []string{authmodes.DeviceQr}, modeIDs)
}

// TestIsAuthenticatedPasswordEntraTokenFallsBackToCachedGroupsOnGroupFetchError
// verifies that on a returning login with a cached Entra password + MFA token, a
// group-fetch failure — even a user-displayable ForDisplayError such as a missing
// GroupMember.Read.All permission — falls back to the cached groups instead of
// denying, exactly like the device-auth flow. The live provider check now happens
// at the token refresh (see refreshEntraPasswordToken), so the group fetch is no
// longer a liveness signal. The ForDisplayError is still surfaced on a *first*
// login that has no cached groups (see
// TestIsAuthenticatedEntraMFASurfacesForDisplayErrorOnFirstLogin).
func TestIsAuthenticatedPasswordEntraTokenFallsBackToCachedGroupsOnGroupFetchError(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const graphPermMsg = "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"
	cachedGroups := []info.Group{{Name: "cached-group", UGID: "cached-id"}}

	// The token was obtained via the entra_password flow, so the provider must
	// implement EntraPasswordProvider for the returning-login liveness refresh.
	// The refresh succeeds (active user); the subsequent group fetch fails, which
	// must fall back to cached groups rather than deny.
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{
			GetGroupsFunc: func() ([]info.Group, error) {
				return nil, &providerErrors.ForDisplayError{Message: graphPermMsg}
			},
		},
	}
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                       broker.Config{DataDir: t.TempDir()},
		provider:                     provider,
		ownerAllowed:                 true,
		firstUserBecomesOwner:        true,
		issuerURL:                    defaultIssuerURL,
		forceAccessCheckWithProvider: true,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true, groups: cachedGroups}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access,
		"a returning Entra login must fall back to cached groups when the group fetch fails, not deny")

	var payload struct {
		UserInfo struct {
			Groups []info.Group `json:"groups"`
		} `json:"userinfo"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Equal(t, cachedGroups, payload.UserInfo.Groups, "cached groups must be used when the group fetch fails")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshDetectsDisabledUser verifies that on a
// returning login the Entra password token refresh (refreshEntraPasswordToken) is the
// live disabled-user check: an AADSTS50057-class rejection is classified exactly like
// the device-auth flow — login is denied and UserIsDisabled is cached so later offline
// attempts are denied too.
func TestIsAuthenticatedPasswordEntraTokenRefreshDetectsDisabledUser(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	provider := &mockEntraPasswordProvider{
		MockProvider:          &testutils.MockProvider{},
		userDisabledErrorCode: "user_disabled",
		refreshErr: &oauth2.RetrieveError{
			ErrorCode:        "user_disabled",
			ErrorDescription: "AADSTS50057: The user account is disabled.",
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, data, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "a disabled user must be denied at the refresh step")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "disabled")

	// The disabled state must be cached so subsequent offline logins are denied too.
	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.True(t, cached.UserIsDisabled, "UserIsDisabled must be cached after an AADSTS50057 refresh rejection")
}

// TestIsAuthenticatedPasswordRefreshPreservesRotationOnUserInfoError verifies
// that the generic OIDC refresh path preserves a rotated refresh token even if
// a later local validation step (here: ID-token verification in getUserInfo)
// fails. Otherwise the cache can be stranded with a refresh token the provider
// already invalidated server-side.
func TestIsAuthenticatedPasswordRefreshPreservesRotationOnUserInfoError(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const listenAddress = "127.0.0.1:31317"
	const rotatedRefreshToken = "rotated-refresh-token"

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		listenAddress:         listenAddress,
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": func(w http.ResponseWriter, _ *http.Request) {
				response := fmt.Sprintf(`{
					"access_token": "accesstoken",
					"refresh_token": %q,
					"token_type": "Bearer",
					"scope": %q,
					"expires_in": 3600,
					"id_token": ".invalid."
				}`, rotatedRefreshToken, strings.Join(consts.DefaultScopes, " "))
				w.Header().Add("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			},
		},
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	seeded := generateCachedInfo(t, tokenOptions{})
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), seeded))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"a refreshed token whose ID token cannot be verified must deny the returning login")

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, rotatedRefreshToken, cached.Token.RefreshToken,
		"a local user-info failure must not discard an already-rotated refresh token")
	require.Equal(t, seeded.RawIDToken, cached.RawIDToken,
		"a failed local validation must not replace the cached raw ID token")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshRotatesRefreshToken verifies that a
// successful Entra password token refresh on a returning login rotates the cached
// refresh token (kept fresh on each login, like the device-auth flow) and that the
// rotated token is persisted for the next login.
func TestIsAuthenticatedPasswordEntraTokenRefreshRotatesRefreshToken(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const rotatedRefreshToken = "rotated-refresh-token"
	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) { return []info.Group{{Name: "remote-group"}}, nil }},
		refreshResult: &oauth2.Token{AccessToken: "new-access-token", RefreshToken: rotatedRefreshToken},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true, groups: []info.Group{{Name: "remote-group"}}}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, rotatedRefreshToken, cached.Token.RefreshToken,
		"the rotated refresh token from refreshEntraPasswordToken must be persisted")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshUpdatesUserInfo verifies that a
// successful Entra password token refresh re-derives the cached user info via
// the provider's access-token claim extraction (rather than refreshed-token
// extras), preserves the cached gecos when the refreshed token omits it, and
// keeps the separately-managed groups unchanged.
func TestIsAuthenticatedPasswordEntraTokenRefreshUpdatesUserInfo(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) {
			return []info.Group{{Name: "remote-group"}}, nil
		}},
		refreshResult:       &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
		accessTokenUserInfo: &info.User{Name: "test-user@email.com", ProviderID: "saved-user-id"},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	// Seed a stale cached token with a different gecos and the groups that should
	// survive the refresh.
	generateAndStoreCachedInfo(t, tokenOptions{
		obtainedViaEntraPasswordAuth: true,
		gecos:                        "stale gecos",
		groups:                       []info.Group{{Name: "remote-group"}},
	}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "stale gecos", cached.UserInfo.Gecos,
		"cached gecos must be preserved when the refreshed token omits it")
	require.Equal(t, "test-user@email.com", cached.UserInfo.Name,
		"user name must be re-derived from the refreshed token's claims")
	// Groups are managed separately and must be preserved as-is from the refresh.
	require.Equal(t, []info.Group{{Name: "remote-group"}}, cached.UserInfo.Groups,
		"groups must be preserved from the cached token, not overwritten by the refresh")
}

func runReturningEntraPasswordLogin(t *testing.T, provider *mockEntraPasswordProvider) (*broker.Broker, string, string) {
	t.Helper()

	const correctPassword = "password"
	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)

	return b, sessionID, access
}

// TestIsAuthenticatedPasswordEntraTokenRefreshDeniesOnVerificationFailure verifies
// that if the refreshed Entra password token fails signature verification the
// returning login is denied — mirroring the first-login deny path in
// TestIsAuthenticatedEntraMFADeniesOnAccessTokenVerificationFailure.
func TestIsAuthenticatedPasswordEntraTokenRefreshDeniesOnVerificationFailure(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) {
			return []info.Group{{Name: "remote-group"}}, nil
		}},
		refreshResult:        &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
		verifyAccessTokenErr: errors.New("token signature verification failed"),
	}

	b, sessionID, access := runReturningEntraPasswordLogin(t, provider)
	require.Equal(t, broker.AuthDenied, access,
		"a refreshed token that fails signature verification must deny the returning login")

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "new-refresh-token", cached.Token.RefreshToken,
		"a local verification failure must not discard an already-rotated refresh token")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshVerificationHasOwnTimeout verifies
// that access-token verification gets its own request timeout after a successful
// Entra password token refresh. Verification may fetch JWKS on a cold cache or
// key rotation; it must not inherit only the leftover time from the refresh call.
func TestIsAuthenticatedPasswordEntraTokenRefreshVerificationHasOwnTimeout(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const refreshDelay = 50 * time.Millisecond
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) {
			return []info.Group{{Name: "remote-group"}}, nil
		}},
		refreshDelay:  refreshDelay,
		refreshResult: &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)

	require.False(t, provider.refreshCtxDeadline.IsZero(), "refresh call should receive a deadline")
	require.False(t, provider.verifyCtxDeadline.IsZero(), "verification call should receive a deadline")
	require.GreaterOrEqual(t, provider.verifyCtxDeadline.Sub(provider.refreshCtxDeadline), refreshDelay/2,
		"verification should get a fresh timeout instead of sharing the refresh context")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshPreservesRotationOnUserInfoError
// verifies that a local failure after a successful Entra refresh still persists
// the rotated refresh token. Otherwise the cache can be stranded with a refresh
// token that Entra already invalidated server-side.
func TestIsAuthenticatedPasswordEntraTokenRefreshPreservesRotationOnUserInfoError(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) {
			return []info.Group{{Name: "remote-group"}}, nil
		}},
		refreshResult:        &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
		userInfoFromTokenErr: errors.New("missing preferred_username claim"),
	}

	b, sessionID, access := runReturningEntraPasswordLogin(t, provider)
	require.Equal(t, broker.AuthDenied, access,
		"a refreshed token whose user info cannot be extracted must deny the returning login")

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "new-refresh-token", cached.Token.RefreshToken,
		"a local user-info failure must not discard an already-rotated refresh token")
}

// TestIsAuthenticatedPasswordEntraTokenRefreshDeniesOnUsernameMismatch verifies
// that if the refreshed Entra password token's identity no longer matches the
// session's username, the returning login is denied — mirroring the username
// cross-check that the device-auth refresh path (getUserInfo) performs on every
// refresh, and that the Entra password flow itself performs on first login.
func TestIsAuthenticatedPasswordEntraTokenRefreshDeniesOnUsernameMismatch(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFunc: func() ([]info.Group, error) {
			return []info.Group{{Name: "remote-group"}}, nil
		}},
		refreshResult:       &oauth2.Token{AccessToken: "new-access-token", RefreshToken: "new-refresh-token"},
		accessTokenUserInfo: &info.User{Name: "someone-else@email.com", ProviderID: "different-user-id"},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{obtainedViaEntraPasswordAuth: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"a refreshed token whose identity no longer matches the session's username must deny the returning login")

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "new-refresh-token", cached.Token.RefreshToken,
		"a local username verification failure must not discard an already-rotated refresh token")
}

// TestDeviceAuthClearsDeviceRegistrationDataWhenRegistrationDisabled verifies that
// when register_device is changed from true to false and the user re-authenticates
// via device-code (which they are forced into because the stale device-registration
// token cannot be used for local-password auth), the new stored token has
// DeviceRegistrationData=nil. This ensures subsequent getGroups calls don't
// incorrectly attempt the PRT-exchange path.
func TestDeviceAuthClearsDeviceRegistrationDataWhenRegistrationDisabled(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                     broker.Config{DataDir: t.TempDir()},
		ownerAllowed:               true,
		firstUserBecomesOwner:      true,
		issuerURL:                  defaultIssuerURL,
		supportsDeviceRegistration: true,
		// register_device was previously true (device got registered), now disabled.
		registerDevice: false,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)

	// Seed a token from when register_device=true: it carries DeviceRegistrationData.
	// authModeIsAvailable will block the password mode (register_device=false but
	// token isForDeviceRegistration=true), forcing the user to re-authenticate via DAG.
	generateAndStoreCachedInfo(t, tokenOptions{isForDeviceRegistration: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

	// Step 1: device-code auth.
	updateAuthModes(t, b, sessionID, authmodes.DeviceQr)
	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.NewPassword}, b.GetNextAuthModes(sessionID))

	// Step 2: set a new local password, which writes the token to disk.
	updateAuthModes(t, b, sessionID, authmodes.NewPassword)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "newpassword", key))
	access, _, err = b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)

	// The newly stored token must carry no DeviceRegistrationData; otherwise the
	// next login's getGroups call would incorrectly try the PRT-exchange path.
	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Empty(t, cached.DeviceRegistrationData,
		"re-authenticating via device-code with register_device=false must store a token "+
			"without DeviceRegistrationData so subsequent group lookups don't attempt PRT exchange")
}

// TestIsAuthenticatedPhoneAppOTPRoutesToMFACode verifies that PhoneAppOTP
// (Authenticator TOTP) is routed to entra_mfa_code even when pollingInterval > 0,
// and that AcquireTokenByMFAFlow is called with poll_attempt=0 and the user's code.
func TestIsAuthenticatedPhoneAppOTPRoutesToMFACode(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Please type in the code displayed on your authenticator app from your device:",
			Method:            "PhoneAppOTP",
			PollingIntervalMs: 5000, // positive — must NOT cause poll routing
			MaxPollAttempts:   10,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — broker should recognise PhoneAppOTP and offer entra_mfa_code.
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, "{}", data, "AuthNext after password should carry no message (avoids PAM read-delay)")
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID),
		"PhoneAppOTP should route to entra_mfa_code, not entra_mfa_wait")

	// Step 2: Submit the OTP code — should call AcquireTokenByMFAFlow with poll_attempt=0.
	err = b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	layout, err := b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")
	require.Equal(t, "Enter your MFA code", layout["label"],
		"The input label should remain generic")

	otpCode := "123456"
	otpAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, otpCode, key))

	access, data, err = b.IsAuthenticated(sessionID, otpAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access)
	require.True(t, json.Valid([]byte(data)), "IsAuthenticated returned data must be valid JSON")
	require.Equal(t, []int{0}, provider.recordedPollAttempts,
		"PhoneAppOTP must call AcquireTokenByMFAFlow with poll_attempt=0")
	require.Equal(t, []string{otpCode}, provider.recordedChallengeData,
		"PhoneAppOTP must pass the user-entered code as auth_data")

	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the offline password")
	_, err = os.Stat(b.TokenPathForSession(sessionID))
	require.NoError(t, err, "Entra MFA completion should cache the refreshed token")
}

// TestIsAuthenticatedEntraMFACodeWrongCodeRetries verifies that an incorrect or
// expired one-time code keeps the MFA flow alive and re-prompts for the code
// (AuthRetry) rather than discarding the flow and forcing password re-entry. A
// subsequent correct code then completes authentication.
func TestIsAuthenticatedEntraMFACodeWrongCodeRetries(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	released := 0
	provider := &mockMFAWrongCodeThenSuccessProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    newTrackedMFAFlowState(func() { released++ }),
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:           "Please type in the code displayed on your authenticator app:",
				Method:            "PhoneAppOTP",
				PollingIntervalMs: 5000,
				MaxPollAttempts:   10,
			},
			mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — routed to entra_mfa_code (PhoneAppOTP).
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID))

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SetAvailableMode should not have returned an error")
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err, "Setup: SelectAuthenticationMode should not have returned an error")

	// Step 2: Submit a wrong code — should retry (stay on the code prompt), keep
	// the flow alive, and not yet cache the offline password.
	wrongAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "000000", key))
	access, data, err := b.IsAuthenticated(sessionID, wrongAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthRetry, access, "a wrong MFA code must return AuthRetry, not bounce back to the password step")
	require.Contains(t, data, "Incorrect or expired code", "the retry message should ask for the code again")
	require.Equal(t, 0, released, "the MFA flow must NOT be released on a retryable wrong code")
	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.Error(t, err, "no offline password should be cached after a wrong code")

	// Step 3: Submit the correct code — completes auth using the same flow.
	rightAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, _, err = b.IsAuthenticated(sessionID, rightAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "a correct code after a wrong one should grant access")
	require.Equal(t, []string{"000000", "123456"}, provider.recordedChallengeData,
		"both code submissions should reuse the same MFA flow")
	_, err = os.Stat(b.PasswordFilepathForSession(sessionID))
	require.NoError(t, err, "successful MFA completion should cache the offline password")
}

func TestIsAuthenticatedEntraMFAWaitDenialReturnsAuthDenied(t *testing.T) {
	t.Parallel()

	provider := &mockMFADeniedProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:           "Approve the sign-in request in Microsoft Authenticator",
				PollingIntervalMs: 1,
				MaxPollAttempts:   5,
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access, "password submission should transition to MFA")

	// Select the MFA wait mode.
	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	// Poll - the mock will return a denial on first poll.
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "MFA denial should return AuthDenied, not AuthRetry")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "denied")
}

func TestIsAuthenticatedEntraMFAWaitTimeoutReturnsAuthNext(t *testing.T) {
	t.Parallel()

	provider := &mockMFATimeoutProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:           "Approve the sign-in request in Microsoft Authenticator",
				PollingIntervalMs: 1,
				MaxPollAttempts:   2,
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	err = b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	// Poll - the mock always returns MFA_POLL_CONTINUE, so max attempts will be exhausted.
	// After timeout the broker should redirect back to entra_password rather than
	// asking the client to retry a dead MFA wait mode.
	access, data, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access, "MFA timeout should return AuthNext to restart from entra_password")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "timed out")
}

// TestEntraPasswordRoutesAADSTSErrors verifies that an AADSTS error raised while
// initiating the password+MFA flow is mapped to the right broker outcome.
func TestEntraPasswordRoutesAADSTSErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		aadsts             int
		category           himmelblau.MFAErrorCategory
		deviceAuthDisabled bool

		wantAccess    string
		wantNextModes []string
		wantMsg       string
	}{
		"Account_locked":                               {aadsts: 50053, wantAccess: broker.AuthDenied, wantMsg: "locked"},
		"Password_expired":                             {aadsts: 50055, wantAccess: broker.AuthDenied, wantMsg: "expired"},
		"Invalid_credentials_retry":                    {aadsts: 50126, wantAccess: broker.AuthRetry, wantMsg: "Incorrect password"},
		"Conditional_access_blocked":                   {aadsts: 53003, wantAccess: broker.AuthDenied, wantMsg: "Conditional Access"},
		"Interactive_auth_to_device":                   {aadsts: 16000, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"Interactive_auth_denied_when_device_disabled": {aadsts: 16000, deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsg: "disabled"},
		"MFA_enrollment_to_device":                     {aadsts: 50072, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"MFA_enrollment_alt_to_device":                 {aadsts: 50079, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"Authenticator_registration_to_device":         {aadsts: 50203, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA registration required"},
		"MFA_enrollment_denied_when_device_disabled":   {aadsts: 50072, deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsg: "disabled"},
		"MFA_required_to_device":                       {category: himmelblau.MFAErrorRequired, wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsg: "MFA is required"},
		"MFA_required_denied_when_device_disabled":     {category: himmelblau.MFAErrorRequired, deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsg: "disabled"},
		"Unhandled_AADSTS_denied":                      {aadsts: 99999, wantAccess: broker.AuthDenied, wantMsg: "AADSTS99999: simulated error. Please report this error"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &mockEntraPasswordProvider{
				MockProvider: &testutils.MockProvider{},
				initErr: &himmelblau.MFAError{
					AADSTS:   tc.aadsts,
					Category: tc.category,
					Message:  "simulated error",
				},
			}

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                 broker.Config{DataDir: t.TempDir()},
				ownerAllowed:           true,
				firstUserBecomesOwner:  true,
				provider:               provider,
				issuerURL:              defaultIssuerURL,
				deviceAuthFlowDisabled: tc.deviceAuthDisabled,
				// Provide a group source (device registration) so a broker with
				// device_code disabled still satisfies the entra_password
				// only-enabled-flow startup check in New().
				registerDevice: true,
			})

			sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
			updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

			passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
			access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
			require.NoError(t, err)
			require.Equal(t, tc.wantAccess, access)

			if tc.wantNextModes != nil {
				require.Equal(t, tc.wantNextModes, b.GetNextAuthModes(sessionID))
			}

			var payload struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal([]byte(data), &payload))
			require.Contains(t, payload.Message, tc.wantMsg)
		})
	}
}

func TestIsAuthenticatedPasswordDeviceRegistrationRefreshDoesNotSendClientSecretToMicrosoftBrokerApp(t *testing.T) {
	t.Parallel()

	const correctPassword = "password"
	const listenAddress = "127.0.0.1:31316"
	const serverURL = "http://" + listenAddress

	var sawBrokerAppRefresh bool
	var refreshClientSecret string
	baseTokenHandler := testutils.TokenHandler(serverURL, &testutils.TokenHandlerOptions{
		IDTokenClaims: []map[string]interface{}{
			{"aud": consts.MicrosoftBrokerAppID},
		},
	})

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                     broker.Config{DataDir: t.TempDir()},
		ownerAllowed:               true,
		firstUserBecomesOwner:      true,
		clientSecret:               "test-client-secret",
		registerDevice:             true,
		supportsDeviceRegistration: true,
		listenAddress:              listenAddress,
		customHandlers: map[string]testutils.EndpointHandler{
			"/token": func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				basicUser, basicPassword, hasBasicAuth := r.BasicAuth()
				isBrokerAppRefresh := r.FormValue("grant_type") == "refresh_token" &&
					(r.FormValue("client_id") == consts.MicrosoftBrokerAppID ||
						(hasBasicAuth && basicUser == consts.MicrosoftBrokerAppID))
				if isBrokerAppRefresh {
					sawBrokerAppRefresh = true
					refreshClientSecret = r.FormValue("client_secret")
					if refreshClientSecret == "" && hasBasicAuth {
						refreshClientSecret = basicPassword
					}
					if refreshClientSecret != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"AADSTS700025: Client is public so neither 'client_assertion' nor 'client_secret' should be presented."}`))
						return
					}
				}
				baseTokenHandler(w, r)
			},
		},
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	generateAndStoreCachedInfo(t, tokenOptions{isForDeviceRegistration: true}, b.TokenPathForSession(sessionID))
	require.NoError(t, password.HashAndStorePassword(correctPassword, b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.Password)
	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, correctPassword, key))

	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access,
		"returning device-registration logins must refresh successfully even when a client secret is configured for Graph fallback")
	require.True(t, sawBrokerAppRefresh, "the returning login must exercise the Microsoft Broker App refresh path")
	require.Empty(t, refreshClientSecret,
		"the Microsoft Broker App is a public client, so refresh must not send the configured OIDC client secret")
}

// TestEntraPasswordInvalidatesCachedCredentialsOnRemotePasswordChange verifies
// that an AADSTS50173 (grant revoked by a remote password change) wipes the
// cached token and password files and offers re-authentication.
func TestEntraPasswordInvalidatesCachedCredentialsOnRemotePasswordChange(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		initErr:      &himmelblau.MFAError{AADSTS: 50173, Message: "grant revoked"},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Seed cached credentials that the revocation must invalidate.
	cached := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	require.NoError(t, token.CacheAuthInfo(b.TokenPathForSession(sessionID), cached))
	require.NoError(t, password.HashAndStorePassword("password", b.PasswordFilepathForSession(sessionID)))

	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "changed remotely")

	require.NoFileExists(t, b.TokenPathForSession(sessionID), "cached token should be removed on remote password change")
	require.NoFileExists(t, b.PasswordFilepathForSession(sessionID), "cached password should be removed on remote password change")
}

// TestIsAuthenticatedFIDOMethodRoutesToDevice verifies that a FIDO/security-key
// MFA method redirects to the device code flow (or denies when device auth is
// unavailable), and no credentials are cached in either case.
func TestIsAuthenticatedFIDOMethodRoutesToDevice(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deviceAuthDisabled bool
		wantAccess         string
		wantNextModes      []string
		wantMsgContains    string
	}{
		"Redirects_to_device":         {wantAccess: broker.AuthNext, wantNextModes: []string{authmodes.Device, authmodes.DeviceQr}, wantMsgContains: "device code flow"},
		"Denied_when_device_disabled": {deviceAuthDisabled: true, wantAccess: broker.AuthDenied, wantMsgContains: "FIDO"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &mockEntraPasswordProvider{
				MockProvider: &testutils.MockProvider{},
				flowState:    &himmelblau.MFAFlowState{},
				challengeInfo: &himmelblau.MFAChallengeInfo{
					Message: "Use your security key",
					Method:  "FidoKey",
				},
			}

			b := newBrokerForTests(t, &brokerForTestConfig{
				Config:                 broker.Config{DataDir: t.TempDir()},
				ownerAllowed:           true,
				firstUserBecomesOwner:  true,
				provider:               provider,
				issuerURL:              defaultIssuerURL,
				deviceAuthFlowDisabled: tc.deviceAuthDisabled,
				// Provide a group source (device registration) so a broker with
				// device_code disabled still satisfies the entra_password
				// only-enabled-flow startup check in New().
				registerDevice: true,
			})

			sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
			updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

			passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
			access, data, err := b.IsAuthenticated(sessionID, passwordAuthData)
			require.NoError(t, err)
			require.Equal(t, tc.wantAccess, access)

			if tc.wantNextModes != nil {
				require.Equal(t, tc.wantNextModes, b.GetNextAuthModes(sessionID))
			}

			var payload struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal([]byte(data), &payload))
			require.Contains(t, payload.Message, tc.wantMsgContains)

			require.NoFileExists(t, b.PasswordFilepathForSession(sessionID))
		})
	}
}

// TestIsAuthenticatedEntraMFACodeDenied verifies that a denied code submission
// returns AuthDenied.
func TestIsAuthenticatedEntraMFACodeDenied(t *testing.T) {
	t.Parallel()

	provider := &mockMFADeniedProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    &himmelblau.MFAFlowState{},
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message: "Enter the code from your authenticator app",
				Method:  "PhoneAppOTP",
			},
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID))

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, data, err := b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "denied")
}

// TestIsAuthenticatedEntraMFACodeFailureRoutesBack verifies that a non-denial
// failure during code verification clears the dead MFA state and routes the
// client back to entra_password rather than the now-dead code mode.
func TestIsAuthenticatedEntraMFACodeFailureRoutesBack(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message: "Enter the code from your authenticator app",
			Method:  "PhoneAppOTP",
		},
		mfaTokenResult: nil, // AcquireTokenByMFAFlow returns a generic (non-denial) error.
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@email.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, data, err := b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraPassword}, b.GetNextAuthModes(sessionID),
		"a failed code submission should route back to entra_password")

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Contains(t, payload.Message, "failed")
}

// TestIsAuthenticatedEntraMFAFallsBackToEmailClaim verifies that when the MFA
// token carries no preferred_username, the user identity is recovered from the
// email extra instead.
func TestIsAuthenticatedEntraMFAFallsBackToEmailClaim(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message: "Enter the code from your authenticator app",
			Method:  "PhoneAppOTP",
		},
		mfaTokenResult: mfaAuthInfo.Token.WithExtra(map[string]any{
			"email": username,
			"sub":   "saved-user-id",
			"name":  "test-user",
		}),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	updateAuthModes(t, b, sessionID, authmodes.EntraMFACode)
	codeAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "123456", key))
	access, _, err = b.IsAuthenticated(sessionID, codeAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access, "email claim should satisfy the user identity when preferred_username is absent")
}

func TestUserPreCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		username        string
		allowedSuffixes []string
		homePrefix      string
	}{
		"Successfully_allow_username_with_matching_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"}},
		"Successfully_allow_username_that_matches_at_least_one_allowed_suffix": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed"},
		},
		"Successfully_allow_username_if_suffix_is_allow_all": {
			username:        "user@doesnotmatter",
			allowedSuffixes: []string{"*"},
		},
		"Successfully_allow_username_if_suffix_has_asterisk": {
			username:        "user@allowed",
			allowedSuffixes: []string{"*@allowed"},
		},
		"Successfully_allow_username_ignoring_empty_string_in_config": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@anothersuffix", "", "@allowed"},
		},
		"Return_userinfo_with_correct_homedir_after_precheck": {
			username:        "user@allowed",
			allowedSuffixes: []string{"@allowed"},
			homePrefix:      "/home/allowed/",
		},

		"Empty_userinfo_if_username_does_not_match_allowed_suffix": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@allowed"},
		},
		"Empty_userinfo_if_username_does_not_match_any_of_the_allowed_suffixes": {
			username:        "user@notallowed",
			allowedSuffixes: []string{"@other", "@something", "@allowed", ""},
		},
		"Empty_userinfo_if_no_allowed_suffixes_are_provided": {
			username: "user@allowed",
		},
		"Empty_userinfo_if_allowed_suffixes_has_only_empty_string": {
			username:        "user@allowed",
			allowedSuffixes: []string{""},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL:          defaultIssuerURL,
				homeBaseDir:        tc.homePrefix,
				allowedSSHSuffixes: tc.allowedSuffixes,
			})

			got, err := b.UserPreCheck(tc.username)
			require.NoError(t, err, "UserPreCheck should not have returned an error")

			golden.CheckOrUpdate(t, got)
		})
	}
}

func TestNormalizedIssuer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuerURL string
		want      string
	}{
		"HTTP_issuerURL":                     {issuerURL: "http://example.com", want: "example.com"},
		"HTTPS_issuerURL":                    {issuerURL: "https://example.com", want: "example.com"},
		"IssuerURL_with_path":                {issuerURL: "https://example.com/tenant/v2.0", want: "example.com_tenant_v2.0"},
		"IssuerURL_with_port":                {issuerURL: "https://example.com:8080", want: "example.com_8080"},
		"IssuerURL_with_port_and_path":       {issuerURL: "https://example.com:8080/path", want: "example.com_8080_path"},
		"IssuerURL_with_IP_address":          {issuerURL: "https://127.0.0.1", want: "127.0.0.1"},
		"IssuerURL_with_IP_address_and_port": {issuerURL: "https://127.0.0.1:8080", want: "127.0.0.1_8080"},
		"IssuerURL_without_scheme":           {issuerURL: "example.com", want: "example.com"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: tc.issuerURL,
			})

			got := b.NormalizedIssuer(tc.issuerURL)
			require.Equal(t, tc.want, got, "NormalizedIssuer returned unexpected result")
		})
	}
}

func TestUserDataDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		issuerURL string
		username  string
		want      string
		wantErr   bool
	}{
		"Successfully_return_user_data_dir_for_simple_username_and_issuer": {
			issuerURL: "https://example.com",
			username:  "user@example.com",
			want:      "example.com/user@example.com",
		},
		"Successfully_return_user_data_dir_for_issuer_url_without_scheme": {
			issuerURL: "example.com",
			username:  "user@example.com",
			want:      "example.com/user@example.com",
		},
		"Error_when_username_is_empty": {
			issuerURL: "https://example.com",
			username:  "",
			wantErr:   true,
		},
		"Error_when_username_contains_path_traversal": {
			issuerURL: "https://example.com",
			username:  "../test",
			wantErr:   true,
		},
		"Error_when_username_contains_path_traversal_but_does_not_leave_the_parent_directory": {
			issuerURL: "https://example.com",
			username:  "test/../other-user",
			wantErr:   true,
		},
		"Error_when_issuer_contains_path_traversal": {
			issuerURL: "https://..",
			username:  "validuser",
			wantErr:   true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: tc.issuerURL,
			})

			got, err := b.UserDataDir(tc.username)
			if tc.wantErr {
				require.Error(t, err, "UserDataDir should return an error, but did not")
				return
			}
			require.NoError(t, err, "UserDataDir should not return an error")
			require.Equal(t, filepath.Join(b.DataDir(), tc.want), got, "UserDataDir returned unexpected result")
		})
	}
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	const providerID = "provider-id-123"

	tests := map[string]struct {
		username   string
		providerID string

		createUserDir       bool
		createProviderIDDir bool
		// usernameIsSymlink makes the username path a compatibility symlink pointing
		// to the provider ID-keyed directory, as created by the cache migration.
		usernameIsSymlink bool
		readOnlyDataDir   bool

		wantErr bool
	}{
		"Successfully_delete_existing_user":        {username: "user@example.com", createUserDir: true},
		"Successfully_delete_unknown_user_is_noop": {username: "unknown@example.com"},

		// Deleting by username when the username path is a compatibility symlink
		// created by the cache migration must also remove the provider ID-keyed
		// target, otherwise the token and password would be left behind on disk.
		"Successfully_delete_symlinked_user_and_provider_ID_target": {
			username: "user@example.com", usernameIsSymlink: true, createProviderIDDir: true,
		},
		"Successfully_delete_by_username_and_provider_ID": {
			username: "user@example.com", providerID: providerID, usernameIsSymlink: true, createProviderIDDir: true,
		},
		"Successfully_delete_by_provider_ID_only": {
			providerID: providerID, createProviderIDDir: true,
		},

		"Error_when_user_data_dir_cannot_be_removed":     {username: "user@example.com", createUserDir: true, readOnlyDataDir: true, wantErr: true},
		"Error_when_userDataDir_could_not_be_determined": {username: "", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{
				issuerURL: defaultIssuerURL,
			})

			// providerIDDir is always derived from the constant provider ID: the
			// on-disk cache directory exists regardless of whether the provider ID is
			// passed to DeleteUser. Creating it first also creates the shared issuer
			// directory needed to place the username symlink.
			providerIDDir, err := b.UserDataDir(providerID)
			require.NoError(t, err, "Setup: UserDataDir for provider ID should not have returned an error")

			// Derive the path where DeleteUser will look for the user's data.
			userDataDir, errUser := b.UserDataDir(tc.username)

			// An empty username (and no provider ID) only verifies that the data dir
			// path could not be derived.
			if tc.username == "" && tc.providerID == "" {
				require.Error(t, errUser, "Setup: UserDataDir should have returned an error for empty username")
				return
			}
			if tc.username != "" {
				require.NoError(t, errUser, "Setup: UserDataDir should not have returned an error for valid username")
			}

			if tc.createUserDir {
				err := os.MkdirAll(userDataDir, 0700)
				require.NoError(t, err, "Setup: could not create user data directory")

				// Write a dummy token file so the directory is non-empty
				err = os.WriteFile(filepath.Join(userDataDir, "token.json"), []byte(`{}`), 0600)
				require.NoError(t, err, "Setup: could not write dummy token file")
			}

			if tc.createProviderIDDir {
				// Create the real provider ID-keyed directory with cached data.
				generateAndStoreCachedInfo(t, tokenOptions{}, filepath.Join(providerIDDir, "token.json"))
				err := os.WriteFile(filepath.Join(providerIDDir, "password"), []byte("hashed"), 0600)
				require.NoError(t, err, "Setup: could not write dummy password file")
			}

			if tc.usernameIsSymlink {
				// Reproduce the compatibility symlink left behind by the cache migration.
				err := os.Symlink(providerIDDir, userDataDir)
				require.NoError(t, err, "Setup: could not create compatibility symlink")
			}

			if tc.readOnlyDataDir {
				// Make the issuer directory read-only so RemoveAll fails on the user subdir
				issuerDir := filepath.Dir(userDataDir)
				err := os.Chmod(issuerDir, 0500) //nolint:gosec // Intentional read-only permission for testing
				require.NoError(t, err, "Setup: could not make issuer directory read-only")
				t.Cleanup(func() { _ = os.Chmod(issuerDir, 0700) }) //nolint:gosec // Restore full permissions after test
			}

			err = b.DeleteUser(tc.username, tc.providerID)
			if tc.wantErr {
				require.Error(t, err, "DeleteUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "DeleteUser should not return an error, but did")

			if tc.username != "" {
				// Verify the user data directory (or compatibility symlink) no longer exists.
				_, lstatErr := os.Lstat(userDataDir)
				require.ErrorIs(t, lstatErr, os.ErrNotExist, "User data path should have been removed")
			}
			if tc.providerID != "" || tc.usernameIsSymlink {
				// The provider ID-keyed directory (token + password) must be gone too.
				require.NoDirExists(t, providerIDDir, "Provider ID directory should have been removed")
			}
		})
	}
}

func TestDeleteUserDoesNotRemoveMismatchedSymlinkTarget(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

	const (
		username        = "user@example.com"
		providerID      = "provider-id-123"
		otherProviderID = "provider-id-other"
	)

	userDataDir, err := b.UserDataDir(username)
	require.NoError(t, err, "Setup: deriving the username data dir should not fail")
	providerIDDir, err := b.UserDataDir(providerID)
	require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")
	otherProviderIDDir, err := b.UserDataDir(otherProviderID)
	require.NoError(t, err, "Setup: deriving the other provider ID data dir should not fail")

	require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating provider ID cache dir should not fail")
	require.NoError(t, os.WriteFile(filepath.Join(providerIDDir, "token.json"), []byte("delete-me"), 0600),
		"Setup: writing provider ID token should not fail")
	require.NoError(t, os.MkdirAll(otherProviderIDDir, 0700), "Setup: creating other provider ID cache dir should not fail")
	require.NoError(t, os.WriteFile(filepath.Join(otherProviderIDDir, "token.json"), []byte("keep-me"), 0600),
		"Setup: writing other provider ID token should not fail")
	require.NoError(t, os.Symlink(otherProviderIDDir, userDataDir), "Setup: creating mismatched compatibility symlink should not fail")

	err = b.DeleteUser(username, providerID)
	require.NoError(t, err, "DeleteUser should not fail on a mismatched compatibility symlink")

	_, err = os.Lstat(userDataDir)
	require.ErrorIs(t, err, os.ErrNotExist, "The stale username symlink should be removed")
	require.NoDirExists(t, providerIDDir, "The requested provider ID cache dir should be removed")
	require.DirExists(t, otherProviderIDDir, "The unrelated provider ID cache dir should be preserved")
	gotToken, err := os.ReadFile(filepath.Join(otherProviderIDDir, "token.json"))
	require.NoError(t, err, "Reading the preserved provider ID token should not fail")
	require.Equal(t, "keep-me", string(gotToken), "The unrelated provider ID cache should not be modified")
	requireIssuerCacheTree(t, filepath.Dir(userDataDir), map[string]string{
		"provider-id-other":            "dir",
		"provider-id-other/token.json": "file",
	})
}

// runDeviceAuthAndNewPassword drives a full online device-auth followed by the
// newpassword step for the given session, the same two IsAuthenticated calls the
// PAM flow performs. It returns the access result of each call.
func runDeviceAuthAndNewPassword(t *testing.T, b *broker.Broker, sessionID, key, newPassword string) (deviceAuthAccess, newPasswordAccess string) {
	t.Helper()

	updateAuthModes(t, b, sessionID, authmodes.DeviceQr)
	deviceAuthAccess, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err, "Device auth IsAuthenticated should not error")

	updateAuthModes(t, b, sessionID, authmodes.NewPassword)
	secret := encryptSecret(t, newPassword, key)
	newPasswordData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, secret)
	newPasswordAccess, _, err = b.IsAuthenticated(sessionID, newPasswordData)
	require.NoError(t, err, "New password IsAuthenticated should not error")

	return deviceAuthAccess, newPasswordAccess
}

// TestDeviceAuthRedirectsToExistingProviderIDDir covers the case where the broker is
// updated but authd is not (so NewSession receives no provider ID) and the provider
// ID-keyed cache directory already exists from a previous login, but the current
// username path does not yet resolve to it. This is what an email change at the IdP
// looks like to the broker: it only sees the current username plus the provider ID it
// learns online. The provider ID is learned during the online device auth, so the
// redirect to the existing directory must happen there, before device registration and
// the new password are written. The session must end up using the existing provider ID
// directory (preserving the cached token, including any device registration data), with
// a compatibility symlink left at the username path.
//
// The mock provider's live ID token is fixed to email "test-user@email.com" and sub
// "test-user-id", and VerifyUsername requires the session username to match that email,
// so the session uses that username; the pre-existing provider ID directory standing in
// for the prior login is what exercises the redirect.
func TestDeviceAuthRedirectsToExistingProviderIDDir(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		issuerURL:             defaultIssuerURL,
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
	})

	const (
		username   = "test-user@email.com"
		providerID = "test-user-id"
	)
	providerIDDir, err := b.UserDataDir(providerID)
	require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")
	usernameDir, err := b.UserDataDir(username)
	require.NoError(t, err, "Setup: deriving the username data dir should not fail")

	// Simulate the cache left behind by the previous login: the provider ID directory
	// already exists and holds a cached token.
	require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating the existing provider ID dir should not fail")
	require.NoError(t, os.WriteFile(filepath.Join(providerIDDir, "token.json"), []byte("previous-login-token"), 0600),
		"Setup: writing the existing provider ID token should not fail")

	// authd has not been updated, so it does not pass a provider ID to NewSession, and
	// the username path does not resolve to the provider ID dir yet.
	sessionID, key, err := b.NewSession(username, "some lang", sessionmode.Login, "")
	require.NoError(t, err, "NewSession should not fail")
	require.Equal(t, usernameDir, b.UserDataDirForSession(sessionID),
		"Before device auth the session should still resolve to the username dir")

	deviceAccess, newPasswordAccess := runDeviceAuthAndNewPassword(t, b, sessionID, key, "brand-new-password")
	require.Equal(t, broker.AuthNext, deviceAccess, "Device auth should advance to the new password step")
	require.Equal(t, broker.AuthGranted, newPasswordAccess, "New password step should grant access")

	// The session must have been redirected to the pre-existing provider ID directory,
	// so the new password overwrites the password in that directory rather than creating
	// a fresh username-keyed cache directory.
	require.Equal(t, providerIDDir, b.UserDataDirForSession(sessionID),
		"The session should use the existing provider ID cache dir after device auth")

	// The freshly re-acquired token and the new password must have been written into the
	// existing provider ID directory, not into a separate username-keyed directory. The
	// redirect happens before deviceAuth reads the previous token for its device
	// registration data, so the device is not re-registered as a side effect.
	_, err = os.Stat(filepath.Join(providerIDDir, "token.json"))
	require.NoError(t, err, "The refreshed token should be in the provider ID dir")
	_, err = os.Stat(filepath.Join(providerIDDir, "password"))
	require.NoError(t, err, "The new password should be in the provider ID dir")

	requireIssuerCacheTree(t, filepath.Dir(providerIDDir), map[string]string{
		"test-user-id":            "dir",
		"test-user-id/token.json": "file",
		"test-user-id/password":   "file",
		"test-user@email.com":     "symlink -> test-user-id",
	})
}

// TestOfflineLoginCacheDirectoryResolution covers how the cache directory is resolved
// when the first login after the broker update happens while offline. The provider ID
// can only be learned online (from a token refresh), so:
//   - an already-migrated cache is still resolved through its compatibility symlink;
//   - a legacy directory whose cached token already carries a provider ID is migrated
//     even offline (the migration happens in NewSession, not gated on being online);
//   - a legacy directory whose cached token has no provider ID stays put until the next
//     online login.
func TestOfflineLoginCacheDirectoryResolution(t *testing.T) {
	t.Parallel()

	const (
		username   = "user@example.com"
		providerID = "saved-user-id"
	)

	tests := map[string]struct {
		// alreadyMigrated sets up a provider ID dir plus a compatibility symlink at the
		// username path (an already-migrated cache).
		alreadyMigrated bool
		// legacyDirWithProviderID sets up a real username dir whose cached token carries
		// a provider ID (migratable even offline).
		legacyDirWithProviderID bool
		// legacyDirWithoutProviderID sets up a real username dir whose cached token has no
		// provider ID (not migratable offline).
		legacyDirWithoutProviderID bool

		wantSessionDirIsProviderIDDir bool
		wantIssuerTree                map[string]string
	}{
		"Already_migrated_cache_stays_on_provider_ID_dir": {
			alreadyMigrated:               true,
			wantSessionDirIsProviderIDDir: true,
			wantIssuerTree: map[string]string{
				"saved-user-id":            "dir",
				"saved-user-id/token.json": "file",
				"user@example.com":         "symlink -> saved-user-id",
			},
		},
		"Legacy_cache_with_provider_ID_migrates_offline": {
			legacyDirWithProviderID:       true,
			wantSessionDirIsProviderIDDir: true,
			wantIssuerTree: map[string]string{
				"saved-user-id":            "dir",
				"saved-user-id/token.json": "file",
				"user@example.com":         "symlink -> saved-user-id",
			},
		},
		"Legacy_cache_without_provider_ID_stays_on_username_dir": {
			legacyDirWithoutProviderID:    true,
			wantSessionDirIsProviderIDDir: false,
			wantIssuerTree: map[string]string{
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Force offline mode by making OIDC discovery unreachable.
			b := newBrokerForTests(t, &brokerForTestConfig{
				customHandlers: map[string]testutils.EndpointHandler{
					"/.well-known/openid-configuration": testutils.UnavailableHandler(),
				},
			})

			usernameDir, err := b.UserDataDir(username)
			require.NoError(t, err, "Setup: deriving the username data dir should not fail")
			providerIDDir, err := b.UserDataDir(providerID)
			require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")
			require.NoError(t, os.MkdirAll(filepath.Dir(usernameDir), 0700), "Setup: creating the issuer dir should not fail")

			switch {
			case tc.alreadyMigrated:
				require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating the provider ID dir")
				generateAndStoreCachedInfo(t, tokenOptions{}, filepath.Join(providerIDDir, "token.json"))
				require.NoError(t, os.Symlink(providerIDDir, usernameDir), "Setup: creating the compatibility symlink")
			case tc.legacyDirWithProviderID:
				require.NoError(t, os.MkdirAll(usernameDir, 0700), "Setup: creating the legacy username dir")
				// generateCachedInfo stores a token whose ProviderID is "saved-user-id".
				generateAndStoreCachedInfo(t, tokenOptions{}, filepath.Join(usernameDir, "token.json"))
			case tc.legacyDirWithoutProviderID:
				require.NoError(t, os.MkdirAll(usernameDir, 0700), "Setup: creating the legacy username dir")
				// A token without a provider ID cannot be migrated offline.
				require.NoError(t, os.WriteFile(filepath.Join(usernameDir, "token.json"), []byte("{}"), 0600),
					"Setup: writing the legacy token without a provider ID")
			}

			sessionID, _, err := b.NewSession(username, "some lang", sessionmode.Login, "")
			require.NoError(t, err, "NewSession should not fail offline")

			gotOffline, err := b.IsOffline(sessionID)
			require.NoError(t, err, "IsOffline should not error")
			require.True(t, gotOffline, "The session should be offline")

			if tc.wantSessionDirIsProviderIDDir {
				require.Equal(t, providerIDDir, b.UserDataDirForSession(sessionID),
					"The session should resolve to the provider ID dir")
			} else {
				require.Equal(t, usernameDir, b.UserDataDirForSession(sessionID),
					"The session should stay on the username dir")
			}

			requireIssuerCacheTree(t, filepath.Dir(usernameDir), tc.wantIssuerTree)
		})
	}
}

// TestEnsureProviderIDCacheDir exercises the cache migration helper that moves a
// session's on-disk cache from a username-based directory to a provider ID-based
// one. It covers the early-return guards (invalid/empty provider ID, already
// migrated, unreadable provider ID directory), the redirect-to-existing-directory
// path (including when the compatibility symlink already exists or cannot be
// created) and the rename-based migration path (success and failures).
func TestEnsureProviderIDCacheDir(t *testing.T) {
	t.Parallel()

	const (
		username   = "user@example.com"
		providerID = "provider-id-123"
	)

	tests := map[string]struct {
		// providerIDOverride replaces the provider ID passed to the function.
		// Use "-" to pass an empty provider ID.
		providerIDOverride string
		// currentDirIsProviderIDDir makes the session's current data dir already
		// point to the provider ID directory, reproducing an already-migrated session.
		currentDirIsProviderIDDir bool

		createProviderIDDir bool
		createUsernameDir   bool
		usernameIsSymlink   bool
		// readOnlyIssuerDir denies writes (but allows traversal) on the issuer dir,
		// so symlink creation and rename fail.
		readOnlyIssuerDir bool
		// noExecIssuerDir denies traversal on the issuer dir, so the provider ID
		// directory existence check fails.
		noExecIssuerDir        bool
		providerIDTokenContent string

		wantProviderIDSet          bool
		wantDataDirIsProviderIDDir bool
		wantSymlinkAtUsername      bool
		wantProviderIDDirExists    bool
		wantUsernameDirExists      bool
		wantProviderIDTokenContent string
		wantIssuerTree             map[string]string
	}{
		"No_op_when_provider_ID_is_empty": {
			providerIDOverride:    "-",
			createUsernameDir:     true,
			wantUsernameDirExists: true,
			wantIssuerTree: map[string]string{
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},
		"No_op_when_provider_ID_contains_path_traversal": {
			providerIDOverride:    "../escape",
			createUsernameDir:     true,
			wantUsernameDirExists: true,
			wantIssuerTree: map[string]string{
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},
		"No_op_when_session_is_already_on_the_provider_ID_directory": {
			currentDirIsProviderIDDir:  true,
			createProviderIDDir:        true,
			wantDataDirIsProviderIDDir: true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
			},
		},
		"No_op_when_provider_ID_directory_status_cannot_be_determined": {
			createUsernameDir:     true,
			noExecIssuerDir:       true,
			wantUsernameDirExists: true,
			wantIssuerTree: map[string]string{
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},

		"Redirects_to_existing_provider_ID_directory_with_a_compatibility_symlink": {
			createProviderIDDir:        true,
			wantProviderIDSet:          true,
			wantDataDirIsProviderIDDir: true,
			wantSymlinkAtUsername:      true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
				"user@example.com":           "symlink -> provider-id-123",
			},
		},
		"Redirects_to_existing_provider_ID_directory_when_symlink_already_exists": {
			createProviderIDDir:        true,
			usernameIsSymlink:          true,
			wantProviderIDSet:          true,
			wantDataDirIsProviderIDDir: true,
			wantSymlinkAtUsername:      true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
				"user@example.com":           "symlink -> provider-id-123",
			},
		},
		"Redirects_to_existing_provider_ID_directory_by_consolidating_username_directory": {
			createProviderIDDir:        true,
			createUsernameDir:          true,
			wantProviderIDSet:          true,
			wantDataDirIsProviderIDDir: true,
			wantSymlinkAtUsername:      true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
				"user@example.com":           "symlink -> provider-id-123",
			},
		},
		"Does_not_consolidate_when_existing_provider_ID_cache_differs": {
			createProviderIDDir:        true,
			providerIDTokenContent:     "different-token-marker",
			createUsernameDir:          true,
			wantProviderIDDirExists:    true,
			wantUsernameDirExists:      true,
			wantProviderIDTokenContent: "different-token-marker",
			wantIssuerTree: map[string]string{
				"provider-id-123":             "dir",
				"provider-id-123/token.json":  "file",
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},
		"Redirects_to_existing_provider_ID_directory_even_when_symlink_cannot_be_created": {
			createProviderIDDir:     true,
			readOnlyIssuerDir:       true,
			wantProviderIDDirExists: true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
			},
		},

		"Migrates_username_directory_to_provider_ID_directory": {
			createUsernameDir:          true,
			wantProviderIDSet:          true,
			wantDataDirIsProviderIDDir: true,
			wantSymlinkAtUsername:      true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":            "dir",
				"provider-id-123/token.json": "file",
				"user@example.com":           "symlink -> provider-id-123",
			},
		},
		"No_op_when_username_directory_does_not_exist": {
			// Nothing exists on disk yet, so create the provider ID cache directory before
			// the first token/password write and keep a username compatibility symlink.
			wantProviderIDSet:          true,
			wantDataDirIsProviderIDDir: true,
			wantSymlinkAtUsername:      true,
			wantProviderIDDirExists:    true,
			wantIssuerTree: map[string]string{
				"provider-id-123":  "dir",
				"user@example.com": "symlink -> provider-id-123",
			},
		},
		"Does_not_migrate_when_rename_fails": {
			createUsernameDir:     true,
			readOnlyIssuerDir:     true,
			wantUsernameDirExists: true,
			wantIssuerTree: map[string]string{
				"user@example.com":            "dir",
				"user@example.com/token.json": "file",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

			providerIDArg := providerID
			switch tc.providerIDOverride {
			case "":
			case "-":
				providerIDArg = ""
			default:
				providerIDArg = tc.providerIDOverride
			}

			usernameDir, err := b.UserDataDir(username)
			require.NoError(t, err, "Setup: deriving the username data dir should not fail")
			// providerIDDir is always derived from the constant provider ID so that
			// assertions have a stable path even for the invalid-provider-ID cases.
			providerIDDir, err := b.UserDataDir(providerID)
			require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")

			issuerDir := filepath.Dir(usernameDir)
			require.NoError(t, os.MkdirAll(issuerDir, 0700), "Setup: creating the issuer dir should not fail")

			const tokenContent = "cached-token-marker"
			if tc.createProviderIDDir {
				providerIDTokenContent := tokenContent
				if tc.providerIDTokenContent != "" {
					providerIDTokenContent = tc.providerIDTokenContent
				}
				require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating the provider ID dir")
				require.NoError(t, os.WriteFile(filepath.Join(providerIDDir, "token.json"), []byte(providerIDTokenContent), 0600),
					"Setup: writing the provider ID token file")
			}
			if tc.createUsernameDir {
				require.NoError(t, os.MkdirAll(usernameDir, 0700), "Setup: creating the username dir")
				require.NoError(t, os.WriteFile(filepath.Join(usernameDir, "token.json"), []byte(tokenContent), 0600),
					"Setup: writing the username token file")
			}
			if tc.usernameIsSymlink {
				require.NoError(t, os.Symlink(providerIDDir, usernameDir), "Setup: creating the compatibility symlink")
			}

			currentDataDir := usernameDir
			if tc.currentDirIsProviderIDDir {
				currentDataDir = providerIDDir
			}

			// Restrict the issuer dir permissions to exercise the failure branches,
			// restoring them right after the call so that the assertions (and the
			// TempDir cleanup) can read the tree again.
			restorePerms := func() {}
			switch {
			case tc.readOnlyIssuerDir:
				require.NoError(t, os.Chmod(issuerDir, 0500), "Setup: making the issuer dir read-only") //nolint:gosec // Intentional for testing.
				restorePerms = func() { _ = os.Chmod(issuerDir, 0700) }                                 //nolint:gosec // Restore after testing.
			case tc.noExecIssuerDir:
				require.NoError(t, os.Chmod(issuerDir, 0000), "Setup: making the issuer dir non-traversable")
				restorePerms = func() { _ = os.Chmod(issuerDir, 0700) } //nolint:gosec // Restore after testing.
			}
			t.Cleanup(restorePerms)

			got := b.EnsureProviderIDCacheDir(username, currentDataDir, providerIDArg)
			restorePerms()

			if tc.wantProviderIDSet {
				require.Equal(t, providerID, got.ProviderID, "Provider ID should have been set on the session")
			} else {
				require.Empty(t, got.ProviderID, "Provider ID should not have been set on the session")
			}

			wantDataDir := currentDataDir
			if tc.wantDataDirIsProviderIDDir {
				wantDataDir = providerIDDir
			}
			require.Equal(t, wantDataDir, got.UserDataDir, "Session data dir does not match the expected one")
			require.Equal(t, filepath.Join(wantDataDir, "token.json"), got.TokenPath, "Session token path does not match")
			require.Equal(t, filepath.Join(wantDataDir, "password"), got.PasswordPath, "Session password path does not match")

			if tc.wantProviderIDDirExists {
				require.DirExists(t, providerIDDir, "Provider ID directory should exist")
				if tc.createProviderIDDir || tc.createUsernameDir {
					wantProviderIDTokenContent := tokenContent
					if tc.wantProviderIDTokenContent != "" {
						wantProviderIDTokenContent = tc.wantProviderIDTokenContent
					}
					// The cached token must be reachable through the provider ID dir.
					gotToken, err := os.ReadFile(filepath.Join(providerIDDir, "token.json"))
					require.NoError(t, err, "Reading the provider ID token file should not fail")
					require.Equal(t, wantProviderIDTokenContent, string(gotToken), "The cached token should have been preserved")
				}
			} else {
				require.NoDirExists(t, providerIDDir, "Provider ID directory should not exist")
			}

			if tc.wantSymlinkAtUsername {
				info, err := os.Lstat(usernameDir)
				require.NoError(t, err, "The username path should exist")
				require.NotZero(t, info.Mode()&os.ModeSymlink, "The username path should be a compatibility symlink")
				rawLink, err := os.Readlink(usernameDir)
				require.NoError(t, err, "os.Readlink on the compatibility symlink should not fail")
				require.False(t, filepath.IsAbs(rawLink), "compatibility symlink should be stored as a relative path, got %q", rawLink)
				target, err := filepath.EvalSymlinks(usernameDir)
				require.NoError(t, err, "The compatibility symlink should resolve")
				require.Equal(t, providerIDDir, target, "The compatibility symlink should point to the provider ID dir")
			}

			if tc.wantUsernameDirExists {
				info, err := os.Lstat(usernameDir)
				require.NoError(t, err, "The username path should still exist")
				require.Zero(t, info.Mode()&os.ModeSymlink, "The username path should still be a real directory")
			}

			if tc.wantIssuerTree != nil {
				requireIssuerCacheTree(t, issuerDir, tc.wantIssuerTree)
			}
		})
	}
}

// TestCompatibilitySymlinkSurvivesIssuerTreeMove verifies that the compatibility
// symlink created by ensureCompatibilitySymlink is stored as a relative path and
// therefore continues to resolve correctly after the entire issuer cache tree is
// moved (e.g. during a snap revision bump that renames the data directory prefix).
func TestCompatibilitySymlinkSurvivesIssuerTreeMove(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{issuerURL: defaultIssuerURL})

	const (
		username   = "user@example.com"
		providerID = "provider-id-123"
	)

	usernameDir, err := b.UserDataDir(username)
	require.NoError(t, err, "Setup: deriving the username data dir should not fail")
	providerIDDir, err := b.UserDataDir(providerID)
	require.NoError(t, err, "Setup: deriving the provider ID data dir should not fail")

	issuerDir := filepath.Dir(usernameDir)
	require.NoError(t, os.MkdirAll(providerIDDir, 0700), "Setup: creating the provider ID dir")
	require.NoError(t, os.WriteFile(filepath.Join(providerIDDir, "token.json"), []byte("cached-token"), 0600),
		"Setup: writing the provider ID token file")

	got := b.EnsureProviderIDCacheDir(username, usernameDir, providerID)
	require.Equal(t, providerIDDir, got.UserDataDir, "Session should use the provider ID cache dir")

	// The raw symlink value must be relative so it is not tied to the current path prefix.
	rawLink, err := os.Readlink(usernameDir)
	require.NoError(t, err, "os.Readlink on the compatibility symlink should not fail")
	require.False(t, filepath.IsAbs(rawLink), "compatibility symlink should be stored as a relative path, got %q", rawLink)

	// Simulate a snap revision bump by moving the entire issuer tree to a new prefix.
	newIssuerDir := filepath.Join(t.TempDir(), "new-revision", filepath.Base(issuerDir))
	require.NoError(t, os.MkdirAll(filepath.Dir(newIssuerDir), 0700), "Setup: creating parent of new issuer dir")
	require.NoError(t, os.Rename(issuerDir, newIssuerDir), "Moving the issuer tree should not fail")

	newUsernameDir := filepath.Join(newIssuerDir, username)
	resolvedTarget, err := filepath.EvalSymlinks(newUsernameDir)
	require.NoError(t, err, "compatibility symlink should still resolve after the issuer tree is moved")
	require.Equal(t, filepath.Join(newIssuerDir, providerID), resolvedTarget,
		"compatibility symlink should point to the provider ID dir in the new location")
}

func TestIsFIDOMethod(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
		want   bool
	}{
		"Empty":          {method: "", want: false},
		"Fido_lower":     {method: "fido", want: true},
		"Fido_upper":     {method: "FIDO", want: true},
		"FidoKey":        {method: "FidoKey", want: true},
		"Fido2_token":    {method: "FIDO2_token", want: true},
		"Webauthn_lower": {method: "webauthn", want: true},
		"WebAuthn_camel": {method: "WebAuthn", want: true},
		"Security_key":   {method: "security_key", want: true},
		"PhoneAppOTP":    {method: "PhoneAppOTP", want: false},
		"PhoneAppPush":   {method: "PhoneAppNotification", want: false},
		"OneWaySMS":      {method: "OneWaySMS", want: false},
		"Random":         {method: "AnythingElse", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, broker.IsFIDOMethod(tc.method))
		})
	}
}

func TestIsPromptMethod(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
		want   bool
	}{
		"AccessPass":            {method: "AccessPass", want: true},
		"PhoneAppOTP":           {method: "PhoneAppOTP", want: true},
		"OneWaySMS":             {method: "OneWaySMS", want: true},
		"ConsolidatedTelephony": {method: "ConsolidatedTelephony", want: true},
		"PhoneAppNotification":  {method: "PhoneAppNotification", want: false},
		"CompanionApps":         {method: "CompanionAppsNotification", want: false},
		"FidoKey":               {method: "FidoKey", want: false},
		"Empty":                 {method: "", want: false},
		"Lowercase_no_match":    {method: "phoneappotp", want: false},
		"Unknown":               {method: "SomeFutureMethod", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, broker.IsPromptMethod(tc.method))
		})
	}
}

// TestEntraPasswordAuthProviderNotSupported verifies that entraPasswordAuth
// returns AuthDenied when the broker's provider does not implement
// EntraPasswordProvider (defensive guard against misconfiguration).
func TestEntraPasswordAuthProviderNotSupported(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		issuerURL:             defaultIssuerURL,
		// Default MockProvider — does NOT implement EntraPasswordProvider.
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	// Force the session into entra_password mode without going through the
	// normal availability check (which would reject a provider that lacks support).
	err := b.SetAvailableMode(sessionID, authmodes.EntraPassword)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraPassword)
	require.NoError(t, err)

	// Empty auth data (no secret) is fine: ProviderAs check fires before any
	// password is consumed.
	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_password with unsupported provider must deny")
}

// TestEntraPasswordAuthNonMFAError verifies that a non-MFAError from
// InitiateEntraPasswordAuth (e.g. a network failure) returns AuthDenied.
func TestEntraPasswordAuthNonMFAError(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		initErr:      errors.New("simulated network failure"),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "non-MFAError from InitiateEntraPasswordAuth must deny")
}

// TestEntraPasswordAuthNilFlowOrChallenge verifies that a nil flow/challenge
// returned by InitiateEntraPasswordAuth (provider contract violation) returns
// AuthDenied.
func TestEntraPasswordAuthNilFlowOrChallenge(t *testing.T) {
	t.Parallel()

	// initErr is nil but both flowState and challengeInfo are nil (default zero values).
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		// flowState and challengeInfo left nil.
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)

	authData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, authData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "nil flow/challenge from provider must deny")
}

// TestEntraMFAWaitAuthProviderNotSupported verifies that entraMFAWaitAuth
// returns AuthDenied when the broker's provider does not implement
// EntraPasswordProvider.
func TestEntraMFAWaitAuthProviderNotSupported(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	err := b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_mfa_wait with unsupported provider must deny")
}

// TestEntraMFAWaitAuthNoActiveMFAFlow verifies that entraMFAWaitAuth returns
// AuthDenied when the session has no active MFA flow (the password step was
// never completed).
func TestEntraMFAWaitAuthNoActiveMFAFlow(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{},
		flowState:     &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{PollingIntervalMs: 1, MaxPollAttempts: 1},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	// Jump straight to entra_mfa_wait without running entra_password first,
	// so session.mfaFlowActive remains nil.
	err := b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_mfa_wait with no active MFA flow must deny")
}

// TestEntraMFAWaitAuthNoChallengeMeta verifies that entraMFAWaitAuth returns
// AuthDenied when the session has an active MFA flow but no challenge metadata
// (another provider contract violation guard).
func TestEntraMFAWaitAuthNoChallengeMeta(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{},
		flowState:     &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{PollingIntervalMs: 1, MaxPollAttempts: 1},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	err := b.SetAvailableMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	// Set only the flow; leave mfaChallengeInfo nil to exercise the guard.
	err = b.SetSessionMFAFlowActive(sessionID, &himmelblau.MFAFlowState{})
	require.NoError(t, err)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_mfa_wait with nil challenge metadata must deny")
}

// TestEntraMFACodeAuthProviderNotSupported verifies that entraMFACodeAuth
// returns AuthDenied when the provider does not implement EntraPasswordProvider.
func TestEntraMFACodeAuthProviderNotSupported(t *testing.T) {
	t.Parallel()

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	err := b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_mfa_code with unsupported provider must deny")
}

// TestEntraMFACodeAuthNoActiveMFAFlow verifies that entraMFACodeAuth returns
// AuthDenied when the session has no active MFA flow.
func TestEntraMFACodeAuthNoActiveMFAFlow(t *testing.T) {
	t.Parallel()

	provider := &mockEntraPasswordProvider{
		MockProvider:  &testutils.MockProvider{},
		flowState:     &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, _ := newSessionForTests(t, b, "test-user@example.com", sessionmode.Login)

	// Jump straight to entra_mfa_code without running entra_password first.
	err := b.SetAvailableMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err)
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "entra_mfa_code with no active MFA flow must deny")
}

// TestIsAuthenticatedEntraMFAUsesVerifiedAccessTokenIdentity verifies that
// first-login identity comes from UserInfoFromAccessToken after VerifyAccessToken,
// not from OAuth token extras that may have been sourced from an unverified
// id_token by libhimmelblau.
func TestIsAuthenticatedEntraMFAUsesVerifiedAccessTokenIdentity(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		accessTokenUserInfo: &info.User{Name: username, ProviderID: "verified-access-token-user-id"},
		mfaTokenResult: mfaAuthInfo.Token.WithExtra(map[string]any{
			"preferred_username": "someone-else@email.com",
			"sub":                "unverified-id-token-user-id",
			"name":               "Someone Else",
		}),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthGranted, access,
		"a first-login MFA token must bind identity to the verified access-token claims, not token extras")

	cached, err := token.LoadAuthInfo(b.TokenPathForSession(sessionID))
	require.NoError(t, err)
	require.Equal(t, "verified-access-token-user-id", cached.UserInfo.ProviderID)
}

// TestIsAuthenticatedEntraMFADeniesOnUsernameMismatch verifies the first-login
// identity cross-check: when the verified MFA access token identity does not
// match the username the user authenticated as, VerifyUsername fails and the
// login is denied. This is the first-login counterpart to the refresh-path
// mismatch test (TestIsAuthenticatedPasswordEntraTokenRefreshDeniesOnUsernameMismatch).
func TestIsAuthenticatedEntraMFADeniesOnUsernameMismatch(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		accessTokenUserInfo: &info.User{Name: "someone-else@email.com", ProviderID: "different-user-id"},
		mfaTokenResult:      newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	advanceToEntraMFAWait(t, b, sessionID, key)

	access, _, err := b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access,
		"a first-login MFA access token whose identity does not match the session username must be denied")
}

// TestIsAuthenticatedEntraMFADenialsDoNotCachePassword verifies that an Entra MFA
// denial does not persist an offline password file. A successful first login
// caches the password for offline use; a denied one must leave no such artifact,
// otherwise a later offline login could grant access to a user who never
// authenticated. This complements the existing denial tests, which assert the
// AuthDenied reply but not the absence of the password file.
func TestIsAuthenticatedEntraMFADenialsDoNotCachePassword(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	provider := &mockEntraPasswordProvider{
		MockProvider: &testutils.MockProvider{GetGroupsFails: true},
		flowState:    &himmelblau.MFAFlowState{},
		challengeInfo: &himmelblau.MFAChallengeInfo{
			Message:           "Approve the sign-in request in Microsoft Authenticator",
			Method:            "PhoneAppNotification",
			PollingIntervalMs: 1,
			MaxPollAttempts:   1,
		},
		mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))

	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)

	require.NoError(t, b.SetAvailableMode(sessionID, authmodes.EntraMFAWait))
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFAWait)
	require.NoError(t, err)

	access, _, err = b.IsAuthenticated(sessionID, "{}")
	require.NoError(t, err)
	require.Equal(t, broker.AuthDenied, access, "initial Entra MFA logins must be denied when groups cannot be resolved")

	require.NoFileExists(t, b.PasswordFilepathForSession(sessionID), "a denied Entra MFA login must not cache an offline password")
}

// TestIsAuthenticatedEntraMFACodeMaxAttemptsLockout verifies that repeated
// retryable wrong MFA codes are capped by maxAuthAttempts: after
// MaxAuthAttempts wrong submissions the broker returns AuthDeniedMaxTries
// (not AuthRetry) and releases the in-progress MFA flow immediately rather
// than waiting for EndSession. This is the lockout counterpart to the
// single-retry-then-success test (TestIsAuthenticatedEntraMFACodeWrongCodeRetries).
func TestIsAuthenticatedEntraMFACodeMaxAttemptsLockout(t *testing.T) {
	t.Parallel()

	username := "test-user@email.com"
	mfaAuthInfo := generateCachedInfo(t, tokenOptions{username: username, issuer: defaultIssuerURL})
	released := 0
	provider := &mockMFAAlwaysWrongCodeProvider{
		mockEntraPasswordProvider: &mockEntraPasswordProvider{
			MockProvider: &testutils.MockProvider{},
			flowState:    newTrackedMFAFlowState(func() { released++ }),
			challengeInfo: &himmelblau.MFAChallengeInfo{
				Message:           "Please type in the code displayed on your authenticator app:",
				Method:            "PhoneAppOTP",
				PollingIntervalMs: 5000,
				MaxPollAttempts:   10,
			},
			mfaTokenResult: newMFATokenResult(mfaAuthInfo.Token),
		},
	}

	b := newBrokerForTests(t, &brokerForTestConfig{
		Config:                broker.Config{DataDir: t.TempDir()},
		ownerAllowed:          true,
		firstUserBecomesOwner: true,
		provider:              provider,
		issuerURL:             defaultIssuerURL,
	})

	sessionID, key := newSessionForTests(t, b, username, sessionmode.Login)

	// Step 1: Submit password — routed to entra_mfa_code (PhoneAppOTP).
	updateAuthModes(t, b, sessionID, authmodes.EntraPassword)
	passwordAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "password", key))
	access, _, err := b.IsAuthenticated(sessionID, passwordAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthNext, access)
	require.Equal(t, []string{authmodes.EntraMFACode}, b.GetNextAuthModes(sessionID))

	require.NoError(t, b.SetAvailableMode(sessionID, authmodes.EntraMFACode))
	_, err = b.SelectAuthenticationMode(sessionID, authmodes.EntraMFACode)
	require.NoError(t, err)

	// Step 2: Submit wrong codes up to MaxAuthAttempts-1 — each must retry,
	// keep the flow alive, and not yet cache the offline password.
	wrongAuthData := fmt.Sprintf(`{"%s":"%s"}`, broker.AuthDataSecret, encryptSecret(t, "000000", key))
	for i := 0; i < broker.MaxAuthAttempts-1; i++ {
		access, data, err := b.IsAuthenticated(sessionID, wrongAuthData)
		require.NoError(t, err)
		require.Equal(t, broker.AuthRetry, access, "a wrong MFA code must return AuthRetry, not a terminal denial (attempt %d)", i+1)
		require.Contains(t, data, "Incorrect or expired code", "the retry message should ask for the code again")
		require.Equal(t, 0, released, "the MFA flow must NOT be released on a retryable wrong code")
		require.NoFileExists(t, b.PasswordFilepathForSession(sessionID), "no offline password should be cached while retrying wrong codes")
	}

	// Step 3: The MaxAuthAttempts-th wrong code triggers the max-tries lockout.
	access, _, err = b.IsAuthenticated(sessionID, wrongAuthData)
	require.NoError(t, err)
	require.Equal(t, broker.AuthDeniedMaxTries, access,
		"exhausting MaxAuthAttempts wrong MFA codes must return AuthDeniedMaxTries, not AuthRetry")
	require.Equal(t, 1, released, "the MFA flow must be released immediately on max-tries lockout")
	require.Len(t, provider.recordedChallengeData, broker.MaxAuthAttempts,
		"each wrong code submission must reuse the same MFA flow")
	require.NoFileExists(t, b.PasswordFilepathForSession(sessionID), "a max-tries lockout must not cache an offline password")
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	var cleanup func()
	defaultIssuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
