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
	"github.com/canonical/authd/authd-oidc-brokers/internal/fileutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/password"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
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
)

// Config is the configuration for the broker.
type Config struct {
	ConfigFile string
	DataDir    string

	userConfig
}

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct {
	cfg        Config
	apiVersion uint

	provider providers.Provider
	oidcCfg  oidc.Config

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
	deviceAuthResponse *oauth2.DeviceAuthResponse
	authInfo           *token.AuthCachedInfo

	isAuthenticating *isAuthenticatedCtx
}

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

type option struct {
	provider providers.Provider
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

	b = &Broker{
		cfg:        cfg,
		apiVersion: apiVersion,
		provider:   opts.provider,
		oidcCfg:    oidc.Config{ClientID: clientID},
		privateKey: privateKey,

		currentSessions:   make(map[string]session),
		currentSessionsMu: sync.RWMutex{},
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

	return nil
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
			ClientSecret: b.cfg.clientSecret,
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
		// Password authentication should be the first option if available, to avoid performing device authentication
		// when it's not necessary.
		modes := append([]string{authmodes.Password}, b.provider.SupportedOIDCAuthModes()...)
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

		isTokenForDeviceRegistration, err := dr.IsTokenForDeviceRegistration(authInfo.Token)
		if err != nil {
			log.Warningf(context.Background(), "Could not check if token is for device registration, so local password authentication is not available: %v", err)
			return false
		}

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
		if session.oidcServer == nil {
			log.Debugf(context.Background(), "OIDC server is not initialized, so device authentication is not available")
			return false
		}
		if session.oidcServer.Endpoint().DeviceAuthURL == "" {
			log.Debugf(context.Background(), "OIDC server does not support device authentication, so device authentication is not available")
			return false
		}
		if session.isOffline {
			log.Noticef(context.Background(), "Session is in offline mode, so device authentication is not available")
			return false
		}
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
		mode := b.supportedAuthModeFromLayout(layout)
		if mode != "" {
			supportedModes = append(supportedModes, mode)
		}
	}
	return supportedModes
}

