// Package broker is the generic oidc business code.
package broker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/fido"
	"github.com/canonical/authd/authd-oidc-brokers/internal/fileutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/canonical/authd/log"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	// LatestAPIVersion is the latest API version supported by the broker. It should be incremented when a non backward
	// compatible change is made to the API.
	// Note: Remember to also bump the LatestAPIVersion in internal/brokers/dbusbroker.go.
	LatestAPIVersion uint = 3

	maxAuthAttempts    = 3
	maxRequestDuration = 5 * time.Second

	// maxMFAPollDuration caps the total wall-clock time spent polling for MFA
	// approval, to prevent infinite polling.
	maxMFAPollDuration = 5 * time.Minute
)

// reauthModes is the set of auth modes offered when the user must re-authenticate
// (e.g. after token revocation, expiry, or password change).
var reauthModes = []string{authmodes.EntraAuth, authmodes.Device, authmodes.DeviceQr}

// Config is the configuration for the broker.
type Config struct {
	ConfigFile string
	DataDir    string

	userConfig
}

// fidoAuthenticator performs WebAuthn assertions with a locally connected
// FIDO2 security key. It is implemented by fido.Authenticator in withmsentraid
// builds; in other builds there is no implementation, the broker's field stays
// nil and the FIDO auth modes are disabled.
type fidoAuthenticator interface {
	// DevicePresent reports whether a FIDO device is connected to this machine.
	DevicePresent() bool
	// DeviceRequiresPIN reports whether the device needs a client PIN for
	// user verification.
	DeviceRequiresPIN() (bool, error)
	// Assert performs the WebAuthn Get ceremony and returns the assertion
	// JSON to pass back to the MFA flow as auth data.
	Assert(ctx context.Context, challenge string, allowList []string, pin string) (string, error)
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	cfg        Config
	apiVersion uint

	provider         providers.Provider
	oidcCfg          oidc.Config
	oidcClientSecret string
	fido             fidoAuthenticator

	currentSessions   map[string]session
	currentSessionsMu sync.RWMutex

	privateKey *rsa.PrivateKey
}

type session struct {
	username   string
	providerID string // stable provider identifier; empty until learned via auth or cache migration
	lang       string
	mode       string

	selectedMode    string
	authModes       []string
	attemptsPerMode map[string]int
	nextAuthModes   []string

	oidcServer              *oidc.Provider
	oauth2Config            oauth2.Config
	isOffline               bool
	providerConnectionError error
	userDataDir             string
	passwordPath            string
	tokenPath               string

	// Data to pass from one request to another.
	deviceAuthResponse        *oauth2.DeviceAuthResponse
	authInfo                  *token.AuthCachedInfo
	mfaFlowActive             *himmelblau.MFAFlowState
	mfaChallengeInfo          *himmelblau.MFAChallengeInfo
	entraAuthPasswordHash     string // pre-computed hash (not plaintext) for offline use
	entraAuthPasswordRequired bool
	fidoPIN                   string // security key PIN, kept in memory only while the FIDO exchange runs

	isAuthenticating *isAuthenticatedCtx
}

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// verifyAndExtractEntraUserInfo verifies the Entra auth access token's RS256
// signature against the tenant JWKS and extracts user info from that verified
// access token. It does NOT cross-check the username against the session; first
// login does that via userInfoFromTokenExtras.
func (b *Broker) verifyAndExtractEntraUserInfo(ctx context.Context, token *oauth2.Token) (info.User, error) {
	ep, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		return info.User{}, errors.New("provider does not support Entra authentication")
	}
	if err := ep.VerifyAccessToken(ctx, b.cfg.issuerURL, token.AccessToken); err != nil {
		return info.User{}, fmt.Errorf("access token verification failed: %w", err)
	}

	userInfo, err := ep.UserInfoFromAccessToken(token.AccessToken)
	if err != nil {
		return info.User{}, fmt.Errorf("could not extract user info from access token: %w", err)
	}

	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}

	return userInfo, nil
}

// userInfoFromTokenExtras is verifyAndExtractEntraUserInfo plus a cross-check
// that the returned access-token identity matches the username the user
// authenticated as. Used on first login (finishEntraAuth), where the username
// has not yet been bound to a verified identity.
func (b *Broker) userInfoFromTokenExtras(ctx context.Context, session *session, token *oauth2.Token) (info.User, error) {
	userInfo, err := b.verifyAndExtractEntraUserInfo(ctx, token)
	if err != nil {
		return info.User{}, err
	}

	if err := b.provider.VerifyUsername(session.username, userInfo.Name); err != nil {
		return info.User{}, fmt.Errorf("username verification failed: %w", err)
	}

	return userInfo, nil
}

// populateAuthInfo creates an AuthCachedInfo and populates it with provider
// metadata and user info. It returns the populated authInfo, or an auth
// response pair if a step fails.
//
// When userInfoOverride is nil, the default verified OIDC ID token path
// (getUserInfo) is used. Callers that already resolved user info through a
// different trust path (e.g. a verified Entra MFA access token) can pass it
// directly.
func (b *Broker) populateAuthInfo(ctx context.Context, session *session, t *oauth2.Token, rawIDToken string, userInfoOverride *info.User) (*token.AuthCachedInfo, string, isAuthenticatedDataResponse) {
	mp, mpOK := providers.ProviderAs[providers.MetadataProvider](b.provider)
	var extraFields map[string]interface{}
	if mpOK {
		extraFields = mp.GetExtraFields(t)
	}
	authInfo := token.NewAuthCachedInfo(t, rawIDToken, extraFields)

	var err error
	if mpOK {
		authInfo.ProviderMetadata, err = mp.GetMetadata(session.oidcServer)
		if err != nil {
			log.Errorf(context.Background(), "could not get provider metadata: %s", err)
			return nil, AuthDenied, unexpectedErrMsg("could not get provider metadata")
		}
	}

	if userInfoOverride != nil {
		authInfo.UserInfo = *userInfoOverride
	} else {
		authInfo.UserInfo, err = b.getUserInfo(ctx, session, t, rawIDToken, false)
	}
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return nil, AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return nil, AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}

	return authInfo, "", nil
}

type option struct {
	provider providers.Provider
	fido     fidoAuthenticator
}

// Option is a func that allows to override some of the broker default settings.
type Option func(*option)

// New returns a new oidc Broker with the providers listed in the configuration file.
func New(cfg Config, apiVersion uint, args ...Option) (b *Broker, err error) {
	p := providers.CurrentProvider()

	if cfg.ConfigFile != "" {
		cfg.userConfig, err = parseConfigFromPath(cfg.ConfigFile, p)
		if err != nil {
			return nil, err
		}
	}

	opts := option{
		provider: p,
		fido:     defaultFIDOAuthenticator(),
	}
	for _, arg := range args {
		arg(&opts)
	}

	if cfg.DataDir == "" {
		err = errors.Join(err, errors.New("cache path is required and was not provided"))
	}
	if cfg.issuerURL == "" {
		err = errors.Join(err, errors.New("issuer URL is required and was not provided"))
	}
	if cfg.clientID == "" {
		err = errors.Join(err, errors.New("client ID is required and was not provided"))
	}
	// The entra_auth flow can only retrieve groups from Microsoft Graph when
	// device registration or a client secret is available (see the matching check
	// in authModeIsAvailable). If neither is configured, the flow is unusable, so
	// fail at startup rather than silently falling back at login time: a startup
	// failure is far more visible to the administrator than a per-login denial.
	if cfg.flows.EntraAuth && !cfg.registerDevice && cfg.clientSecret == "" {
		if _, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](opts.provider); ok {
			err = errors.Join(err, fmt.Errorf(
				"invalid configuration: the %[1]q flow is enabled in [%[2]s], but it cannot retrieve group memberships from Microsoft Graph without %[3]q enabled or a %[4]q configured; "+
					"fix this by either disabling %[1]q, enabling %[3]q, or granting the app the GroupMember.Read.All application permission and configuring a %[4]q",
				flowsEntraAuthKey, flowsSection, registerDeviceKey, clientSecret))
		}
	}
	if err != nil {
		return nil, err
	}

	if cfg.homeBaseDir == "" {
		cfg.homeBaseDir = "/home"
	}

	// Generate a new private key for the broker.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return nil, errors.New("failed to generate broker private key")
	}

	clientID := cfg.clientID
	if _, ok := providers.ProviderAs[providers.DeviceRegisterer](opts.provider); ok && cfg.registerDevice {
		clientID = consts.MicrosoftBrokerAppID
	}

	// The Microsoft Broker App is a public client and must never send a secret,
	// even when client_secret is configured (for Graph API fallback). Resolve
	// this once so that NewSession never needs to re-derive it from the client ID.
	oidcClientSecret := cfg.clientSecret
	if clientID == consts.MicrosoftBrokerAppID {
		oidcClientSecret = ""
	}

	b = &Broker{
		cfg:              cfg,
		apiVersion:       apiVersion,
		provider:         opts.provider,
		oidcCfg:          oidc.Config{ClientID: clientID},
		oidcClientSecret: oidcClientSecret,
		fido:             opts.fido,
		privateKey:       privateKey,

		currentSessions:   make(map[string]session),
		currentSessionsMu: sync.RWMutex{},
	}

	// If the provider supports app-only Graph API group lookup and a client secret
	// is configured, propagate the secret so it can use client credentials as
	// a fallback when the delegated token lacks GroupMember.Read.All.
	if setter, ok := providers.ProviderAs[providers.GraphClientSecretSetter](opts.provider); ok && cfg.clientSecret != "" {
		setter.SetGraphClientSecret(cfg.clientSecret)
	}

	return b, nil
}

// normalizedIssuer converts an issuer URL into a filesystem-safe directory name
// by stripping the scheme and replacing path/port separators with underscores.
func normalizedIssuer(issuerURL string) string {
	_, issuer, found := strings.Cut(issuerURL, "://")
	if !found {
		// If the issuer URL does not contain a scheme, use the whole issuer URL as the issuer.
		issuer = issuerURL
	}
	issuer = strings.ReplaceAll(issuer, "/", "_")
	issuer = strings.ReplaceAll(issuer, ":", "_")

	return issuer
}

// setCachePaths updates all session cache-file paths derived from dataDir.
func setCachePaths(s *session, dataDir string) {
	s.userDataDir = dataDir
	s.tokenPath = filepath.Join(dataDir, "token.json")
	s.passwordPath = filepath.Join(dataDir, "password")
}

func childDir(parent, name string) (string, error) {
	if name == "" {
		return "", errors.New("cannot be empty")
	}
	if name == "." || name == ".." {
		return "", errors.New("path traversal detected")
	}

	parent = filepath.Clean(parent)
	dir := filepath.Join(parent, name)
	rel, err := filepath.Rel(parent, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.Base(dir) != name {
		return "", errors.New("path traversal detected")
	}

	return dir, nil
}

func pathIsInside(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func (b *Broker) issuerDataDir() (string, error) {
	dir, err := childDir(b.cfg.DataDir, normalizedIssuer(b.cfg.issuerURL))
	if err != nil {
		return "", fmt.Errorf("can't use issuer URL %q for data dir: %w", b.cfg.issuerURL, err)
	}
	return dir, nil
}

func (b *Broker) cacheSymlinkTarget(linkPath string) (string, error) {
	target, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return "", err
	}

	issuerDataDir, err := b.issuerDataDir()
	if err != nil {
		return "", err
	}
	if !pathIsInside(issuerDataDir, target) {
		return "", fmt.Errorf("cache symlink %q points outside issuer cache directory: %q", linkPath, target)
	}

	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cache symlink %q points to non-directory target %q", linkPath, target)
	}

	return target, nil
}

func (b *Broker) ensureCompatibilitySymlink(linkPath, target string) error {
	// Use a relative symlink so that the link remains valid when the path prefix
	// changes (e.g. a snap revision bump moves everything from .../316/... to
	// .../317/... while the relative offset between the two sibling directories
	// stays the same).
	relTarget, err := filepath.Rel(filepath.Dir(linkPath), target)
	if err != nil {
		return fmt.Errorf("could not compute relative symlink target for %q → %q: %w", linkPath, target, err)
	}

	info, err := os.Lstat(linkPath)
	if errors.Is(err, os.ErrNotExist) {
		return os.Symlink(relTarget, linkPath)
	}
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("path already exists and is not a symlink")
	}

	existingTarget, err := b.cacheSymlinkTarget(linkPath)
	if err != nil {
		if rmErr := os.Remove(linkPath); rmErr != nil {
			return fmt.Errorf("could not replace invalid cache compatibility symlink %q: %w", linkPath, rmErr)
		}
		return os.Symlink(relTarget, linkPath)
	}
	if filepath.Clean(existingTarget) != filepath.Clean(target) {
		return fmt.Errorf("cache compatibility symlink already points to %q", existingTarget)
	}

	// The symlink resolves to the right target, but may be stored as an absolute
	// path. Normalize it to a relative path so it survives a snap revision bump
	// that moves the entire data directory prefix.
	rawLink, err := os.Readlink(linkPath)
	if err != nil {
		return err
	}
	if rawLink == relTarget {
		return nil
	}
	if rmErr := os.Remove(linkPath); rmErr != nil {
		return fmt.Errorf("could not replace absolute cache compatibility symlink %q: %w", linkPath, rmErr)
	}
	return os.Symlink(relTarget, linkPath)
}

func consolidateKnownCacheFiles(sourceDir, targetDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	knownCacheFiles := map[string]struct{}{"token.json": {}, "password": {}}
	for _, entry := range entries {
		if _, ok := knownCacheFiles[entry.Name()]; !ok {
			return fmt.Errorf("unexpected cache entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-regular cache entry %q", entry.Name())
		}

		targetPath := filepath.Join(targetDir, entry.Name())
		targetInfo, err := os.Lstat(targetPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("target cache entry %q already exists and is not regular", entry.Name())
		}

		sourceContent, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			return err
		}
		targetContent, err := os.ReadFile(targetPath)
		if err != nil {
			return err
		}
		if !slices.Equal(sourceContent, targetContent) {
			return fmt.Errorf("target cache entry %q already exists with different content", entry.Name())
		}
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDir, entry.Name())
		targetPath := filepath.Join(targetDir, entry.Name())
		if _, err := os.Lstat(targetPath); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		} else if err != nil {
			return err
		}

		if err := os.Remove(sourcePath); err != nil {
			return err
		}
	}

	return os.Remove(sourceDir)
}

func (b *Broker) ensureUsernameCompatibilityPath(username, providerIDDir string) error {
	usernameDir, err := b.userDataDir(username)
	if err != nil {
		return err
	}

	info, err := os.Lstat(usernameDir)
	if errors.Is(err, os.ErrNotExist) {
		return b.ensureCompatibilitySymlink(usernameDir, providerIDDir)
	}
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return b.ensureCompatibilitySymlink(usernameDir, providerIDDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("path already exists and is not a directory or symlink")
	}
	if err := consolidateKnownCacheFiles(usernameDir, providerIDDir); err != nil {
		return err
	}

	return b.ensureCompatibilitySymlink(usernameDir, providerIDDir)
}

// adoptProviderIDDir points the session at the provider ID-keyed cache directory. It is only
// called once the cache actually lives at providerIDDir (or is reachable through a compatibility
// symlink), so the session never ends up writing to a directory that does not exist.
func adoptProviderIDDir(s *session, providerID, providerIDDir string) {
	s.providerID = providerID
	setCachePaths(s, providerIDDir)
}

// ensureProviderIDCacheDir migrates the session's on-disk cache from a username-based
// directory to a provider ID-based one. It is idempotent: calling it twice with the
// same provider ID has no effect.
//
// Two cases are handled:
//  1. The provider ID-based directory already exists (e.g. a previous email change): any cache
//     files still in the username directory are consolidated into it and a compatibility symlink
//     is left at the username path so that future NewSession calls resolve it.
//  2. The provider ID-based directory does not yet exist: the username directory is renamed to the
//     provider ID path and a compatibility symlink is left behind. If there is no username
//     directory to migrate yet, the provider ID directory is created directly.
//
// On any unrecoverable error the function logs a warning and returns without changing the session
// paths, leaving the username-based layout in place for a later attempt. s.providerID is set only
// after the cache has been switched to the provider ID directory.
func (b *Broker) ensureProviderIDCacheDir(s *session, providerID string) {
	providerIDDir, err := b.userDataDir(providerID)
	if err != nil {
		log.Warningf(context.Background(), "Could not determine cache directory for provider ID %q: %v", providerID, err)
		return
	}
	if providerIDDir == s.userDataDir {
		// The session is already using the provider ID-keyed directory.
		return
	}

	exists, err := fileutils.FileExists(providerIDDir)
	if err != nil {
		log.Errorf(context.Background(), "Could not check if provider ID cache directory %q exists: %v", providerIDDir, err)
		return
	}

	if exists {
		b.redirectToExistingProviderIDDir(s, providerID, providerIDDir)
		return
	}
	b.migrateUsernameDirToProviderIDDir(s, providerID, providerIDDir)
}

// redirectToExistingProviderIDDir handles the case where the provider ID-keyed directory already
// exists (e.g. the user changed their email at the IdP and a directory for the new username was
// created). Any cache files still sitting in the username directory are consolidated into the
// provider ID directory and a compatibility symlink is left at the username path.
func (b *Broker) redirectToExistingProviderIDDir(s *session, providerID, providerIDDir string) {
	log.Infof(context.Background(), "Redirecting cache for user %q to existing provider ID-based directory %q", s.username, providerIDDir)

	if info, lstatErr := os.Lstat(s.userDataDir); lstatErr == nil && info.IsDir() {
		if moveErr := consolidateKnownCacheFiles(s.userDataDir, providerIDDir); moveErr != nil {
			log.Warningf(context.Background(), "Could not consolidate cache directory %q into %q: %v", s.userDataDir, providerIDDir, moveErr)
			return
		}
	}
	if linkErr := b.ensureCompatibilitySymlink(s.userDataDir, providerIDDir); linkErr != nil {
		log.Warningf(context.Background(), "Could not create cache compatibility symlink %q: %v", s.userDataDir, linkErr)
		return
	}
	adoptProviderIDDir(s, providerID, providerIDDir)
}

// migrateUsernameDirToProviderIDDir renames the username-keyed cache directory to the provider
// ID-keyed one and leaves a compatibility symlink at the old path. If the username directory does
// not exist yet there is nothing to migrate, so the provider ID directory is created directly.
func (b *Broker) migrateUsernameDirToProviderIDDir(s *session, providerID, providerIDDir string) {
	renameErr := os.Rename(s.userDataDir, providerIDDir)
	if os.IsNotExist(renameErr) {
		// There is no cache to migrate yet (e.g. a brand new user, or a freshly changed email
		// whose directory has not been written yet): create the provider ID directory directly.
		b.createProviderIDDirWithSymlink(s, providerID, providerIDDir)
		return
	}
	if renameErr != nil {
		log.Warningf(context.Background(), "Could not migrate cache directory %q to %q: %v", s.userDataDir, providerIDDir, renameErr)
		return
	}
	log.Infof(context.Background(), "Migrated cache directory for user %q from %q to %q", s.username, s.userDataDir, providerIDDir)

	linkErr := b.ensureCompatibilitySymlink(s.userDataDir, providerIDDir)
	if linkErr == nil {
		adoptProviderIDDir(s, providerID, providerIDDir)
		return
	}
	log.Warningf(context.Background(), "Could not create cache compatibility symlink %q: %v", s.userDataDir, linkErr)

	// The cache now lives at providerIDDir but we could not leave a compatibility symlink behind.
	// Roll the rename back so old authd versions, which resolve the cache by username, keep working.
	// If the rollback succeeds the session stays on the username directory; if it fails, the data is
	// only at providerIDDir, so the session must use it.
	if rollbackErr := os.Rename(providerIDDir, s.userDataDir); rollbackErr != nil {
		log.Warningf(context.Background(), "Could not roll back cache directory migration from %q to %q: %v", providerIDDir, s.userDataDir, rollbackErr)
		adoptProviderIDDir(s, providerID, providerIDDir)
	}
}

// createProviderIDDirWithSymlink creates the provider ID-keyed directory for a session that has no
// existing username-keyed cache to migrate, and leaves a compatibility symlink at the username path
// for old authd versions that resolve the cache by username. If the symlink cannot be created, the
// freshly created directory is removed so the next attempt starts from a clean state.
func (b *Broker) createProviderIDDirWithSymlink(s *session, providerID, providerIDDir string) {
	if mkdirErr := os.MkdirAll(providerIDDir, 0700); mkdirErr != nil {
		log.Warningf(context.Background(), "Could not create provider ID cache directory %q: %v", providerIDDir, mkdirErr)
		return
	}
	if linkErr := b.ensureCompatibilitySymlink(s.userDataDir, providerIDDir); linkErr != nil {
		log.Warningf(context.Background(), "Could not create cache compatibility symlink %q: %v", s.userDataDir, linkErr)
		if rmErr := os.RemoveAll(providerIDDir); rmErr != nil {
			log.Warningf(context.Background(), "Could not remove unused provider ID cache directory %q: %v", providerIDDir, rmErr)
		}
		return
	}
	adoptProviderIDDir(s, providerID, providerIDDir)
}

// userDataDir returns the path to the broker's data directory for the given user.
// If the issuer URL or the username contains path traversal characters, an error is returned.
func (b *Broker) userDataDir(basePath string) (string, error) {
	if basePath == "" {
		return "", errors.New("base path cannot be empty")
	}

	issuerDataDir, err := b.issuerDataDir()
	if err != nil {
		return "", fmt.Errorf("invalid issuer URL %q: %w", b.cfg.issuerURL, err)
	}

	dir, err := childDir(issuerDataDir, basePath)
	if err != nil {
		return "", fmt.Errorf("invalid base path %q: %w", basePath, err)
	}

	return dir, nil
}

// NewSession creates a new session for the user. providerID is the stable provider
// identifier from authd's database; when non-empty it is used to locate the
// provider ID-keyed cache directory directly, bypassing the username-based lookup.
func (b *Broker) NewSession(username, lang, mode, providerID string) (sessionID, encryptionKey string, err error) {
	if username == "" {
		return "", "", errors.New("username is required")
	}

	sessionID = uuid.New().String()
	s := session{
		username: username,
		lang:     lang,
		mode:     mode,

		attemptsPerMode: make(map[string]int),
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&b.privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal broker public key: %v", err)
	}

	// When authd passes the provider ID (v3 API), try the provider ID-keyed cache directory
	// first. This is the authoritative location after migration and handles
	// the case where the username changed at the IdP (no symlink exists for
	// the new email).
	if providerID != "" {
		if providerIDDir, providerIDErr := b.userDataDir(providerID); providerIDErr == nil {
			if fi, statErr := os.Stat(providerIDDir); statErr == nil && fi.IsDir() {
				s.providerID = providerID
				setCachePaths(&s, providerIDDir)
				if linkErr := b.ensureUsernameCompatibilityPath(username, providerIDDir); linkErr != nil {
					log.Warningf(context.Background(), "Could not repair cache compatibility path for user %q: %v", username, linkErr)
				}
			}
		}
	}

	// Fall back to the username-based path when provider ID lookup didn't resolve.
	if s.userDataDir == "" {
		s.userDataDir, err = b.userDataDir(username)
		if err != nil {
			return "", "", err
		}
		setCachePaths(&s, s.userDataDir)

		// Attempt to migrate a legacy username-based cache directory to the provider ID-based layout.
		// If the entry at the username path is already a compatibility symlink from a prior
		// migration, resolve it and update paths.  If it is a real directory whose cached
		// token contains a provider ID, rename the directory and leave the symlink behind.
		if linkInfo, lstatErr := os.Lstat(s.userDataDir); lstatErr == nil {
			switch {
			case linkInfo.Mode()&os.ModeSymlink != 0:
				if providerIDDir, evalErr := b.cacheSymlinkTarget(s.userDataDir); evalErr == nil {
					setCachePaths(&s, providerIDDir)
					if cachedInfo, loadErr := token.LoadAuthInfo(s.tokenPath); loadErr == nil {
						s.providerID = cachedInfo.UserInfo.ProviderID
					}
				} else {
					// The compatibility symlink is dangling or unsafe. Leaving it in place would make
					// subsequent cache writes fail or follow an untrusted target. Remove it so the
					// username path can be recreated as a fresh cache dir.
					log.Warningf(context.Background(), "Removing dangling cache symlink %q: %v", s.userDataDir, evalErr)
					if rmErr := os.Remove(s.userDataDir); rmErr != nil {
						log.Warningf(context.Background(), "Could not remove dangling cache symlink %q: %v", s.userDataDir, rmErr)
					}
				}
			case linkInfo.IsDir():
				if cachedInfo, loadErr := token.LoadAuthInfo(s.tokenPath); loadErr == nil && cachedInfo.UserInfo.ProviderID != "" {
					b.ensureProviderIDCacheDir(&s, cachedInfo.UserInfo.ProviderID)
				}
			}
		}
	}

	// Construct an OIDC provider via OIDC discovery.
	s.oidcServer, err = b.connectToOIDCServer(context.Background())
	if err != nil && b.cfg.forceAccessCheckWithProvider {
		log.Errorf(context.Background(), "Could not connect to the provider and force_access_check_with_provider is set, denying authentication: %v", err)
		//nolint:staticcheck,revive // ST1005 This error is displayed as is to the user, so it should be capitalized
		return "", "", errors.New("Error connecting to provider. Check your network connection.")
	}
	if err != nil {
		log.Noticef(context.Background(), "Could not connect to the provider, starting session in offline mode: %v", err)
		s.isOffline = true
		s.providerConnectionError = err
	}

	scopes := append(consts.DefaultScopes, b.provider.AdditionalScopes()...)
	if _, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider); ok && b.cfg.registerDevice {
		scopes = consts.MicrosoftBrokerAppScopes
	}
	// Append extra scopes from config
	scopes = append(scopes, b.cfg.extraScopes...)
	if s.oidcServer != nil {
		s.oauth2Config = oauth2.Config{
			ClientID:     b.oidcCfg.ClientID,
			ClientSecret: b.oidcClientSecret,
			Endpoint:     s.oidcServer.Endpoint(),
			Scopes:       scopes,
		}
	}

	b.currentSessionsMu.Lock()
	b.currentSessions[sessionID] = s
	b.currentSessionsMu.Unlock()

	return sessionID, base64.StdEncoding.EncodeToString(pubASN1), nil
}

func (b *Broker) connectToOIDCServer(ctx context.Context) (*oidc.Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()

	return oidc.NewProvider(ctx, b.cfg.issuerURL)
}

// GetAuthenticationModes returns the authentication modes available for the user.
func (b *Broker) GetAuthenticationModes(sessionID string, supportedUILayouts []map[string]string) (authModesWithLabels []map[string]string, err error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	availableModes, err := b.availableAuthModes(session)
	if err != nil {
		return nil, err
	}

	// Store the available auth modes, so that we can check in SelectAuthenticationMode if the selected mode is valid.
	session.authModes = availableModes
	if err := b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	modesSupportedByUI := b.authModesSupportedByUI(supportedUILayouts)

	for _, mode := range availableModes {
		if !slices.Contains(modesSupportedByUI, mode) {
			continue
		}

		authModesWithLabels = append(authModesWithLabels, map[string]string{
			"id":    mode,
			"label": authmodes.Label[mode],
		})
	}

	if len(authModesWithLabels) == 0 {
		// If we can't use a local authentication mode and we failed to connect to the provider,
		// report the connection error.
		if session.providerConnectionError != nil {
			log.Errorf(context.Background(), "Error connecting to provider: %v", session.providerConnectionError)
			//nolint:staticcheck,revive // ST1005 This error is displayed as is to the user, so it should be capitalized
			return nil, errors.New("Error connecting to provider. Check your network connection.")
		}
		return nil, fmt.Errorf("no authentication modes available for user %q", session.username)
	}

	return authModesWithLabels, nil
}