func (b *Broker) supportedAuthModeFromLayout(layout map[string]string) string {
	supportedEntries := strings.Split(strings.TrimPrefix(layout["entry"], "optional:"), ",")
	switch layout["type"] {
	case "qrcode":
		if !strings.Contains(layout["wait"], "true") {
			return ""
		}
		if layout["renders_qrcode"] == "false" {
			return authmodes.Device
		}
		return authmodes.DeviceQr

	case "form":
		if slices.Contains(supportedEntries, "chars_password") {
			return authmodes.Password
		}

	case "newpassword":
		if slices.Contains(supportedEntries, "chars_password") {
			return authmodes.NewPassword
		}
	}
	return ""
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
		// device authentication requests must provide client authentication, similar to that for
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
			return nil, fmt.Errorf("could not generate Device Authentication code layout: %v", err)
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
		log.Warningf(context.Background(), "No refresh token returned for user during device authentication. You might have to add the 'offline_access' scope to the 'extra_scopes' setting.")
	}

	rawIDToken, ok := t.Extra("id_token").(string)
	if !ok {
		log.Error(context.Background(), "token response does not contain an ID token")
		return AuthDenied, unexpectedErrMsg("token response does not contain an ID token")
	}

	var extraFields map[string]interface{}
	if mp, ok := providers.ProviderAs[providers.MetadataProvider](b.provider); ok {
		extraFields = mp.GetExtraFields(t)
	}
	authInfo := token.NewAuthCachedInfo(t, rawIDToken, extraFields)

	if mp, ok := providers.ProviderAs[providers.MetadataProvider](b.provider); ok {
		authInfo.ProviderMetadata, err = mp.GetMetadata(session.oidcServer)
		if err != nil {
			log.Errorf(context.Background(), "could not get provider metadata: %s", err)
			return AuthDenied, unexpectedErrMsg("could not get provider metadata")
		}
	}

	authInfo.UserInfo, err = b.getUserInfo(ctx, session, t, rawIDToken, false)
	if err != nil {
		log.Errorf(context.Background(), "could not get user info: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Could not get user info")
	}

	if !b.userNameIsAllowed(authInfo.UserInfo.Name) {
		log.Warning(context.Background(), b.userNotAllowedLogMsg(authInfo.UserInfo.Name))
		return AuthDenied, errorMessage{Message: "Authentication failure: user not allowed in broker configuration"}
	}
	if authInfo.UserInfo.ProviderID != "" && session.providerID == "" {
		b.ensureProviderIDCacheDir(session, authInfo.UserInfo.ProviderID)
	}

	if dr, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider); ok && b.cfg.registerDevice {
		// Load existing device registration data if there is any, to avoid re-registering the device.
		var deviceRegistrationData []byte
		oldAuthInfo, err := token.LoadAuthInfo(session.tokenPath)
		if err == nil {
			deviceRegistrationData = oldAuthInfo.DeviceRegistrationData
		}

		var cleanup func()
		authInfo.DeviceRegistrationData, cleanup, err = dr.MaybeRegisterDevice(ctx, t,
			session.username,
			b.cfg.issuerURL,
			deviceRegistrationData,
		)
		if err != nil {
			log.Errorf(context.Background(), "error registering device: %s", err)
			return AuthDenied, errorMessage{Message: "Error registering device"}
		}
		defer cleanup()

		// Store the auth info, so that the device registration data is not lost if the login fails after this point.
		if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}
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

	// Refresh the token if we're online even if the token has not expired
	if b.cfg.forceAccessCheckWithProvider || !session.isOffline {
		// Check if we have a refresh token before attempting to refresh
		if authInfo.Token.RefreshToken == "" {
			log.Warningf(context.Background(), "No refresh token available for user %q", session.username)
			session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
			return AuthNext, errorMessage{Message: "Remote authentication failed: No refresh token. Please contact your administrator."}
		}

		// We have a refresh token, attempt to refresh
		oldAuthInfo := authInfo
		authInfo, err = b.refreshToken(ctx, session, authInfo)
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) {
			if b.provider.IsTokenExpiredError(retrieveErr) {
				log.Noticef(context.Background(), "Refresh token expired for user %q, new device authentication required", session.username)
				session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
				return AuthNext, errorMessage{Message: "Refresh token expired, please authenticate again using device authentication."}
			}
			if udc, ok := providers.ProviderAs[providers.UserDisabledChecker](b.provider); ok && udc.IsUserDisabledError(retrieveErr) {
				log.Error(context.Background(), retrieveErr.Error())
				log.Errorf(context.Background(), "Login denied: User %q is disabled in %s, please contact your administrator.", session.username, b.provider.DisplayName())

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
	if dr, ok := providers.ProviderAs[providers.DeviceRegisterer](b.provider); ok && !session.isOffline && b.cfg.registerDevice {
		var cleanup func()
		authInfo.DeviceRegistrationData, cleanup, err = dr.MaybeRegisterDevice(ctx,
			authInfo.Token,
			session.username,
			b.cfg.issuerURL,
			authInfo.DeviceRegistrationData,
		)
		if err != nil {
			log.Errorf(context.Background(), "error registering device: %s", err)
			return AuthDenied, errorMessage{Message: "Error registering device"}
		}
		defer cleanup()

		// Store the auth info, so that the device registration data is not lost if the login fails after this point.
		if err := token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}
	}

	// Try to refresh the groups
	groups, err := b.getGroups(ctx, session, authInfo)
	if errors.Is(err, providerErrors.ErrDeviceDisabled) {
		// The device is disabled, deny login
		log.Errorf(context.Background(), "Login failed: %s", err)

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
		log.Errorf(context.Background(), "Login failed: %s", err)
		return AuthDenied, errorMessageForDisplay(err, "Invalid redirect URI")
	}
	var retryWithDeviceAuthError *providerErrors.RetryWithDeviceAuthError
	if errors.As(err, &retryWithDeviceAuthError) {
		log.Errorf(context.Background(), "Token acquisition failed: %s. Try again using device authentication.", err)
		// The token acquisition failed unexpectedly.
		// One possible reason is that the device was deleted by an administrator in Entra ID.
		// In this case, the user can perform device authentication again to get a new token
		// and register the device again, allowing the user to log in.
		// We delete the device registration data to cause device authentication to re-register the device.
		authInfo.DeviceRegistrationData = nil
		if err = token.CacheAuthInfo(session.tokenPath, authInfo); err != nil {
			log.Errorf(context.Background(), "Failed to store token: %s", err)
			return AuthDenied, unexpectedErrMsg("failed to store token")
		}

		session.nextAuthModes = []string{authmodes.Device, authmodes.DeviceQr}
		msg := "Authentication failed due to a token issue. Please try again using device authentication."
		return AuthNext, errorMessage{Message: msg}
	}
	if err != nil {
		// We couldn't fetch the groups, but we have valid cached ones.
		log.Warningf(context.Background(), "Could not get groups: %v. Using cached groups.", err)
	} else {
		authInfo.UserInfo.Groups = groups
	}

	return b.finishAuth(session, authInfo)
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
	if session.isAuthenticating != nil {
		b.CancelIsAuthenticated(sessionID)
	}

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

// refreshToken refreshes the OAuth2 token and returns the updated AuthCachedInfo.
func (b *Broker) refreshToken(ctx context.Context, session *session, oldToken *token.AuthCachedInfo) (*token.AuthCachedInfo, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, maxRequestDuration)
	defer cancel()
	// set cached token expiry time to one hour in the past
	// this makes sure the token is refreshed even if it has not 'actually' expired
	oldToken.Token.Expiry = time.Now().Add(-time.Hour)
	oauthToken, err := session.oauth2Config.TokenSource(timeoutCtx, oldToken.Token).Token()
	if err != nil {
		return nil, err
	}

	// Update the raw ID token
	rawIDToken, ok := oauthToken.Extra("id_token").(string)
	if !ok {
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
		return nil, err
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
	idToken, err := session.oidcServer.Verifier(&b.oidcCfg).Verify(ctx, rawIDToken)
	if err != nil {
		return info.User{}, fmt.Errorf("could not verify token: %w", err)
	}

	userInfo, err := b.provider.GetUserInfo(idToken, isRefresh)
	var missingClaimErr *providerErrors.MissingClaimError
	if errors.As(err, &missingClaimErr) {
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
		var claims info.Claimer
		claims, err = info.NewMergedClaimer(idToken, userInfoClaims)
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

func (b *Broker) getGroups(ctx context.Context, session *session, t *token.AuthCachedInfo) ([]info.Group, error) {
	if session.isOffline {
		return nil, errors.New("session is in offline mode")
	}

	gf, ok := providers.ProviderAs[providers.GroupFetcher](b.provider)
	if !ok {
		return nil, nil
	}

	return gf.GetGroups(ctx,
		b.cfg.clientID,
		b.cfg.issuerURL,
		t.Token,
		t.ProviderMetadata,
		t.DeviceRegistrationData,
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