func (b *Broker) availableAuthModes(session session) (availableModes []string, err error) {
	if len(session.nextAuthModes) > 0 {
		for _, mode := range session.nextAuthModes {
			if !b.authModeIsAvailable(session, mode) {
				continue
			}
			availableModes = append(availableModes, mode)
		}
		if availableModes == nil {
			log.Warningf(context.Background(), "None of the next auth modes are available: %v", session.nextAuthModes)
		}
		return availableModes, nil
	}

	switch session.mode {
	case sessionmode.ChangePassword, sessionmode.ChangePasswordOld:
		// Session is for changing the password.
		if !passwordFileExists(session) {
			return nil, errors.New("password file does not exist, cannot change password")
		}
		return []string{authmodes.Password}, nil

	default:
		// Session is for login. Check which auth modes are available.
		// The order of the modes is important, because authd picks the first supported one.
		// Password authentication should be the first option if available, to avoid re-doing the device code flow
		// when it's not necessary.
		modes := append([]string{authmodes.Password}, b.provider.SupportedOnlineAuthModes()...)
		for _, mode := range modes {
			if b.authModeIsAvailable(session, mode) {
				availableModes = append(availableModes, mode)
			}
		}
		return availableModes, nil
	}
}

func (b *Broker) authModeIsAvailable(session session, authMode string) bool {
	switch authMode {
	case authmodes.Password:
		if !tokenExists(session) {
			log.Debugf(context.Background(), "Token does not exist for user %q, so local password authentication is not available", session.username)
			return false
		}

		if !passwordFileExists(session) {
			log.Debugf(context.Background(), "Password file does not exist for user %q, so local password authentication is not available", session.username)
			return false
		}

		authInfo, err := token.LoadAuthInfo(session.tokenPath)
		if err != nil {
			log.Warningf(context.Background(), "Could not load token, so local password authentication is not available: %v", err)
			return false
		}

		dr, isDR := providers.ProviderAs[providers.DeviceRegisterer](b.provider)
		if !isDR {
			// If the provider does not support device registration,
			// we can always use the token for local password authentication.
			log.Debugf(context.Background(), "Provider does not support device registration, so local password authentication is available for user %q", session.username)
			return true
		}

		if session.isOffline {
			// If the session is in offline mode, we can't register the device anyway,
			// so we can allow the user to use local password authentication.
			log.Debugf(context.Background(), "Session is in offline mode, so local password authentication is available for user %q", session.username)
			return true
		}

		isTokenForDeviceRegistration := dr.IsTokenForDeviceRegistration(authInfo)

		if b.cfg.registerDevice && !isTokenForDeviceRegistration {
			// TODO: We might want to display a message to the user in this case
			log.Noticef(context.Background(), "Token exists for user %q, but it cannot be used for device registration, so local password authentication is not available", session.username)
			return false
		}
		if !b.cfg.registerDevice && isTokenForDeviceRegistration {
			// TODO: We might want to display a message to the user in this case
			log.Noticef(context.Background(), "Token exists for user %q, but it requires device registration, so local password authentication is not available", session.username)
			return false
		}

		return true
	case authmodes.NewPassword:
		return true
	case authmodes.Device, authmodes.DeviceQr:
		if !b.cfg.flows.DeviceAuth {
			log.Debugf(context.Background(), "The device code flow is disabled in the [flows] config, so it is not available")
			return false
		}
		if session.oidcServer == nil {
			log.Debugf(context.Background(), "OIDC server is not initialized, so the device code flow is not available")
			return false
		}
		if session.oidcServer.Endpoint().DeviceAuthURL == "" {
			log.Debugf(context.Background(), "OIDC server does not support the device code flow, so it is not available")
			return false
		}
		if session.isOffline {
			log.Noticef(context.Background(), "Session is in offline mode, so the device code flow is not available")
			return false
		}
		return true
	case authmodes.EntraAuth:
		if _, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider); !ok {
			return false
		}
		if !b.cfg.flows.EntraAuth {
			log.Debugf(context.Background(), "The %q flow is disabled in the [flows] config, so it is not available", authmodes.EntraAuth)
			return false
		}
		if session.isOffline {
			log.Debugf(context.Background(), "Session is in offline mode, so Entra %s authentication is not available", authMode)
			return false
		}
		// The entra_auth flow can only retrieve groups from Microsoft Graph
		// when device registration (PRT-based token exchange) or a client
		// secret (app-only client credentials) is available. Without either,
		// every login would fail at the group-fetch step, so don't offer the
		// mode rather than letting users hit an undiagnosable denial. New()
		// rejects that configuration at startup, so this is a defensive guard
		// for tests or manually constructed brokers.
		if !b.cfg.registerDevice && b.cfg.clientSecret == "" {
			log.Debugf(context.Background(), "The %q flow requires %q to be enabled or a client secret to be configured to retrieve groups from Microsoft Graph, so it is not available", flowsEntraAuthKey, registerDeviceKey)
			return false
		}
		return true
	case authmodes.EntraAuthWait, authmodes.EntraAuthCode, authmodes.EntraAuthFido, authmodes.EntraAuthFidoPin:
		// MFA follow-up modes are always available when offered via AuthNext.
		return true
	}
	return false
}

func tokenExists(session session) bool {
	exists, err := fileutils.FileExists(session.tokenPath)
	if err != nil {
		log.Warningf(context.Background(), "Could not check if token exists: %v", err)
	}
	return exists
}

func passwordFileExists(session session) bool {
	exists, err := fileutils.FileExists(session.passwordPath)
	if err != nil {
		log.Warningf(context.Background(), "Could not check if local password file exists: %v", err)
	}
	return exists
}

func (b *Broker) authModesSupportedByUI(supportedUILayouts []map[string]string) (supportedModes []string) {
	for _, layout := range supportedUILayouts {
		modes := b.supportedAuthModesFromLayout(layout)
		supportedModes = append(supportedModes, modes...)
	}
	return supportedModes
}

func (b *Broker) supportedAuthModesFromLayout(layout map[string]string) []string {
	supportedEntries := strings.Split(strings.TrimPrefix(layout["entry"], "optional:"), ",")
	switch layout["type"] {
	case "qrcode":
		if !strings.Contains(layout["wait"], "true") {
			return nil
		}
		if layout["renders_qrcode"] == "false" {
			return []string{authmodes.Device}
		}
		return []string{authmodes.DeviceQr}

	case "form":
		var modes []string
		if slices.Contains(supportedEntries, "chars_password") {
			modes = append(modes, authmodes.Password, authmodes.EntraAuth, authmodes.EntraAuthFidoPin)
		}
		if strings.Contains(layout["wait"], "true") {
			modes = append(modes, authmodes.EntraAuthWait, authmodes.EntraAuthFido)
		}
		if slices.Contains(supportedEntries, "chars") {
			modes = append(modes, authmodes.EntraAuthCode)
		}
		return modes

	case "newpassword":
		if slices.Contains(supportedEntries, "chars_password") {
			return []string{authmodes.NewPassword}
		}
	}
	return nil
}

// SelectAuthenticationMode selects the authentication mode for the user.
func (b *Broker) SelectAuthenticationMode(sessionID, authModeID string) (uiLayoutInfo map[string]string, err error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	// populate UI options based on selected authentication mode
	uiLayoutInfo, err = b.generateUILayout(&session, authModeID)
	if err != nil {
		return nil, err
	}

	// Store selected mode
	session.selectedMode = authModeID

	if err = b.updateSession(sessionID, session); err != nil {
		return nil, err
	}

	return uiLayoutInfo, nil
}

func (b *Broker) generateUILayout(session *session, authModeID string) (map[string]string, error) {
	if !slices.Contains(session.authModes, authModeID) {
		return nil, fmt.Errorf("selected authentication mode %q does not exist", authModeID)
	}

	var uiLayout map[string]string
	switch authModeID {
	case authmodes.Device, authmodes.DeviceQr:
		ctx, cancel := context.WithTimeout(context.Background(), maxRequestDuration)
		defer cancel()

		var authOpts []oauth2.AuthCodeOption

		// Workaround to cater for RFC compliant oauth2 server. Public providers do not properly
		// implement the RFC, (probably) because they assume that device clients are public.
		// As described in https://datatracker.ietf.org/doc/html/rfc8628#section-3.1
		// device code flow requests must provide client authentication, similar to that for
		// the token endpoint.
		// The golang/oauth2 library does not implement this, see https://github.com/golang/oauth2/issues/685.
		// We implement a workaround for implementing the client_secret_post client authn method.
		// Supporting client_secret_basic would require us to patch the http client used by the
		// oauth2 lib.
		// Some providers support both of these authentication methods, some implement only one and
		// some implement neither.
		// This was tested with the following providers:
		// - Ory Hydra: supports client_secret_post
		// TODO @shipperizer: client_authentication methods should be configurable
		if secret := session.oauth2Config.ClientSecret; secret != "" {
			authOpts = append(authOpts, oauth2.SetAuthURLParam("client_secret", secret))
		}

		log.Debug(ctx, "Sending Device Authorization Request to retrieve device code...")
		response, err := session.oauth2Config.DeviceAuth(ctx, authOpts...)
		if err != nil {
			return nil, fmt.Errorf("could not generate device code flow layout: %v", err)
		}
		log.Debugf(ctx, "Retrieved device code. Device Authorization Response: %#v", response)
		session.deviceAuthResponse = response

		label := "Open the URL and enter the code below."
		if authModeID == authmodes.DeviceQr {
			label = "Scan the QR code or open the URL and enter the code below."
		}

		uiLayout = map[string]string{
			"type":    "qrcode",
			"label":   label,
			"wait":    "true",
			"button":  "Request new code",
			"content": response.VerificationURI,
			"code":    response.UserCode,
		}

	case authmodes.Password:
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Enter your password",
			"entry": "chars_password",
		}

	case authmodes.EntraAuth:
		if session.entraAuthPasswordRequired {
			uiLayout = map[string]string{
				"type":  "form",
				"label": "Enter your Entra ID password",
				"entry": "chars_password",
			}
		} else {
			uiLayout = map[string]string{
				"type":  "form",
				"label": "Checking available Entra ID authentication methods...",
				"wait":  "true",
			}
		}

	case authmodes.EntraAuthWait:
		mfaWaitLabel := "Waiting for MFA approval..."
		if session.mfaChallengeInfo != nil && session.mfaChallengeInfo.Message != "" {
			mfaWaitLabel = session.mfaChallengeInfo.Message
		}
		uiLayout = map[string]string{
			"type":  "form",
			"label": mfaWaitLabel,
			"wait":  "true",
		}

	case authmodes.EntraAuthCode:
		uiLayout = map[string]string{
			"type":  "form",
			"entry": "chars",
			"label": "Enter your MFA code",
		}

	case authmodes.EntraAuthFido:
		uiLayout = map[string]string{
			"type":  "form",
			"label": "Insert your security key and touch it",
			"wait":  "true",
		}

	case authmodes.EntraAuthFidoPin:
		uiLayout = map[string]string{
			"type":  "form",
			"entry": "chars_password",
			"label": "Enter your security key PIN",
		}

	case authmodes.NewPassword:
		label := "Create a local password"
		if session.mode == sessionmode.ChangePassword || session.mode == sessionmode.ChangePasswordOld {
			label = "Update your local password"
		}

		uiLayout = map[string]string{
			"type":  "newpassword",
			"label": label,
			"entry": "chars_password",
		}
	}

	return uiLayout, nil
}

// IsAuthenticated evaluates the provided authenticationData and returns the authentication status for the user.
func (b *Broker) IsAuthenticated(sessionID, authenticationData string) (string, string, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return AuthDenied, "{}", err
	}

	var authData map[string]string
	if authenticationData != "" {
		if err := json.Unmarshal([]byte(authenticationData), &authData); err != nil {
			return AuthDenied, "{}", fmt.Errorf("authentication data is not a valid json value: %v", err)
		}
	}

	ctx, err := b.startAuthenticate(sessionID)
	if err != nil {
		return AuthDenied, "{}", err
	}

	// Cleans up the IsAuthenticated context when the call is done.
	defer b.CancelIsAuthenticated(sessionID)

	authDone := make(chan struct{})
	var access string
	var iadResponse isAuthenticatedDataResponse
	go func() {
		access, iadResponse = b.handleIsAuthenticated(ctx, &session, authData)
		close(authDone)
	}()

	select {
	case <-authDone:
	case <-ctx.Done():
		// We can ignore the error here since the message is constant.
		msg, _ := json.Marshal(errorMessage{Message: "Authentication request cancelled"})
		return AuthCancelled, string(msg), ctx.Err()
	}

	if access == AuthRetry {
		session.attemptsPerMode[session.selectedMode]++
		if session.attemptsPerMode[session.selectedMode] >= maxAuthAttempts {
			access = AuthDeniedMaxTries
			if b.apiVersion < 2 {
				access = AuthDenied
			}
			iadResponse = errorMessage{Message: "Maximum number of authentication attempts reached"}
			// Free any in-progress MFA flow immediately rather than waiting for
			// EndSession — consistent with all other terminal paths.
			session.entraAuthPasswordHash = ""
			clearEntraAuthState(&session)
		}
	}

	if err = b.updateSession(sessionID, session); err != nil {
		return AuthDenied, "{}", err
	}

	encoded, err := json.Marshal(iadResponse)
	if err != nil {
		return AuthDenied, "{}", fmt.Errorf("could not parse data to JSON: %v", err)
	}

	data := string(encoded)
	if data == "null" {
		data = "{}"
	}
	return access, data, nil
}

func unexpectedErrMsg(msg string) errorMessage {
	return errorMessage{Message: fmt.Sprintf("An unexpected error occurred: %s. Please report this error on https://github.com/canonical/authd/issues", msg)}
}

func (b *Broker) handleIsAuthenticated(ctx context.Context, session *session, authData map[string]string) (access string, data isAuthenticatedDataResponse) {
	rawSecret, ok := authData[AuthDataSecret]
	if !ok {
		rawSecret = authData[AuthDataSecretOld]
	}

	// Decrypt secret if present.
	secret, err := decodeRawSecret(b.privateKey, rawSecret)
	if err != nil {
		log.Errorf(context.Background(), "could not decode secret: %s", err)
		return AuthRetry, unexpectedErrMsg("could not decode secret")
	}

	switch session.selectedMode {
	case authmodes.Device, authmodes.DeviceQr:
		return b.deviceAuth(ctx, session)
	case authmodes.Password:
		return b.passwordAuth(ctx, session, secret)
	case authmodes.NewPassword:
		return b.newPassword(session, secret)
	case authmodes.EntraAuth:
		return b.entraAuth(ctx, session, secret)
	case authmodes.EntraAuthWait:
		return b.entraAuthWaitAuth(ctx, session)
	case authmodes.EntraAuthCode:
		return b.entraAuthCodeAuth(ctx, session, secret)
	case authmodes.EntraAuthFido:
		return b.entraAuthFidoAuth(ctx, session)
	case authmodes.EntraAuthFidoPin:
		return b.entraAuthFidoPinAuth(session, secret)
	default:
		log.Errorf(context.Background(), "unknown authentication mode %q", session.selectedMode)
		return AuthDenied, unexpectedErrMsg("unknown authentication mode")
	}
}

func (b *Broker) deviceAuth(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	response := session.deviceAuthResponse
	if response == nil {
		log.Error(context.Background(), "device auth response is not set")
		return AuthDenied, unexpectedErrMsg("device auth response is not set")
	}

	if response.Expiry.IsZero() {
		response.Expiry = time.Now().Add(time.Hour)
		log.Debugf(context.Background(), "Device code does not have an expiry time, using default of %s", response.Expiry)
	} else {
		log.Debugf(context.Background(), "Device code expiry time: %s", response.Expiry)
	}
	expiryCtx, cancel := context.WithDeadline(ctx, response.Expiry)
	defer cancel()

	// The default interval is 5 seconds, which means the user has to wait up to 5 seconds after
	// successful authentication. We're reducing the interval to 1 second to improve UX a bit.
	response.Interval = 1

	log.Debug(ctx, "Polling to exchange device code for token...")
	t, err := session.oauth2Config.DeviceAccessToken(expiryCtx, response, b.provider.AuthOptions()...)
	if err != nil {
		log.Errorf(context.Background(), "Error retrieving access token: %s", err)
		return AuthRetry, errorMessage{Message: "Error retrieving access token. Please try again."}
	}
	log.Debug(ctx, "Exchanged device code for token.")

	if t.RefreshToken == "" {
		log.Warningf(context.Background(), "No refresh token returned for user during device code flow. You might have to add the 'offline_access' scope to the 'extra_scopes' setting.")
	}

	rawIDToken, ok := t.Extra("id_token").(string)
	if !ok {
		log.Error(context.Background(), "token response does not contain an ID token")
		return AuthDenied, unexpectedErrMsg("token response does not contain an ID token")
	}

	authInfo, access, data := b.populateAuthInfo(ctx, session, t, rawIDToken, nil)
	if authInfo == nil {
		return access, data
	}

	// Load existing device registration data if there is any, to avoid re-registering the device.
	var deviceRegistrationData []byte
	if oldAuthInfo, err := token.LoadAuthInfo(session.tokenPath); err == nil {
		deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}
	if authInfo.UserInfo.ProviderID != "" && session.providerID == "" {
		b.ensureProviderIDCacheDir(session, authInfo.UserInfo.ProviderID)
	}

	cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, t, deviceRegistrationData)
	defer cleanup()
	if access != "" {
		return access, data
	}

	// We can only fetch the groups after registering the device, because the token acquired for device registration
	// cannot be used with the Microsoft Graph API and a new token must be acquired for the Graph API.
	authInfo.UserInfo.Groups, err = b.getGroups(ctx, session, authInfo)
	if err != nil {
		log.Errorf(context.Background(), "failed to get groups: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Failed to retrieve groups from Microsoft Graph API")
	}

	// Store the auth info in the session so that we can use it when handling the
	// next IsAuthenticated call for the new password mode.
	session.authInfo = authInfo
	session.nextAuthModes = []string{authmodes.NewPassword}

	return AuthNext, nil
}

func (b *Broker) passwordAuth(ctx context.Context, session *session, secret string) (string, isAuthenticatedDataResponse) {
	ok, err := password.CheckPassword(secret, session.passwordPath)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return AuthDenied, unexpectedErrMsg("could not check password")
	}
	if !ok {
		log.Noticef(context.Background(), "Authentication failure: incorrect local password for user %q", session.username)
		return AuthRetry, errorMessage{Message: "Incorrect password, please try again."}
	}

	authInfo, err := token.LoadAuthInfo(session.tokenPath)
	if err != nil {
		log.Error(context.Background(), err.Error())
		return AuthDenied, unexpectedErrMsg("could not load stored token")
	}

	// If the session is for changing the password, we don't need to refresh the token and user info (and we don't
	// want the method call to return an error if refreshing the token or user info fails).
	if session.mode == sessionmode.ChangePassword || session.mode == sessionmode.ChangePasswordOld {
		// Store the auth info in the session so that we can use it when handling the
		// next IsAuthenticated call for the new password mode.
		session.authInfo = authInfo
		session.nextAuthModes = []string{authmodes.NewPassword}
		return AuthNext, nil
	}

	// Refresh the token on every online login (even if it has not expired) to
	// re-verify the account with the provider. This refresh is also the live
	// disabled/revoked-user check. Tokens obtained via the Entra auth flow are issued by the
	// Microsoft Broker App and are refreshed as a public client (no client_secret)
	// via the provider; all other tokens use the OIDC app refresh. Both paths feed
	// the same error classification below.
	if b.cfg.forceAccessCheckWithProvider || !session.isOffline {
		oldAuthInfo := authInfo
		// Both refresh paths use the cached refresh token; without one we can't
		// perform the liveness check, so require re-authentication.
		if authInfo.Token.RefreshToken == "" {
			log.Warningf(context.Background(), "No refresh token available for user %q", session.username)
			session.nextAuthModes = reauthModes
			return AuthNext, errorMessage{Message: "Remote authentication failed: No refresh token. Please contact your administrator."}
		}
		if authInfo.ObtainedViaEntraAuth {
			authInfo, err = b.refreshEntraToken(ctx, session, authInfo)
		} else {
			authInfo, err = b.refreshToken(ctx, session, authInfo)
		}
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) {
			if isAADSTSGrantRevokedError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token revoked for user %q after a remote password change/reset", session.username)
				b.invalidateCachedCredentials(session)
				session.nextAuthModes = reauthModes
				return AuthNext, errorMessage{Message: "Your password was changed remotely. Please re-authenticate."}
			}
			if b.provider.IsTokenExpiredError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token expired for user %q, re-authentication required", session.username)
				session.nextAuthModes = reauthModes
				return AuthNext, errorMessage{Message: "Refresh token expired, please authenticate again."}
			}
			if udc, ok := providers.ProviderAs[providers.UserDisabledChecker](b.provider); ok && udc.IsUserDisabledError(retrieveErr) {
				log.Error(context.Background(), retrieveErr.Error())
				log.Errorf(context.Background(), "Login denied: user %q is disabled in %s", session.username, b.provider.DisplayName())

				// Store the information that the user is disabled, so that we can deny login on subsequent offline attempts.
				oldAuthInfo.UserIsDisabled = true
				if err = token.CacheAuthInfo(session.tokenPath, oldAuthInfo); err != nil {
					log.Errorf(context.Background(), "Failed to store token: %s", err)
					return AuthDenied, unexpectedErrMsg("failed to store token")
				}

				return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
			}
		}
		if err != nil {
			log.Errorf(context.Background(), "Failed to refresh token: %s", err)

			// Fall back to offline mode for transient network failures (e.g. timeout, DNS,
			// connection refused). Unless provider authentication is forced.
			var netErr net.Error
			if errors.As(err, &netErr) && !b.cfg.forceAccessCheckWithProvider {
				log.Warningf(context.Background(), "Network error during token refresh for user %q, skipping token refresh", session.username)
				authInfo = oldAuthInfo
				session.isOffline = true
			} else {
				return AuthDenied, errorMessage{Message: "Failed to refresh token"}
			}
		}
	}

	// Check disabled status. We have to do this after trying to refresh the token,
	// because if token refresh fails with a network error, the session falls back
	// to offline mode.
	if authInfo.UserIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: user %q is disabled in %s and session is offline", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s. Please contact your administrator or try again with a working network connection.", b.provider.DisplayName())}
	}

	if authInfo.DeviceIsDisabled && session.isOffline {
		log.Errorf(context.Background(), "Login denied: device %q is disabled in %s and session is offline", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("This device is disabled in %s. Please contact your administrator or try again with a working network connection.", b.provider.DisplayName())}
	}

	// If device registration is enabled, ensure that the device is registered.
	// Skipped when offline: registration requires a live provider connection.
	if !session.isOffline {
		cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, authInfo.Token, authInfo.DeviceRegistrationData)
		defer cleanup()
		if access != "" {
			return access, data
		}
	}

	// Try to refresh the groups
	groups, err := b.getGroups(ctx, session, authInfo)
	if errors.Is(err, providerErrors.ErrDeviceDisabled) {
		// The device is disabled, deny login
		log.Errorf(context.Background(), "Login denied: device is disabled in %s for user %q", b.provider.DisplayName(), session.username)

		// Store the information that the device is disabled, so that we can deny login on subsequent offline attempts.
		authInfo.DeviceIsDisabled = true
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		return AuthDenied, errorMessage{Message: fmt.Sprintf("This device is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
	}
	if errors.Is(err, providerErrors.ErrInvalidRedirectURI) {
		// Deny login if the redirect URI is invalid, so that users and administrators are aware of the issue.
		log.Errorf(context.Background(), "Login denied: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Invalid redirect URI")
	}
	var retryWithDeviceAuthError *providerErrors.RetryWithDeviceAuthError
	if errors.As(err, &retryWithDeviceAuthError) {
		log.Errorf(context.Background(), "Token acquisition failed: %s. Try again using the device code flow.", err)
		// The token acquisition failed unexpectedly.
		// One possible reason is that the device was deleted by an administrator in Entra ID.
		// In this case, the user can perform device code flow again to get a new token
		// and register the device again, allowing the user to log in.
		// We delete the device registration data to cause device code flow to re-register the device.
		authInfo.DeviceRegistrationData = nil
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		session.nextAuthModes = reauthModes
		msg := "Authentication failed due to a token issue. Please try again."
		return AuthNext, errorMessage{Message: msg}
	}
	if err != nil {
		// We couldn't fetch the groups, but we have valid cached ones. The live
		// provider check (and force_access_check_with_provider enforcement) happens
		// at the token refresh above, the same as the device-auth flow, so a
		// group-fetch failure here falls back to cached groups for both flows.
		log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
	} else {
		authInfo.UserInfo.Groups = groups
	}

	return b.finishAuth(session, authInfo)
}

func (b *Broker) entraAuth(ctx context.Context, session *session, userPassword string) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_auth mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra authentication")
	}
	passwordSubmitted := userPassword != ""
	if session.entraAuthPasswordRequired && !passwordSubmitted {
		return AuthRetry, errorMessage{Message: "Please enter your Entra ID password."}
	}

	// A prior MFA flow may still be active if the password step is restarted
	// (e.g. the user navigates back to re-enter the password). Release it before
	// starting a new one so the libhimmelblau continuation it owns is not leaked.
	clearEntraAuthState(session)

	// Load the cached auth info once at the start of the flow and stash it on the
	// session, so the second step (entra_auth_wait/entra_auth_code → finishEntraAuth)
	// reuses it instead of re-reading the token from disk on every call.
	//
	// A load error is non-fatal: it is expected on a first login (no cached token
	// yet), and for any other reason (e.g. an unreadable token) the flow can still
	// proceed by treating it as "no prior device data". A nil session.authInfo is
	// the correct state in both cases; log it for visibility.
	cachedAuthInfo, err := token.LoadAuthInfo(session.tokenPath)
	if err != nil {
		log.Debugf(context.Background(), "No cached auth info for user %q (first login or unreadable token): %v", session.username, err)
	}
	session.authInfo = cachedAuthInfo

	// Existing device registration data for the Entra auth flow (from the cached info).
	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	// Use device-scoped MFA only after a real password has been submitted.
	// Passwordless probing passes a NULL password to libhimmelblau, and the
	// native device/enrollment flow cannot operate without password-derived
	// material.
	withDeviceScope := passwordSubmitted && (b.cfg.registerDevice || himmelblau.ValidDeviceRegistrationDataJSON(deviceRegistrationData))

	// Advertise FIDO assertion capability whenever this build can perform a
	// local WebAuthn assertion at all, even if no key is currently plugged in.
	// libhimmelblau forwards this as isFidoSupported to Entra, which controls
	// whether it fetches the WebAuthn challenge; without it, Entra reports
	// PASSWORD_REQUIRED for unplugged-key sessions and passwordless FIDO-only
	// accounts are misrouted to a password prompt. The actual local-vs-remote
	// gate is entraAuthFidoAuth, which waits for a key up to fidoDeviceWaitTimeout
	// and then falls back to the device code flow, so a headless/SSH session
	// where no key can appear is not stranded.
	var authOpts []himmelblau.AuthOption
	if b.fido != nil {
		authOpts = append(authOpts, himmelblau.AuthOptionFido)
	}

	flow, challengeInfo, err := entraProvider.InitiateEntraAuth(ctx, b.cfg.clientID, b.cfg.issuerURL, session.username, userPassword, deviceRegistrationData, withDeviceScope, authOpts...)
	if err != nil {
		var mfaErr *himmelblau.MFAError
		if errors.As(err, &mfaErr) {
			return b.routeMFAInitError(mfaErr, session)
		}
		// A non-MFAError here is unexpected (the provider should classify expected
		// failures as MFAError); surface it as a reportable bug.
		log.Errorf(context.Background(), "Entra authentication failed: %v", err)
		return AuthDenied, unexpectedErrMsg("failed to initiate Entra authentication flow")
	}
	if flow == nil || challengeInfo == nil {
		himmelblau.FreeMFAFlowState(flow)
		log.Error(context.Background(), "Entra authentication did not return a complete MFA challenge")
		return AuthDenied, unexpectedErrMsg("provider returned incomplete MFA challenge")
	}

	// InitiateEntraAuth is a non-preemptible cgo call; if the request was
	// cancelled while it was in flight, IsAuthenticated already returned via its
	// ctx.Done() branch without persisting this session update. Stashing the flow
	// on session at that point would make it unreachable, leaking the native
	// continuation state it owns, so free it immediately instead (same reasoning
	// as the equivalent check in finishEntraAuth).
	if ctx.Err() != nil {
		himmelblau.FreeMFAFlowState(flow)
		log.Noticef(context.Background(), "Entra authentication succeeded but the request was cancelled; discarding MFA flow for user %q", session.username)
		return AuthCancelled, nil
	}

	session.mfaFlowActive = flow
	session.mfaChallengeInfo = challengeInfo
	session.entraAuthPasswordRequired = false
	session.entraAuthPasswordHash = ""

	if passwordSubmitted {
		// Hash the password immediately to narrow the plaintext memory window.
		// The hash is written to disk in finishEntraAuth after MFA succeeds.
		passwordHash, hashErr := password.HashPassword(userPassword)
		if hashErr != nil {
			log.Errorf(context.Background(), "Failed to hash password: %v", hashErr)
			clearEntraAuthState(session)
			return AuthDenied, unexpectedErrMsg("failed to process password")
		}
		session.entraAuthPasswordHash = passwordHash
	}

	return b.routeMFAChallenge(session, challengeInfo)
}

// routeMFAChallenge inspects the MFA challenge negotiated by the password
// entry mode and sets the session's next auth modes to the matching
// follow-up: the local security-key ceremony (or its Device
// Authentication fallback) for FIDO methods, code entry for prompt methods,
// and the out-of-band poll for the rest.
func (b *Broker) routeMFAChallenge(session *session, challengeInfo *himmelblau.MFAChallengeInfo) (string, isAuthenticatedDataResponse) {
	mfaMethod := challengeInfo.Method
	pollingInterval := challengeInfo.PollingIntervalMs

	if isFIDOMethod(mfaMethod) {
		return b.routeFIDOChallenge(session, challengeInfo)
	}

	switch {
	case isPromptMethod(mfaMethod):
		// Code-entry MFA: user must type a code (OTP, SMS, etc.).
		session.nextAuthModes = []string{authmodes.EntraAuthCode}
	case isPollMethod(mfaMethod):
		// Poll-based MFA: approval happens out of band (push notification or
		// phone call), so wait and poll. The poll loop applies a default
		// interval if the challenge does not carry a positive one.
		session.nextAuthModes = []string{authmodes.EntraAuthWait}
	case pollingInterval > 0:
		// Unknown method: a polling interval hints that approval happens out of band.
		log.Warningf(context.Background(), "Unknown MFA method %q with polling interval %dms, treating it as a poll-based method", mfaMethod, pollingInterval)
		session.nextAuthModes = []string{authmodes.EntraAuthWait}
	default:
		log.Warningf(context.Background(), "Unknown MFA method %q without a polling interval, treating it as a code-entry method", mfaMethod)
		session.nextAuthModes = []string{authmodes.EntraAuthCode}
	}

	return AuthNext, nil
}

func clearEntraAuthState(session *session) {
	himmelblau.FreeMFAFlowState(session.mfaFlowActive)
	session.mfaFlowActive = nil
	session.mfaChallengeInfo = nil
	session.fidoPIN = ""
}

// restartFromEntraAuth handles a terminal MFA-step failure: it clears the
// now-dead MFA state (including the cached password hash) and directs the
// client back to entra_auth so it can restart the flow rather than
// re-entering a dead follow-up mode, showing msg to the user.
func restartFromEntraAuth(session *session, msg string) (string, isAuthenticatedDataResponse) {
	needsPassword := session.entraAuthPasswordRequired || session.entraAuthPasswordHash != ""
	session.entraAuthPasswordHash = ""
	session.entraAuthPasswordRequired = needsPassword
	clearEntraAuthState(session)
	session.nextAuthModes = []string{authmodes.EntraAuth}
	return AuthNext, errorMessage{Message: msg}
}

// replayCompletedMFA handles a stray IsAuthenticated call for an MFA follow-up
// mode that arrives once mfaFlowActive is already nil: the assertion/poll
// succeeded and chained to a different mode. It replays that stored transition
// (AuthNext) instead of denying an already-successful login; with no pending
// transition it denies. These flows are single-use, so replaying the stored
// transition is the only safe response to a duplicate call.
func replayCompletedMFA(session *session, mode string) (string, isAuthenticatedDataResponse) {
	if len(session.nextAuthModes) > 0 && !slices.Contains(session.nextAuthModes, mode) {
		log.Debugf(context.Background(), "%q mode selected again for user %q after already completing; replaying transition to %v", mode, session.username, session.nextAuthModes)
		return AuthNext, nil
	}
	log.Errorf(context.Background(), "%q mode selected but no active MFA flow", mode)
	return AuthDenied, unexpectedErrMsg("no active MFA flow")
}

// denyAndClearMFA frees the MFA flow and wipes the cached password hash, then
// denies with data. Terminal FIDO/MFA failures use it; success paths keep the
// hash for offline caching, so clearEntraAuthState alone must not wipe it.
func denyAndClearMFA(session *session, data isAuthenticatedDataResponse) (string, isAuthenticatedDataResponse) {
	session.entraAuthPasswordHash = ""
	clearEntraAuthState(session)
	return AuthDenied, data
}

// cachedDeviceRegistrationData returns the device registration data from the
// session's cached auth info (loaded once at the start of the flow by
// entraAuth), or nil if there is no cached token or it carries none.
func (b *Broker) cachedDeviceRegistrationData(session *session) []byte {
	if session.authInfo != nil {
		return session.authInfo.DeviceRegistrationData
	}
	return nil
}

func (b *Broker) entraAuthWaitAuth(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_auth_wait mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra MFA")
	}

	if session.mfaFlowActive == nil {
		return replayCompletedMFA(session, authmodes.EntraAuthWait)
	}
	if session.mfaChallengeInfo == nil {
		log.Error(context.Background(), "MFA wait mode selected but no MFA challenge metadata is available")
		return AuthDenied, unexpectedErrMsg("no active MFA challenge")
	}

	maxAttempts := session.mfaChallengeInfo.MaxPollAttempts

	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	pollCtx, pollCancel := context.WithTimeout(ctx, maxMFAPollDuration)
	defer pollCancel()

	// The first poll attempt is 1 for Himmelblau's poll-based MFA flow.
	// maxAttempts <= 0 means "no usable attempt budget from the challenge": -1 is
	// libhimmelblau's "no max defined", and 0 can result from its
	// expires_in/polling_interval integer division when expires_in < polling_interval.
	// In both cases poll until the wall-clock cap above rather than skipping every
	// poll and reporting an immediate (false) timeout.
	for attempt := 1; maxAttempts <= 0 || attempt <= maxAttempts; attempt++ {
		oauthToken, err := entraProvider.AcquireTokenByMFAFlow(
			pollCtx, b.cfg.clientID, b.cfg.issuerURL, session.username,
			session.mfaFlowActive, "", attempt,
			deviceRegistrationData,
		)
		if err != nil {
			var mfaErr *himmelblau.MFAError
			if errors.As(err, &mfaErr) && mfaErr.IsMFAPollContinue() {
				// MFA not yet approved, keep polling.
				pollingInterval := session.mfaChallengeInfo.PollingIntervalMs
				if pollingInterval <= 0 {
					pollingInterval = 1000
				}
				select {
				case <-pollCtx.Done():
					return b.endExpiredMFAPoll(ctx, session)
				case <-time.After(time.Duration(pollingInterval) * time.Millisecond):
					continue
				}
			}
			// A user denial is terminal — handle it first, even if our poll
			// deadline happened to elapse during this (non-preemptible) call.
			if errors.As(err, &mfaErr) && mfaErr.IsMFADenied() {
				session.entraAuthPasswordHash = ""
				clearEntraAuthState(session)
				log.Noticef(context.Background(), "MFA authentication denied for user %q", session.username)
				return AuthDenied, errorMessage{Message: "MFA authentication was denied."}
			}
			// AcquireTokenByMFAFlow is a non-preemptible CGo call: our poll
			// deadline (or the caller's cancellation) can elapse while it is in
			// flight, after which it returns a generic error rather than a poll
			// continuation. Report that as the timeout/cancellation it really is,
			// keeping the underlying error in the log for diagnosis.
			if pollCtx.Err() != nil {
				log.Errorf(context.Background(), "MFA poll error at deadline for user %q: %v", session.username, err)
				return b.endExpiredMFAPoll(ctx, session)
			}
			// Genuine MFA failure.
			log.Errorf(context.Background(), "MFA poll failed: %v", err)
			return restartFromEntraAuth(session, "MFA authentication failed. Please try again.")
		}

		// MFA approved — finish auth.
		clearEntraAuthState(session)
		return b.finishEntraAuth(ctx, session, oauthToken)
	}

	// Max poll attempts exceeded.
	return b.endExpiredMFAPoll(ctx, session)
}

// endExpiredMFAPoll handles a poll-loop exit caused by the internal poll
// deadline elapsing, the caller cancelling the request, or the maximum number
// of poll attempts being exhausted. It clears the now-dead MFA state and directs
// the client back to entra_auth so it can restart the flow, distinguishing a
// caller cancellation (AuthCancelled) from a wall-clock timeout (AuthNext).
func (b *Broker) endExpiredMFAPoll(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	if ctx.Err() != nil {
		// The whole IsAuthenticated request was cancelled by the caller, which
		// is usually transient (e.g. GDM re-selecting the wait mode). Like the
		// FIDO assertion path, do NOT free the flow here: IsAuthenticated
		// returns via its ctx.Done() branch and skips updateSession, so the
		// stored session keeps its mfaFlowActive pointer. A cancelled poll does
		// not consume the flow (libhimmelblau only advances it on success), so
		// leaving it intact lets the resumed poll keep waiting for the same MFA
		// approval instead of dead-ending on a released flow and looping back to
		// the password probe. A genuinely terminal cancel is handled by
		// EndSession, which frees the flow.
		log.Noticef(context.Background(), "MFA poll cancelled for user %q", session.username)
		return AuthCancelled, nil
	}
	log.Noticef(context.Background(), "MFA poll timed out for user %q", session.username)
	return restartFromEntraAuth(session, "MFA approval timed out. Please try again.")
}

func (b *Broker) entraAuthCodeAuth(ctx context.Context, session *session, code string) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_auth_code mode selected but provider does not support it")
		return AuthDenied, unexpectedErrMsg("provider does not support Entra MFA")
	}

	if session.mfaFlowActive == nil {
		return replayCompletedMFA(session, authmodes.EntraAuthCode)
	}

	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	oauthToken, err := entraProvider.AcquireTokenByMFAFlow(
		ctx, b.cfg.clientID, b.cfg.issuerURL, session.username,
		session.mfaFlowActive, code, 0,
		deviceRegistrationData,
	)
	if err != nil {
		var mfaErr *himmelblau.MFAError
		if errors.As(err, &mfaErr) && mfaErr.IsMFADenied() {
			log.Noticef(context.Background(), "MFA code verification denied for user %q", session.username)
			session.entraAuthPasswordHash = ""
			clearEntraAuthState(session)
			return AuthDenied, errorMessage{Message: "MFA authentication was denied."}
		}
		if errors.As(err, &mfaErr) && mfaErr.IsMFARetryableCode() {
			// An incorrect or expired one-time code: re-prompt for the code
			// rather than discarding the flow and forcing password re-entry.
			// The MFA flow remains valid on this path (libhimmelblau only
			// advances flow.ctx/flow_token on success), so the next code
			// submission reuses it. AuthRetry stays on the entra_auth_code mode
			// and is capped by maxAuthAttempts, so repeated wrong codes still
			// end in denial.
			log.Noticef(context.Background(), "Incorrect MFA code for user %q, re-prompting", session.username)
			return AuthRetry, errorMessage{Message: "Incorrect or expired code. Please try again."}
		}
		log.Noticef(context.Background(), "MFA code verification failed for user %q: %v", session.username, err)
		return restartFromEntraAuth(session, "MFA authentication failed. Please try again.")
	}

	clearEntraAuthState(session)
	return b.finishEntraAuth(ctx, session, oauthToken)
}

// routeFIDOChallenge decides how to continue when Entra ID selected a
// FIDO/security-key method. When this build can perform the assertion and the
// challenge carries the WebAuthn data, continue with the local FIDO modes even
// if no key is plugged in yet: the entra_auth_fido step waits (bounded by
// fidoDeviceWaitTimeout) for the user to insert and touch it, then falls back
// to the device code flow on a machine where no key appears. Redirect to the
// device code flow immediately only when local FIDO is impossible at all (no
// authenticator in this build, or Entra sent no WebAuthn challenge).
func (b *Broker) routeFIDOChallenge(session *session, challengeInfo *himmelblau.MFAChallengeInfo) (string, isAuthenticatedDataResponse) {
	if challengeInfo.FidoChallenge == "" || b.fido == nil {
		log.Noticef(context.Background(), "FIDO MFA method %q for user %q cannot be completed locally; redirecting to the device code flow", challengeInfo.Method, session.username)
		return b.redirectFIDOToDeviceAuth(session)
	}

	// When a key is already connected and needs a PIN, collect it before the
	// touch. When none is connected yet, go straight to the assertion step: it
	// waits for insertion, and a PIN is requested reactively (ErrPINRequired)
	// if the key the user eventually inserts needs one.
	if b.fido.DevicePresent() {
		pinRequired, err := b.fido.DeviceRequiresPIN()
		if err != nil {
			log.Warningf(context.Background(), "Could not determine whether the security key requires a PIN: %v", err)
		}
		if pinRequired && session.fidoPIN == "" {
			session.nextAuthModes = []string{authmodes.EntraAuthFidoPin}
			return AuthNext, nil
		}
	}
	session.nextAuthModes = []string{authmodes.EntraAuthFido}
	return AuthNext, nil
}

// redirectFIDOToDeviceAuth clears the MFA state and directs the client to
// the device code flow (or denies when that flow is disabled). It is the
// fallback for FIDO challenges that cannot be completed on this machine.
func (b *Broker) redirectFIDOToDeviceAuth(session *session) (string, isAuthenticatedDataResponse) {
	session.entraAuthPasswordHash = ""
	clearEntraAuthState(session)
	if b.cfg.flows.DeviceAuth {
		session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
		return AuthNext, errorMessage{Message: "This account requires FIDO/security key authentication. Please complete authentication using the device code flow."}
	}
	return AuthDenied, errorMessage{Message: "This account requires FIDO/security key authentication, which is not available in this mode. The device code flow is also unavailable. Please contact your administrator."}
}

// failFIDOAssertion handles a local ceremony that could not be completed. A
// passwordless session has no validated password to restart from, and
// re-probing would just loop back to the same failing local assertion, so it
// falls back to the device code flow where Entra runs the ceremony in a
// browser; a password session restarts the password flow instead.
func (b *Broker) failFIDOAssertion(session *session) (string, isAuthenticatedDataResponse) {
	if session.entraAuthPasswordHash == "" {
		return b.redirectFIDOToDeviceAuth(session)
	}
	return restartFromEntraAuth(session, "Security key authentication failed. Please try again.")
}

// entraAuthFidoPinAuth stores the security key PIN on the session and advances
// to the assertion mode. The PIN never leaves broker memory: it is passed to
// the local WebAuthn ceremony and cleared with the MFA state.
func (b *Broker) entraAuthFidoPinAuth(session *session, pin string) (string, isAuthenticatedDataResponse) {
	if session.mfaFlowActive == nil {
		return replayCompletedMFA(session, authmodes.EntraAuthFidoPin)
	}
	if pin == "" {
		return AuthRetry, errorMessage{Message: "Please enter your security key PIN."}
	}

	session.fidoPIN = pin
	session.nextAuthModes = []string{authmodes.EntraAuthFido}
	return AuthNext, nil
}

// fidoDevicePollInterval is how often entraAuthFidoAuth re-checks for an
// inserted security key while showing the "insert your security key" screen.
const fidoDevicePollInterval = 500 * time.Millisecond

// fidoDeviceWaitTimeout bounds how long entraAuthFidoAuth waits for a key to be
// inserted before falling back to the device code flow. A headless or SSH
// session cannot attach a security key, so without a bound the "insert your
// security key" screen would block forever; the timeout gives an interactive
// user time to plug the key in while still failing over on a machine where none
// can appear. It is a var so tests can shorten it.
var fidoDeviceWaitTimeout = 60 * time.Second

// entraAuthFidoAuth performs the WebAuthn assertion with the local security
// key and completes the MFA flow with the resulting assertion.
func (b *Broker) entraAuthFidoAuth(ctx context.Context, session *session) (string, isAuthenticatedDataResponse) {
	entraProvider, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		log.Error(context.Background(), "entra_auth_fido mode selected but provider does not support it")
		return denyAndClearMFA(session, unexpectedErrMsg("provider does not support Entra MFA"))
	}
	if b.fido == nil {
		log.Error(context.Background(), "entra_auth_fido mode selected but this build has no FIDO support")
		return denyAndClearMFA(session, unexpectedErrMsg("FIDO authentication is not available"))
	}
	if session.mfaFlowActive == nil {
		return replayCompletedMFA(session, authmodes.EntraAuthFido)
	}
	if session.mfaChallengeInfo == nil || session.mfaChallengeInfo.FidoChallenge == "" {
		log.Error(context.Background(), "FIDO mode selected but no FIDO challenge is available")
		return denyAndClearMFA(session, unexpectedErrMsg("no active FIDO challenge"))
	}

	// The entra_auth_fido screen says "Insert your security key and touch it", so
	// wait here for a key to be connected rather than failing over immediately
	// when none is plugged in yet. Give up after fidoDeviceWaitTimeout and fall
	// back to the device code flow: a headless/SSH session can never attach a
	// key, and blocking forever would strand the login. Cancellation (user abort
	// or session end) unwinds like a cancelled assertion: return without freeing
	// the flow, so a resumed attempt reuses it (see the ErrCanceled note in
	// routeFIDOAssertionError).
	if !b.fido.DevicePresent() {
		waitDeadline := time.NewTimer(fidoDeviceWaitTimeout)
		defer waitDeadline.Stop()
		pollTicker := time.NewTicker(fidoDevicePollInterval)
		defer pollTicker.Stop()
		for !b.fido.DevicePresent() {
			select {
			case <-ctx.Done():
				log.Noticef(context.Background(), "Security key wait cancelled for user %q", session.username)
				return AuthCancelled, nil
			case <-waitDeadline.C:
				log.Noticef(context.Background(), "No security key inserted for user %q within %s; falling back to the device code flow", session.username, fidoDeviceWaitTimeout)
				return b.redirectFIDOToDeviceAuth(session)
			case <-pollTicker.C:
			}
		}
	}

	assertion, err := b.fido.Assert(ctx, session.mfaChallengeInfo.FidoChallenge, session.mfaChallengeInfo.FidoAllowList, session.fidoPIN)
	if err != nil {
		return b.routeFIDOAssertionError(ctx, session, err)
	}

	deviceRegistrationData := b.cachedDeviceRegistrationData(session)

	oauthToken, err := entraProvider.AcquireTokenByMFAFlow(
		ctx, b.cfg.clientID, b.cfg.issuerURL, session.username,
		session.mfaFlowActive, assertion, 0,
		deviceRegistrationData,
	)
	if err != nil {
		var mfaErr *himmelblau.MFAError
		if errors.As(err, &mfaErr) && mfaErr.IsMFADenied() {
			log.Noticef(context.Background(), "FIDO MFA denied for user %q", session.username)
			return denyAndClearMFA(session, errorMessage{Message: "MFA authentication was denied."})
		}
		log.Noticef(context.Background(), "FIDO assertion was rejected for user %q: %v", session.username, err)
		return b.failFIDOAssertion(session)
	}

	clearEntraAuthState(session)
	return b.finishEntraAuth(ctx, session, oauthToken)
}

// routeFIDOAssertionError maps a failed local WebAuthn ceremony to the next
// broker step. PIN problems route to the PIN mode (bounded by the device,
// which hard-blocks its PIN after a few consecutive failures), a missed touch
// is retriable on the same mode, and everything else restarts the flow.
func (b *Broker) routeFIDOAssertionError(ctx context.Context, session *session, err error) (string, isAuthenticatedDataResponse) {
	switch {
	case errors.Is(err, fido.ErrPINRequired):
		session.fidoPIN = ""
		session.nextAuthModes = []string{authmodes.EntraAuthFidoPin}
		return AuthNext, errorMessage{Message: "Your security key requires a PIN."}
	case errors.Is(err, fido.ErrPINInvalid):
		log.Noticef(context.Background(), "Incorrect security key PIN for user %q, re-prompting", session.username)
		session.fidoPIN = ""
		session.nextAuthModes = []string{authmodes.EntraAuthFidoPin}
		return AuthNext, errorMessage{Message: "Incorrect security key PIN. Please try again."}
	case errors.Is(err, fido.ErrPINBlocked):
		log.Noticef(context.Background(), "Security key PIN blocked for user %q", session.username)
		if session.entraAuthPasswordHash == "" {
			return b.redirectFIDOToDeviceAuth(session)
		}
		return denyAndClearMFA(session, errorMessage{Message: "The security key PIN is blocked. Remove and reinsert the key, then try again."})
	case errors.Is(err, fido.ErrTimeout):
		// The MFA flow is still valid (nothing was sent to Entra ID), so the
		// user can retry the touch on the same mode. AuthRetry is capped by
		// maxAuthAttempts, so a never-touched key still ends in denial.
		return AuthRetry, errorMessage{Message: "The security key was not touched in time. Please try again."}
	case errors.Is(err, fido.ErrCanceled) || ctx.Err() != nil:
		// A cancelled assertion is almost always transient: the PAM client
		// dropped the current conversation (e.g. GDM re-selecting the mode) and
		// resumes the SAME session. The local WebAuthn ceremony does not consume
		// the MFA flow, so leave it intact for the resumed attempt to reuse.
		//
		// Crucially, do NOT free the flow here. IsAuthenticated returns via its
		// ctx.Done() branch on cancellation and skips updateSession, so any
		// clearing done on this session copy is discarded: the stored session
		// keeps its mfaFlowActive pointer. Freeing the underlying flow would
		// then strand that stored pointer as a released flow, so the resumed
		// assertion fails with "MFA flow state has been released", which
		// restarts from the password probe and loops the user back to FIDO
		// indefinitely. A genuinely terminal cancel is handled by EndSession,
		// which frees the flow itself.
		log.Noticef(context.Background(), "FIDO assertion cancelled for user %q", session.username)
		return AuthCancelled, nil
	case errors.Is(err, fido.ErrNoDevice):
		// The key was unplugged mid-ceremony. Retry on the same mode, which
		// waits for the user to reinsert it, rather than failing over to the
		// device code flow. AuthRetry is capped by maxAuthAttempts.
		log.Noticef(context.Background(), "Security key removed mid-ceremony for user %q; waiting for reinsertion", session.username)
		return AuthRetry, errorMessage{Message: "The security key was removed. Please reinsert it and try again."}
	case errors.Is(err, fido.ErrNoCredentials):
		log.Noticef(context.Background(), "Connected security key has no matching credential for user %q; redirecting to the device code flow", session.username)
		return b.redirectFIDOToDeviceAuth(session)
	default:
		log.Errorf(context.Background(), "FIDO assertion failed for user %q: %v", session.username, err)
		return b.failFIDOAssertion(session)
	}
}

func (b *Broker) finishEntraAuth(ctx context.Context, session *session, mfaToken *oauth2.Token) (string, isAuthenticatedDataResponse) {
	// Ensure any cached password hash is cleared from memory on all exit paths.
	defer func() { session.entraAuthPasswordHash = "" }()

	// AcquireTokenByMFAFlow returns (nil, nil) only on a provider contract
	// violation, but this is the trust boundary into the generic broker: guard
	// against it so a misbehaving provider denies rather than panicking (and
	// taking down the broker process) on the t.Extra dereference below.
	if mfaToken == nil {
		log.Error(context.Background(), "Entra MFA flow completed without returning a token")
		return AuthDenied, unexpectedErrMsg("MFA flow returned no token")
	}

	// handleIsAuthenticated runs in a goroutine; on cancellation IsAuthenticated
	// returns AuthCancelled without awaiting it. If the (non-preemptible) MFA call
	// completed but the request was cancelled in the meantime, stop here rather
	// than registering a device and persisting a token + password file for an
	// authentication the client already abandoned.
	if ctx.Err() != nil {
		log.Noticef(context.Background(), "Entra MFA succeeded but the request was cancelled; not persisting credentials for user %q", session.username)
		return AuthCancelled, nil
	}

	t := mfaToken
	// Reuse the auth info loaded once at the start of the flow (entraAuth)
	// rather than re-reading the token from disk.
	oldAuthInfo := session.authInfo

	// The Entra MFA path does not produce a verified raw OIDC ID token for authd to
	// persist. Carry over a cached RawIDToken from a previous login so we never
	// replace it with an empty one.
	var rawIDToken string
	if oldAuthInfo != nil {
		rawIDToken = oldAuthInfo.RawIDToken
	}

	// The MFA token is issued for the Entra native API audience, so standard OIDC
	// ID token verification (getUserInfo) would fail. Extract user info from the
	// access token after verifying it, then cross-check it against the session
	// username.
	userInfo, err := b.userInfoFromTokenExtras(ctx, session, t)
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}
	authInfo, access, data := b.populateAuthInfo(ctx, session, t, rawIDToken, &userInfo)
	if authInfo == nil {
		return access, data
	}

	// Mark this token as having been obtained via the entra_auth flow so
	// that returning logins refresh it through the Microsoft Broker App public
	// refresh path (the liveness/revocation check) rather than the OIDC app
	// refresh.
	authInfo.ObtainedViaEntraAuth = true

	// Carry over device registration data from a previous login when we are not
	// (re-)registering the device in this one. authInfo is built fresh from the
	// MFA token, so without this the subsequent finishAuth would persist an empty
	// value and silently discard a device that was registered earlier. For a
	// first-time login (no cached token) it keeps its zero value, which is correct.
	if oldAuthInfo != nil {
		authInfo.DeviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}

	var deviceRegistrationData []byte
	if oldAuthInfo != nil {
		deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
	}
	// A successful passwordless MFA flow can still yield a token that is valid
	// for first-time device registration, so do not gate registration on an
	// entered Entra password here.
	cleanup, access, data := b.maybeRegisterDevice(ctx, session, authInfo, t, deviceRegistrationData)
	defer cleanup()
	if access != "" {
		// Keep the existing client-secret group-fetch fallback for first-time
		// passwordless Entra logins in mixed configs: register_device=true should
		// prefer registration, but if it fails before any local device state
		// exists and an app-only Graph path is configured, continue without
		// registration instead of denying the login.
		if session.entraAuthPasswordHash == "" && oldAuthInfo == nil && b.cfg.clientSecret != "" {
			log.Warningf(context.Background(), "Device registration failed for first-time passwordless Entra login for user %q; falling back to app-only Graph lookup", session.username)
		} else {
			return access, data
		}
	}

	// Fetch groups. The MFA flow just performed a live provider verification, so a
	// group-fetch failure here is not a liveness signal: fall back to cached groups
	// on a returning auth, and only deny first-time logins that have no cached groups.
	groups, err := b.getGroups(ctx, session, authInfo)
	if err != nil {
		if oldAuthInfo != nil {
			log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
			authInfo.UserInfo.Groups = oldAuthInfo.UserInfo.Groups
		} else {
			log.Errorf(context.Background(), "failed to get groups: %s", err)
			return AuthDenied, errorMessageForDisplay(err, "Failed to retrieve groups from Microsoft Graph API")
		}
	} else {
		authInfo.UserInfo.Groups = groups
	}

	// A passwordless login has no Entra password to cache for offline
	// authentication. When the user has no local password yet, chain into the
	// newpassword step (like the device-auth flow does) so offline logins
	// keep working; an existing local password stays valid, so returning
	// users are not asked to redefine one on every login.
	if session.entraAuthPasswordHash == "" && !passwordFileExists(*session) {
		session.authInfo = authInfo
		session.nextAuthModes = []string{authmodes.NewPassword}
		return AuthNext, nil
	}

	access, data = b.finishAuth(session, authInfo)
	if access != AuthGranted {
		return access, data
	}

	// Store the pre-computed password hash for offline authentication. This runs
	// after finishAuth so that a denial there cannot leave a password file on
	// disk without a cached token (token-then-password matches the ordering of
	// the device-auth flow).
	if session.entraAuthPasswordHash != "" {
		if hashErr := password.StoreHashedPassword(session.entraAuthPasswordHash, session.passwordPath); hashErr != nil {
			log.Errorf(context.Background(), "Failed to store password hash: %v", hashErr)
			return AuthDenied, unexpectedErrMsg("failed to store password")
		}
		session.entraAuthPasswordHash = ""

		if msg, ok := data.(userInfoMessage); ok {
			msg.Message = cachedPasswordMessage
			data = msg
		}
	}

	return access, data
}

// routeMFAInitError routes the AADSTS errors returned by InitiateEntraAuth
// (the MFA init step) to appropriate broker responses.
func (b *Broker) routeMFAInitError(mfaErr *himmelblau.MFAError, session *session) (string, isAuthenticatedDataResponse) {
	if mfaErr.IsMFAPasswordRequired() {
		log.Debugf(context.Background(), "Passwordless Entra authentication for user %q requires password entry", session.username)
		session.entraAuthPasswordRequired = true
		session.nextAuthModes = []string{authmodes.EntraAuth}
		// Do not send an intermediate AuthNext message here: GDM shows any
		// auth.Next message as a transient challenge state before it requests
		// the next layout, which creates a redundant spinner-only screen with
		// the same label as the real password form. The next selected layout
		// already prompts for the Entra password, so returning AuthNext with no
		// message moves straight to that form.
		return AuthNext, nil
	}

	switch mfaErr.AADSTS {
	case 50053:
		log.Noticef(context.Background(), "Account locked for user %q (AADSTS50053)", session.username)
		return AuthDenied, errorMessage{Message: "Your account is locked. Please try again later or contact your administrator."}
	case 50055:
		log.Noticef(context.Background(), "Entra password expired for user %q", session.username)
		return AuthDenied, errorMessage{Message: "Your password has expired. Please change it via the Entra portal."}
	case 50057:
		log.Noticef(context.Background(), "Login denied: user %q is disabled in %s (AADSTS50057)", session.username, b.provider.DisplayName())
		return AuthDenied, errorMessage{Message: fmt.Sprintf("Your user account is disabled in %s, please contact your administrator.", b.provider.DisplayName())}
	case 50072, 50079, 50203:
		log.Noticef(context.Background(), "MFA enrollment required for user %q (AADSTS%d)", session.username, mfaErr.AADSTS)
		if b.cfg.flows.DeviceAuth {
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "MFA registration required. Please complete setup using the device code flow."}
		}
		return AuthDenied, errorMessage{Message: "MFA registration required, but the device code flow is disabled. Please contact your administrator."}
	case 16000:
		log.Noticef(context.Background(), "Interactive authentication required for user %q (AADSTS16000)", session.username)
		if b.cfg.flows.DeviceAuth {
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "MFA registration required. Please complete setup using the device code flow."}
		}
		return AuthDenied, errorMessage{Message: "MFA registration required, but the device code flow is disabled. Please contact your administrator."}
	case 50126:
		log.Noticef(context.Background(), "Invalid credentials for user %q", session.username)
		return AuthRetry, errorMessage{Message: "Incorrect password, please try again."}
	case 50173:
		log.Noticef(context.Background(), "Password changed remotely for user %q, invalidating cached credentials", session.username)
		b.invalidateCachedCredentials(session)
		session.nextAuthModes = reauthModes
		return AuthNext, errorMessage{Message: "Your password was changed remotely. Please re-authenticate."}
	case 53003:
		log.Noticef(context.Background(), "Conditional Access blocked sign-in for user %q (AADSTS53003)", session.username)
		if b.cfg.flows.DeviceAuth {
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "Access was blocked by your organization's Conditional Access policies. Please complete authentication using the device code flow."}
		}
		return AuthDenied, errorMessage{Message: "Access was blocked by your organization's Conditional Access policies and the device code flow is disabled. Please contact your administrator."}
	default:
		if mfaErr.IsMFARequired() {
			// The native password MFA flow could not be set up; redirect to Device
			// Authentication which handles MFA via a separate flow.
			log.Noticef(context.Background(), "MFA required for user %q; redirecting to the device code flow", session.username)
			if b.cfg.flows.DeviceAuth {
				session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
				return AuthNext, errorMessage{Message: "MFA is required. Please complete authentication using the device code flow."}
			}
			return AuthDenied, errorMessage{Message: "MFA is required but the device code flow is disabled. Please contact your administrator."}
		}
		log.Errorf(context.Background(), "Unhandled AADSTS error %d: %s", mfaErr.AADSTS, mfaErr.Message)
		return AuthDenied, unexpectedErrMsg(mfaErr.Error())
	}
}

func (b *Broker) invalidateCachedCredentials(session *session) {
	for _, path := range []string{session.passwordPath, session.tokenPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warningf(context.Background(), "Failed to remove cached credential %q: %v", path, err)
		}
	}
}

func isAADSTSGrantRevokedError(err *oauth2.RetrieveError) bool {
	if err == nil || err.ErrorCode != "invalid_grant" {
		return false
	}
	return strings.HasPrefix(err.ErrorDescription, "AADSTS50173:")
}

// isFIDOMethod returns true if the MFA method is a FIDO/security key method.
func isFIDOMethod(method string) bool {
	method = strings.ToLower(method)
	return strings.Contains(method, "fido") || strings.Contains(method, "webauthn") || strings.Contains(method, "security_key")
}

// isPromptMethod reports whether the MFA method requires the user to enter a
// code (TOTP, SMS OTP, access-pass, etc.) rather than approve a push
// notification or answer a phone call.
//
// Method identifiers and their UX are derived from libhimmelblau's
// auth.rs MFA branch (third_party/libhimmelblau/src/auth.rs around L3340-L3360):
//   - AccessPass, PhoneAppOTP, OneWaySMS, ConsolidatedTelephony → user types a code (prompt)
//   - PhoneAppNotification, CompanionAppsNotification → push approval (no prompt)
//   - TwoWayVoiceMobile, TwoWayVoiceAlternateMobile, TwoWayVoiceOffice → answer a phone call (no prompt)
//   - FidoKey → handled separately via isFIDOMethod
func isPromptMethod(method string) bool {
	switch method {
	case "AccessPass", "PhoneAppOTP", "OneWaySMS", "ConsolidatedTelephony":
		return true
	}
	return false
}

// isPollMethod reports whether the MFA method is approved out of band (push
// notification or phone call), in which case the broker polls for completion
// instead of prompting the user for a code. See isPromptMethod for where the
// method identifiers come from.
func isPollMethod(method string) bool {
	switch method {
	case "PhoneAppNotification", "CompanionAppsNotification",
		"TwoWayVoiceMobile", "TwoWayVoiceAlternateMobile", "TwoWayVoiceOffice":
		return true
	}
	return false
}

func (b *Broker) finishAuth(session *session, authInfo *token.AuthCachedInfo) (string, isAuthenticatedDataResponse) {
	if b.cfg.shouldRegisterOwner() {
		if err := b.cfg.registerOwner(b.cfg.ConfigFile, authInfo.UserInfo.Name); err != nil {
			// The user is not allowed if we fail to create the owner-autoregistration file.
			// Otherwise the owner might change if the broker is restarted.
			log.Errorf(context.Background(), "Failed to assign the owner role: %v", err)
			return AuthDenied, unexpectedErrMsg("failed to assign the owner role")
		}
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}

	// Append extra groups from config, avoiding duplicates.
	existingGroups := make(map[string]struct{}, len(authInfo.UserInfo.Groups)+len(b.cfg.extraGroups)+len(b.cfg.ownerExtraGroups))
	for _, g := range authInfo.UserInfo.Groups {
		existingGroups[g.Name] = struct{}{}
	}
	for _, name := range b.cfg.extraGroups {
		if _, exists := existingGroups[name]; !exists {
			log.Debugf(context.Background(), "Adding extra group %q", name)
			authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
			existingGroups[name] = struct{}{}
		}
	}
	if b.isOwner(authInfo.UserInfo.Name) {
		for _, name := range b.cfg.ownerExtraGroups {
			if _, exists := existingGroups[name]; !exists {
				log.Debugf(context.Background(), "Adding owner extra group %q", name)
				authInfo.UserInfo.Groups = append(authInfo.UserInfo.Groups, info.Group{Name: name})
				existingGroups[name] = struct{}{}
			}
		}
	}

	if session.isOffline {
		// Skip the migration below when offline: it learns the provider ID from the freshly
		// authenticated user info, which is only available online. NewSession still migrates an
		// offline session when the cached token already carries a provider ID, so this only defers
		// the migration for a legacy cache whose token predates the provider ID (it then migrates on
		// the next online login).
		return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
	}

	// If we are authenticating a cached user without refreshing the token, we might not have the providerID cached yet.
	// So, before migrating it, we need to ensure that we have the information required and that the dir was not
	// migrated yet.
	if authInfo.UserInfo.ProviderID != "" && session.providerID == "" {
		b.ensureProviderIDCacheDir(session, authInfo.UserInfo.ProviderID)
	}

	err := token.CacheAuthInfo(session.tokenPath, authInfo)
	if err != nil && b.cfg.forceAccessCheckWithProvider {
		log.Errorf(context.Background(), "Failed to store token: %s", err)
		return AuthDenied, unexpectedErrMsg("failed to store token")
	}
	if err != nil {
		log.Errorf(context.Background(), "Failed to store token: %s. Continuing with login since provider access check is not forced.", err)
	}

	return AuthGranted, userInfoMessage{UserInfo: authInfo.UserInfo}
}

func (b *Broker) newPassword(session *session, secret string) (string, isAuthenticatedDataResponse) {
	if secret == "" {
		return AuthRetry, unexpectedErrMsg("empty secret")
	}

	// This mode must always come after an authentication mode, so we should have auth info from the previous mode
	// stored in the session.
	authInfo := session.authInfo
	if authInfo == nil {
		log.Error(context.Background(), "auth info is not set")
		return AuthDenied, unexpectedErrMsg("auth info is not set")
	}

	if err := password.HashAndStorePassword(secret, session.passwordPath); err != nil {
		log.Errorf(context.Background(), "Failed to store password: %s", err)
		return AuthDenied, unexpectedErrMsg("failed to store password")
	}

	return b.finishAuth(session, authInfo)
}

// userNameIsAllowed checks whether the user's username is allowed to access the machine.
func (b *Broker) userNameIsAllowed(userName string) bool {
	return b.cfg.userNameIsAllowed(b.provider.NormalizeUsername(userName))
}

// isOwner returns true if the user is the owner of the machine.
func (b *Broker) isOwner(userName string) bool {
	return b.cfg.owner == b.provider.NormalizeUsername(userName)
}

func (b *Broker) userNotAllowedLogMsg(userName string) string {
	logMsg := fmt.Sprintf("User %q is not in the list of allowed users.", userName)
	logMsg += fmt.Sprintf("\nYou can add the user to allowed_users in %s", b.cfg.ConfigFile)
	return logMsg
}

func (b *Broker) startAuthenticate(sessionID string) (context.Context, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	if session.isAuthenticating != nil {
		log.Errorf(context.Background(), "Authentication already running for session %q", sessionID)
		return nil, errors.New("authentication already running for this user session")
	}

	ctx, cancel := context.WithCancel(context.Background())
	session.isAuthenticating = &isAuthenticatedCtx{ctx: ctx, cancelFunc: cancel}

	if err := b.updateSession(sessionID, session); err != nil {
		cancel()
		return nil, err
	}

	return ctx, nil
}

// EndSession ends the session for the user.
func (b *Broker) EndSession(sessionID string) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}

	// Checks if there is a isAuthenticated call running for this session and cancels it before ending the session.
	// Cancelling asks any in-flight goroutine to unwind; we then free the MFA
	// flow ourselves rather than relying on that goroutine to do it. Some
	// cancellation paths intentionally leave the flow intact (e.g. the FIDO
	// assertion, whose cancel is usually a transient re-select and must not
	// strand the resumed session with a released flow), so freeing here is what
	// guarantees the flow is not leaked on a genuinely terminal cancel.
	//
	// Freeing here is safe even when a goroutine is mid-flight: FreeMFAFlowState
	// takes MFAFlowState.mu and nils its release callback, so it waits for any
	// concurrent AcquireTokenByMFAFlow to finish, runs the underlying C free
	// exactly once, and is a no-op if that goroutine also frees the flow on its
	// own terminal path. Sessions are stored by value, so there is no shared
	// write to mfaFlowActive itself (confirmed race-clean under `go test -race`).
	if session.isAuthenticating != nil {
		b.CancelIsAuthenticated(sessionID)
	}
	himmelblau.FreeMFAFlowState(session.mfaFlowActive)

	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()
	delete(b.currentSessions, sessionID)
	return nil
}

// CancelIsAuthenticated cancels the IsAuthenticated call for the user.
func (b *Broker) CancelIsAuthenticated(sessionID string) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return
	}

	if session.isAuthenticating == nil {
		return
	}

	session.isAuthenticating.cancelFunc()
	session.isAuthenticating = nil

	if err := b.updateSession(sessionID, session); err != nil {
		log.Errorf(context.Background(), "Error when cancelling IsAuthenticated: %v", err)
	}
}

// DeleteUser removes all broker side data stored for the given user
// from the broker's data directory. providerID is the stable provider identifier;
// when non-empty it is used to also remove any provider ID-keyed cache directory
// created after the broker cache migration. username is always tried as a
// fallback to support pre-migration caches.
func (b *Broker) DeleteUser(username, providerID string) error {
	var providerIDDir string
	if providerID != "" {
		var err error
		providerIDDir, err = b.userDataDir(providerID)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(providerIDDir); err != nil {
			return fmt.Errorf("could not remove provider ID-keyed user data directory %q: %w", providerIDDir, err)
		}
		log.Debugf(context.Background(), "Deleted broker data for provider ID %q at %q", providerID, providerIDDir)
	}

	if username != "" {
		userDataDir, err := b.userDataDir(username)
		if err != nil {
			return err
		}

		// If the username path is a compatibility symlink created by the provider ID cache
		// migration, os.RemoveAll would only unlink the symlink and leave the real provider ID-keyed
		// directory (token + password) on disk. Resolve the target before removing the symlink so we
		// can also remove the target afterwards.
		var target string
		if info, lstatErr := os.Lstat(userDataDir); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			var evalErr error
			target, evalErr = b.cacheSymlinkTarget(userDataDir)
			if evalErr != nil && !errors.Is(evalErr, os.ErrNotExist) {
				return fmt.Errorf("refusing to follow cache compatibility symlink %q: %w", userDataDir, evalErr)
			}
			if providerIDDir != "" && target != "" && filepath.Clean(target) != filepath.Clean(providerIDDir) {
				log.Warningf(context.Background(), "Not removing cache compatibility symlink target %q for user %q because it does not match provider ID %q", target, username, providerID)
				target = ""
			}
		}

		if err := os.RemoveAll(userDataDir); err != nil {
			return fmt.Errorf("could not remove user data directory %q: %w", userDataDir, err)
		}

		if target != "" && target != userDataDir {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("could not remove user data directory %q: %w", target, err)
			}
		}

		log.Debugf(context.Background(), "Deleted broker data for user %q at %q", username, userDataDir)
	}

	return nil
}

// UserPreCheck checks if the user is valid and can be allowed to authenticate.
// It returns the user info in JSON format if the user is valid, or an empty string if the user is not allowed.
func (b *Broker) UserPreCheck(username string) (string, error) {
	found := false
	for _, suffix := range b.cfg.allowedSSHSuffixes {
		if suffix == "" {
			continue
		}

		// If suffix is only "*", TrimPrefix will return the empty string and that works for the 'match all' case also.
		suffix = strings.TrimPrefix(suffix, "*")
		if strings.HasSuffix(username, suffix) {
			found = true
			break
		}
	}

	if !found {
		// The username does not match any of the allowed suffixes.
		return "", nil
	}

	u := info.NewUser(username, filepath.Join(b.cfg.homeBaseDir, username), "", "", "", nil)
	encoded, err := json.Marshal(u)
	if err != nil {
		return "", fmt.Errorf("could not marshal user info: %v", err)
	}
	return string(encoded), nil
}

// getSession returns the session information for the specified session ID or an error if the session is not active.
func (b *Broker) getSession(sessionID string) (session, error) {
	b.currentSessionsMu.RLock()
	defer b.currentSessionsMu.RUnlock()
	s, active := b.currentSessions[sessionID]
	if !active {
		return session{}, fmt.Errorf("%s is not a current transaction", sessionID)
	}
	return s, nil
}

// updateSession checks if the session is still active and updates the session info.
func (b *Broker) updateSession(sessionID string, session session) error {
	// Checks if the session was ended in the meantime, otherwise we would just accidentally recreate it.
	if _, err := b.getSession(sessionID); err != nil {
		return err
	}
	b.currentSessionsMu.Lock()
	defer b.currentSessionsMu.Unlock()
	b.currentSessions[sessionID] = session
	return nil
}

// refreshEntraToken refreshes a token obtained via the Entra auth flow for the
// liveness/revocation check on a returning login. The provider performs a public
// refresh (no client_secret) as the Microsoft Broker App; on success the rotated
// refresh token replaces the cached one (kept fresh on each login, like the
// device-auth refresh). Errors are returned unwrapped so the caller classifies them
// with the same checks it uses for device-auth (IsUserDisabledError → AADSTS50057,
// IsTokenExpiredError → AADSTS50173, isAADSTSGrantRevokedError, net.Error → offline).
func (b *Broker) refreshEntraToken(ctx context.Context, session *session, oldToken *token.AuthCachedInfo) (*token.AuthCachedInfo, error) {
	ep, ok := providers.ProviderAs[himmelblau.EntraAuthProvider](b.provider)
	if !ok {
		// The token was obtained via the entra_auth flow, so the provider that
		// issued it must implement EntraAuthProvider. If it no longer does, the
		// deployment is misconfigured: fail the login rather than skipping the
		// liveness/revocation check, which would let a deleted/disabled user keep
		// logging in with the cached token.
		return nil, fmt.Errorf("provider does not implement EntraAuthProvider; cannot refresh entra_auth token for user %q", oldToken.UserInfo.Name)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()
	newTok, err := ep.RefreshEntraToken(refreshCtx, b.cfg.issuerURL, oldToken.Token.RefreshToken)
	if err != nil {
		return oldToken, err
	}
	refreshed := *oldToken
	tokenCopy := *oldToken.Token
	refreshed.Token = &tokenCopy
	refreshed.Token.RefreshToken = newTok.RefreshToken
	oldToken = &refreshed
	cacheRotatedToken := func(reason string) {
		if cacheErr := token.CacheAuthInfo(session.tokenPath, oldToken); cacheErr != nil {
			log.Errorf(context.Background(), "Failed to store rotated refresh token after %s: %s", reason, cacheErr)
		}
	}

	// Refresh the cached user info from the verified refreshed access token's
	// claims. Keep the cached gecos if the refreshed token omits one, and keep
	// groups (those are refreshed separately by getGroups). Verification can hit
	// the network itself (JWKS fetch), so give it its own request timeout rather
	// than sharing whatever remains after the token refresh call.
	verifyCtx, verifyCancel := context.WithTimeout(ctx, maxRequestDuration)
	defer verifyCancel()
	if err := ep.VerifyAccessToken(verifyCtx, b.cfg.issuerURL, newTok.AccessToken); err != nil {
		// Refresh-token rotation has already succeeded server-side. Preserve the
		// rotated token even though this login is denied, otherwise a local issue
		// such as clock skew can strand the cache with a dead refresh token.
		cacheRotatedToken("verification failure")
		return oldToken, fmt.Errorf("access token verification failed: %w", err)
	}
	userInfo, err := ep.UserInfoFromAccessToken(newTok.AccessToken)
	if err != nil {
		cacheRotatedToken("user info extraction failure")
		return oldToken, fmt.Errorf("could not refresh user info from the refreshed Entra token: %w", err)
	}
	// getUserInfo (the device-auth refresh path) re-checks this on every refresh,
	// not just on first login; do the same here so a refreshed Entra token can't
	// silently swap the cached identity.
	if err := b.provider.VerifyUsername(session.username, userInfo.Name); err != nil {
		cacheRotatedToken("username verification failure")
		return oldToken, fmt.Errorf("username verification failed: %w", err)
	}
	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}
	if userInfo.Gecos == "" {
		userInfo.Gecos = oldToken.UserInfo.Gecos
	}
	userInfo.Groups = oldToken.UserInfo.Groups
	oldToken.UserInfo = userInfo

	return oldToken, nil
}

// refreshToken refreshes the OAuth2 token and returns the updated AuthCachedInfo.
func (b *Broker) refreshToken(ctx context.Context, session *session, oldToken *token.AuthCachedInfo) (*token.AuthCachedInfo, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()
	// Build a token carrying only the refresh token, like refreshEntraToken
	// does: oauth2.Token.Valid() requires a non-empty AccessToken, so omitting it
	// forces TokenSource to hit the token endpoint even if the cached token has not
	// actually expired, without mutating the caller's cached oldToken.
	oauthToken, err := session.oauth2Config.TokenSource(timeoutCtx, &oauth2.Token{RefreshToken: oldToken.Token.RefreshToken}).Token()
	if err != nil {
		return nil, err
	}
	refreshed := *oldToken
	tokenCopy := *oldToken.Token
	refreshed.Token = &tokenCopy
	if oauthToken.RefreshToken != "" {
		refreshed.Token.RefreshToken = oauthToken.RefreshToken
	}
	oldToken = &refreshed
	cacheRotatedToken := func(reason string) {
		if cacheErr := token.CacheAuthInfo(session.tokenPath, oldToken); cacheErr != nil {
			log.Errorf(context.Background(), "Failed to store rotated refresh token after %s: %s", reason, cacheErr)
		}
	}

	// Update the raw ID token. Treat an absent, null, or empty id_token the same:
	// keep the cached one rather than storing an empty value.
	rawIDToken, _ := oauthToken.Extra("id_token").(string)
	if rawIDToken == "" {
		log.Debug(context.Background(), "refreshed token does not contain an ID token, keeping the old one")
		rawIDToken = oldToken.RawIDToken
	}

	var extraFields map[string]interface{}
	if mp, ok := providers.ProviderAs[providers.MetadataProvider](b.provider); ok {
		extraFields = mp.GetExtraFields(oauthToken)
	}
	t := token.NewAuthCachedInfo(oauthToken, rawIDToken, extraFields)
	t.ProviderMetadata = oldToken.ProviderMetadata
	t.DeviceRegistrationData = oldToken.DeviceRegistrationData

	t.UserInfo, err = b.getUserInfo(ctx, session, oauthToken, rawIDToken, true)
	if err != nil {
		// Token refresh has already succeeded server-side. Preserve a rotated
		// refresh token even if a later local validation step fails, otherwise the
		// cache can be stranded with a refresh token the provider already invalidated.
		cacheRotatedToken("user info refresh failure")
		return oldToken, err
	}
	if t.UserInfo.Gecos == "" {
		t.UserInfo.Gecos = oldToken.UserInfo.Gecos
	}

	t.UserInfo.Groups = oldToken.UserInfo.Groups

	return t, nil
}

// getUserInfo verifies and parses the raw ID token and returns the user info from it.
// If any provider specific mandatory claims are not present in the ID token, the missing claims are
// fetched from the `/userinfo` endpoint and merged before extracting user info.
// Note that verifying the ID token requires a working network connection to the provider's JWKs endpoint,
// so make sure to only call this function if the session is online.
func (b *Broker) getUserInfo(ctx context.Context, session *session, token *oauth2.Token, rawIDToken string, isRefresh bool) (info.User, error) {
	var (
		claims   info.Claimer
		userInfo info.User
		idToken  *oidc.IDToken
		err      error
	)

	if rawIDToken == "" {
		claims, err = session.oidcServer.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil {
			return info.User{}, fmt.Errorf("could not get user info from UserInfo endpoint: %w", err)
		}
	} else {
		var verifyErr error
		idToken, verifyErr = session.oidcServer.Verifier(&b.oidcCfg).Verify(ctx, rawIDToken)
		if verifyErr != nil {
			return info.User{}, fmt.Errorf("could not verify token: %w", verifyErr)
		}
		claims = idToken
	}

	userInfo, err = b.provider.GetUserInfo(claims, isRefresh)
	var missingClaimErr *providerErrors.MissingClaimError
	if rawIDToken != "" && errors.As(err, &missingClaimErr) {
		// The ID token is missing a required claim. Try fetching the claims from the UserInfo endpoint.
		log.Infof(context.Background(), "ID token is missing claim %q. Fetching claims from UserInfo endpoint.", missingClaimErr.Claim)
		var userInfoClaims info.Claimer
		userInfoClaims, err = session.oidcServer.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil {
			return info.User{}, fmt.Errorf("could not get user info from UserInfo endpoint: %w", err)
		}

		// OIDC Core §5.3.2: if the UserInfo response provides a sub claim, it MUST
		// equal the sub from the verified ID token. A mismatch means the /userinfo
		// response is attempting to substitute a different ProviderID and must be
		// rejected to prevent UID takeover.
		var subClaimCheck struct {
			Sub string `json:"sub"`
		}
		if err = userInfoClaims.Claims(&subClaimCheck); err != nil {
			return info.User{}, fmt.Errorf("could not decode UserInfo endpoint claims: %w", err)
		}
		if subClaimCheck.Sub != "" && subClaimCheck.Sub != idToken.Subject {
			return info.User{}, fmt.Errorf("userinfo sub %q does not match ID token sub %q: rejecting potential identity substitution", subClaimCheck.Sub, idToken.Subject)
		}

		// Merge ID token claims with UserInfo claims.
		// UserInfo claims override ID token claims for the same key.
		claims, err = info.NewMergedClaimer(claims, userInfoClaims)
		if err != nil {
			return info.User{}, fmt.Errorf("could not merge ID token and UserInfo endpoint claims: %w", err)
		}
		userInfo, err = b.provider.GetUserInfo(claims, isRefresh)
	}
	if err != nil {
		return info.User{}, err
	}

	if err = b.provider.VerifyUsername(session.username, userInfo.Name); err != nil {
		return info.User{}, fmt.Errorf("username verification failed: %w", err)
	}

	// This means that home was not provided by the claims, so we need to set it to the broker default.
	if !filepath.IsAbs(userInfo.Home) {
		userInfo.Home = filepath.Join(b.cfg.homeBaseDir, userInfo.Home)
	}

	return userInfo, nil
}

// maybeRegisterDevice registers the device when the provider supports it and
// register_device is enabled, updating and persisting authInfo.DeviceRegistrationData.
// regToken is the token used to perform the registration; existingData is any
// previously stored device-registration data, passed to avoid re-registering.
//
// The returned cleanup must be deferred by the caller until AFTER group retrieval,
// because the Graph token exchange depends on the registration state that cleanup
// releases. cleanup is always non-nil (a no-op when nothing was registered), so the
// caller can defer it unconditionally. When access is non-empty the caller must
// return (access, data); an empty access means "proceed".
func (b *Broker) maybeRegisterDevice(ctx context.Context, session *session, authInfo *token.AuthCachedInfo, regToken *oauth2.Token, existingData []byte) (cleanup func(), access string, data isAuthenticatedDataResponse) {
	cleanup = func() {}

	dr, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider)
	if !ok || !b.cfg.registerDevice {
		return cleanup, "", nil
	}

	var err error
	authInfo.DeviceRegistrationData, cleanup, err = dr.MaybeRegisterDevice(ctx, regToken,
		session.username,
		b.cfg.issuerURL,
		existingData,
	)
	if err != nil {
		log.Errorf(context.Background(), "error registering device: %s", err)
		return func() {}, AuthDenied, errorMessage{Message: "Error registering device"}
	}

	// Store the auth info, so that the device registration data is not lost if the login fails after this point.
	if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
		log.Errorf(context.Background(), "Failed to store token: %s", err)
		return cleanup, AuthDenied, unexpectedErrMsg("failed to store token")
	}

	return cleanup, "", nil
}

func (b *Broker) getGroups(ctx context.Context, session *session, t *token.AuthCachedInfo) ([]info.Group, error) {
	if session.isOffline {
		return nil, errors.New("session is in offline mode")
	}

	gf, ok := providers.ProviderAs[providers.GroupFetcher](b.provider)
	if !ok {
		return nil, nil
	}
	// A cached token that carries device-registration data has a PRT that must be
	// exchanged for a Graph-scoped token (strategy 2). Derive this from the
	// presence of that data rather than tracking a separate persisted flag.
	return gf.GetGroups(ctx,
		b.cfg.clientID,
		b.cfg.issuerURL,
		t.Token,
		t.ProviderMetadata,
		t.DeviceRegistrationData,
		len(t.DeviceRegistrationData) > 0,
	)
}

// Checks if the provided error is of type ForDisplayError. If it is, it returns the error message. Else, it returns
// the provided fallback message.
func errorMessageForDisplay(err error, fallback string) errorMessage {
	var forDisplayErr *providerErrors.ForDisplayError
	if errors.As(err, &forDisplayErr) {
		return errorMessage{Message: forDisplayErr.Error()}
	}
	return errorMessage{Message: fallback}
}
