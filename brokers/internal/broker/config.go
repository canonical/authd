package broker

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"unicode"

	"github.com/canonical/authd/brokers/internal/broker/authmodes"
	"github.com/canonical/authd/log"
	"gopkg.in/ini.v1"
)

// Configuration sections and keys.
const (
	// forceAccessCheckWithProviderKey is the key in the config file for the setting to force verification with the
	// identity provider during login.
	forceAccessCheckWithProviderKey    = "force_access_check_with_provider"
	forceAccessCheckWithProviderKeyOld = "force_provider_authentication"

	// oidcSection is the section name in the config file for the OIDC specific configuration.
	oidcSection = "oidc"
	// issuerKey is the key in the config file for the issuer.
	issuerKey = "issuer"
	// clientIDKey is the key in the config file for the client ID.
	clientIDKey = "client_id"
	// clientSecret is the optional client secret for this client.
	clientSecret = "client_secret"
	// extraScopesKey is the key in the config file for extra OIDC scopes.
	extraScopesKey = "extra_scopes"

	// entraIDSection is the section name in the config file for Microsoft Entra ID specific configuration.
	entraIDSection = "msentraid"
	// registerDeviceKey is the key in the config file for the setting that enables automatic device registration.
	registerDeviceKey = "register_device"

	// usersSection is the section name in the config file for the users and broker specific configuration.
	usersSection = "users"
	// allowedUsersKey is the key in the config file for the users that are allowed to access the machine.
	allowedUsersKey = "allowed_users"
	// ownerKey is the key in the config file for the owner of the machine.
	ownerKey              = "owner"
	ownerProviderIDPrefix = "provider-id:"
	// homeDirKey is the key in the config file for the home directory prefix.
	homeDirKey = "home_base_dir"
	// sshSuffixesKey is the key in the config file for the SSH allowed suffixes.
	sshSuffixesKey = "ssh_allowed_suffixes_first_auth"
	// sshSuffixesKeyOld is the old key in the config file for the SSH allowed suffixes. It should be removed later.
	sshSuffixesKeyOld = "ssh_allowed_suffixes"
	// extraGroupsKey is the key in the config file for the extra groups to add to each authd user.
	extraGroupsKey = "extra_groups"
	// ownerExtraGroupsKey is the key in the config file for the extra groups to add to the owner.
	ownerExtraGroupsKey = "owner_extra_groups"
	// allUsersKeyword is the keyword for the `allowed_users` key that allows access to all users.
	allUsersKeyword = "ALL"
	// ownerUserKeyword is the keyword for the `allowed_users` key that allows access to the owner.
	ownerUserKeyword = "OWNER"

	// flowsSection is the section name in the config file for the authentication flow control.
	flowsSection = "flows"
	// flowsDeviceAuthKey controls whether the device_auth and device_auth_qr modes are enabled.
	flowsDeviceAuthKey = "device_code"
	// flowsEntraAuthKey controls whether entra_auth mode is enabled.
	flowsEntraAuthKey = "entra_auth"

	// ownerAutoRegistrationConfigPath is the name of the file that will be auto-generated to register the owner.
	ownerAutoRegistrationConfigPath     = "20-owner-autoregistration.conf"
	ownerAutoRegistrationConfigTemplate = "templates/20-owner-autoregistration.conf.tmpl"
)

var (
	//go:embed templates/20-owner-autoregistration.conf.tmpl
	ownerAutoRegistrationConfig embed.FS

	// knownConfigKeys maps each known config section to its known keys.
	knownConfigKeys = map[string]map[string]struct{}{
		oidcSection: {
			issuerKey:                          {},
			clientIDKey:                        {},
			clientSecret:                       {},
			extraScopesKey:                     {},
			forceAccessCheckWithProviderKey:    {},
			forceAccessCheckWithProviderKeyOld: {},
		},
		entraIDSection: {
			registerDeviceKey: {},
		},
		usersSection: {
			allowedUsersKey:     {},
			ownerKey:            {},
			homeDirKey:          {},
			sshSuffixesKey:      {},
			sshSuffixesKeyOld:   {},
			extraGroupsKey:      {},
			ownerExtraGroupsKey: {},
		},
		flowsSection: {
			flowsDeviceAuthKey: {},
			flowsEntraAuthKey:  {},
		},
	}
)

type provider interface {
	NormalizeUsername(username string) string
	SupportedOnlineAuthModes() []string
}

// configFile holds the path and content of a configuration file.
type configFile struct {
	path    string
	content []byte
}

type templateEnv struct {
	Owner     string
	OwnerName string
}

type userConfig struct {
	clientID     string
	clientSecret string
	issuerURL    string

	forceAccessCheckWithProvider bool
	registerDevice               bool

	allowedUsers          map[string]struct{}
	allUsersAllowed       bool
	ownerAllowed          bool
	firstUserBecomesOwner bool
	owner                 string
	ownerMutex            *sync.RWMutex
	homeBaseDir           string
	allowedSSHSuffixes    []string
	extraGroups           []string
	ownerExtraGroups      []string
	extraScopes           []string

	flows flowsConfig

	provider provider
}

// flowsConfig holds the parsed [flows] section configuration.
type flowsConfig struct {
	DeviceAuth bool
	EntraAuth  bool
}

// defaultFlowsConfig returns the defaults for flow settings omitted from the
// configuration. Device-code authentication stays enabled for compatibility.
// Entra authentication follows register_device so existing configurations keep
// using it when device registration was enabled.
func defaultFlowsConfig(registerDevice bool) flowsConfig {
	return flowsConfig{
		DeviceAuth: true,
		EntraAuth:  registerDevice,
	}
}

// GetDropInDir takes the broker configuration path and returns the drop in dir path.
func GetDropInDir(cfgPath string) string {
	return cfgPath + ".d"
}

func readDropInFiles(cfgPath string) ([]configFile, error) {
	// Check if a .d directory exists and return the paths to the files in it.
	dropInDir := GetDropInDir(cfgPath)
	files, err := os.ReadDir(dropInDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var dropInFiles []configFile
	// files is empty if the directory does not exist
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		p := filepath.Join(dropInDir, file.Name())
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("could not read drop-in file %q: %v", p, err)
		}
		dropInFiles = append(dropInFiles, configFile{path: p, content: content})
	}

	return dropInFiles, nil
}

func (uc *userConfig) populateUsersConfig(users *ini.Section) {
	uc.ownerMutex.Lock()
	defer uc.ownerMutex.Unlock()

	if users == nil {
		// The default behavior is to allow only the owner
		uc.ownerAllowed = true
		uc.firstUserBecomesOwner = true
		return
	}

	uc.homeBaseDir = users.Key(homeDirKey).String()

	suffixesKey := sshSuffixesKey
	// If we don't have the new key, we should try reading the old one instead.
	if !users.HasKey(sshSuffixesKey) {
		suffixesKey = sshSuffixesKeyOld
	}
	uc.allowedSSHSuffixes = strings.Split(users.Key(suffixesKey).String(), ",")

	if uc.allowedUsers == nil {
		uc.allowedUsers = make(map[string]struct{})
	}

	allowedUsers := users.Key(allowedUsersKey).Strings(",")
	if len(allowedUsers) == 0 {
		allowedUsers = append(allowedUsers, ownerUserKeyword)
	}

	for _, user := range allowedUsers {
		if user == allUsersKeyword {
			uc.allUsersAllowed = true
			continue
		}
		if user == ownerUserKeyword {
			uc.ownerAllowed = true
			if !users.HasKey(ownerKey) {
				// If owner is unset, then the first user becomes owner
				uc.firstUserBecomesOwner = true
			}
			continue
		}

		uc.allowedUsers[uc.provider.NormalizeUsername(user)] = struct{}{}
	}

	// We need to read the owner key after we call HasKey, because the key is created
	// when we call the "Key" function and we can't distinguish between empty and unset.
	uc.owner = users.Key(ownerKey).String()
	if !strings.HasPrefix(uc.owner, ownerProviderIDPrefix) {
		uc.owner = uc.provider.NormalizeUsername(uc.owner)
	}

	uc.extraGroups = users.Key(extraGroupsKey).Strings(",")
	uc.ownerExtraGroups = users.Key(ownerExtraGroupsKey).Strings(",")
}

// parseConfigFromPath parses the config file and returns a map with the configuration keys and values.
func parseConfigFromPath(cfgPath string, p provider, allowLegacyConfig bool, reportConfigViolations func([]error)) (userConfig, error) {
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		return userConfig{}, fmt.Errorf("could not open config file %q: %v", cfgPath, err)
	}
	cfgFile := configFile{path: cfgPath, content: content}

	dropInFiles, err := readDropInFiles(cfgPath)
	if err != nil {
		return userConfig{}, err
	}

	return parseConfigWithReporter(cfgFile, dropInFiles, p, allowLegacyConfig, reportConfigViolations)
}

func cloneUserConfig(uc userConfig) userConfig {
	uc.ownerMutex = &sync.RWMutex{}

	uc.allowedUsers = maps.Clone(uc.allowedUsers)

	uc.allowedSSHSuffixes = slices.Clone(uc.allowedSSHSuffixes)
	uc.extraGroups = slices.Clone(uc.extraGroups)
	uc.ownerExtraGroups = slices.Clone(uc.ownerExtraGroups)
	uc.extraScopes = slices.Clone(uc.extraScopes)

	return uc
}

// validatePlaceholders checks that no values in iniCfg still contain unedited
// template placeholders (e.g. "<ISSUER_ID>"). path is used only for the error
// message and should be the main config file path.
func validatePlaceholders(path string, iniCfg *ini.File) error {
	var placeholderErr error
	for _, section := range iniCfg.Sections() {
		for _, key := range section.Keys() {
			v := key.Value()
			if strings.Contains(v, "<") && strings.Contains(v, ">") {
				placeholderErr = errors.Join(placeholderErr, fmt.Errorf("unedited template placeholder found in section '%s', key '%s'", section.Name(), key.Name()))
			}
		}
	}
	if placeholderErr != nil {
		return fmt.Errorf("config file %q has invalid values, did you edit the config file?\n%w", path, placeholderErr)
	}
	return nil
}

// validateConfigFile checks a parsed ini config for validity: parseable boolean
// fields, and unknown sections and keys. It does not check for template
// placeholders; call validatePlaceholders for that.
func validateConfigFile(path string, iniCfg *ini.File) []error {
	var violations []error

	for _, section := range iniCfg.Sections() {
		if section.Name() == ini.DefaultSection {
			if len(section.Keys()) > 0 {
				violations = append(violations, fmt.Errorf("keys outside of any section in config file %q", path))
			}
			continue
		}
		sectionKeys, ok := knownConfigKeys[section.Name()]
		if !ok {
			violations = append(violations, fmt.Errorf("unknown section %q in config file %q", section.Name(), path))
			continue
		}

		for _, key := range section.Keys() {
			if _, ok := sectionKeys[key.Name()]; !ok {
				violations = append(violations, fmt.Errorf("unknown key %q in section %q in config file %q", key.Name(), section.Name(), path))
			}
		}
	}

	oidc := iniCfg.Section(oidcSection)
	if oidc != nil {
		forceAccessCheckKey := forceAccessCheckWithProviderKey
		if !oidc.HasKey(forceAccessCheckKey) {
			forceAccessCheckKey = forceAccessCheckWithProviderKeyOld
		}
		if oidc.HasKey(forceAccessCheckKey) {
			if _, err := oidc.Key(forceAccessCheckKey).Bool(); err != nil {
				violations = append(violations,
					fmt.Errorf("error parsing '%s' in config file %q: %w", forceAccessCheckKey, path, err))
			}
		}
	}

	entraID := iniCfg.Section(entraIDSection)
	if entraID != nil && entraID.HasKey(registerDeviceKey) {
		if _, err := entraID.Key(registerDeviceKey).Bool(); err != nil {
			violations = append(violations,
				fmt.Errorf("error parsing '%s' in config file %q: %w", registerDeviceKey, path, err))
		}
	}

	flows := iniCfg.Section(flowsSection)
	for _, keyName := range []string{flowsDeviceAuthKey, flowsEntraAuthKey} {
		if flows.HasKey(keyName) {
			if _, err := flows.Key(keyName).Bool(); err != nil {
				violations = append(violations,
					fmt.Errorf("error parsing '%s' in config file %q: %w", keyName, path, err))
			}
		}
	}

	return violations
}

// parseConfig parses the config file and returns a userConfig struct with the configuration keys and values.
// It also checks if the keys contain any placeholders and returns an error if they do.
func parseConfig(cfg configFile, dropInCfgs []configFile, p provider, allowLegacyConfig bool) (userConfig, error) {
	return parseConfigWithReporter(cfg, dropInCfgs, p, allowLegacyConfig, nil)
}

func parseConfigWithReporter(cfg configFile, dropInCfgs []configFile, p provider, allowLegacyConfig bool, reportConfigViolations func([]error)) (userConfig, error) {
	uc := userConfig{provider: p, ownerMutex: &sync.RWMutex{}}

	iniCfg, err := ini.Load(cfg.content)
	if err != nil {
		return userConfig{}, fmt.Errorf("error in config file %q: %w", cfg.path, err)
	}

	// Validate syntax of the main config, but defer the placeholder check until
	// after all drop-ins are applied: drop-in files in broker.conf.d are allowed
	// to override placeholder values from the main config.
	violations := validateConfigFile(cfg.path, iniCfg)

	for _, dropIn := range dropInCfgs {
		dropInCfg, err := ini.Load(dropIn.content)
		if err != nil {
			return userConfig{}, fmt.Errorf("error in drop-in config file %q: %w", dropIn.path, err)
		}

		violations = append(violations, validateConfigFile(dropIn.path, dropInCfg)...)

		if err := iniCfg.Append(dropIn.content); err != nil {
			return userConfig{}, fmt.Errorf("error in drop-in config file %q: %w", dropIn.path, err)
		}
	}

	entraID := iniCfg.Section(entraIDSection)
	if entraID.HasKey(registerDeviceKey) {
		// Already validated per-file above; ignore error.
		uc.registerDevice, _ = entraID.Key(registerDeviceKey).Bool()
	}

	flows, flowsErr := parseFlowsConfig(iniCfg.Section(flowsSection), uc.registerDevice, p)
	uc.flows = flows

	if len(violations) > 0 {
		if !allowLegacyConfig {
			return userConfig{}, errors.Join(violations...)
		}
		for _, violation := range violations {
			log.Warning(context.Background(), violation.Error())
		}
	}

	// Checked after the violations, so that a config with both problems
	// reports the full list instead of only the flows error.
	if flowsErr != nil {
		return userConfig{}, flowsErr
	}

	// Check that all placeholders from the main config were overridden by drop-ins.
	if err := validatePlaceholders(cfg.path, iniCfg); err != nil {
		return userConfig{}, err
	}

	oidc := iniCfg.Section(oidcSection)
	if oidc != nil {
		uc.issuerURL = oidc.Key(issuerKey).String()
		uc.clientID = oidc.Key(clientIDKey).String()
		uc.clientSecret = strings.TrimSpace(oidc.Key(clientSecret).String())
		uc.extraScopes = oidc.Key(extraScopesKey).Strings(",")

		forceAccessCheckKey := forceAccessCheckWithProviderKey
		// If we don't have the new key, we should try reading the old one instead.
		if !oidc.HasKey(forceAccessCheckKey) {
			forceAccessCheckKey = forceAccessCheckWithProviderKeyOld
		}
		if oidc.HasKey(forceAccessCheckKey) {
			// Already validated per-file above; ignore error.
			uc.forceAccessCheckWithProvider, _ = oidc.Key(forceAccessCheckKey).Bool()
		}
	}

	uc.populateUsersConfig(iniCfg.Section(usersSection))
	if uc.owner == ownerProviderIDPrefix {
		return userConfig{}, errors.New("owner provider ID must not be empty")
	}

	if allowLegacyConfig && reportConfigViolations != nil {
		reportConfigViolations(append([]error(nil), violations...))
	}

	return uc, nil
}

func (uc *userConfig) userNameIsAllowed(userName, providerID string) bool {
	uc.ownerMutex.RLock()
	defer uc.ownerMutex.RUnlock()

	// The user is allowed to log in if:
	// - ALL users are allowed
	// - the user's name is in the list of allowed_users
	// - OWNER is in the allowed_users list and the user is the owner of the machine
	// - The user will be registered as the owner
	if uc.allUsersAllowed {
		return true
	}
	if _, ok := uc.allowedUsers[userName]; ok {
		return true
	}
	if uc.ownerAllowed && uc.isOwnerLocked(userName, providerID) {
		return true
	}

	return uc.shouldRegisterOwner()
}

// isOwnerLocked requires ownerMutex to be held.
func (uc *userConfig) isOwnerLocked(userName, providerID string) bool {
	if id, bound := strings.CutPrefix(uc.owner, ownerProviderIDPrefix); bound {
		return id != "" && id == providerID
	}
	return uc.owner != "" && uc.owner == uc.provider.NormalizeUsername(userName)
}

// shouldRegisterOwner returns true if the first user to log in should be registered as the owner.
// Only call this with the ownerMutex locked.
func (uc *userConfig) shouldRegisterOwner() bool {
	return uc.ownerAllowed && uc.firstUserBecomesOwner && uc.owner == ""
}

func (uc *userConfig) registerOwner(cfgPath, userName, providerID string) error {
	uc.ownerMutex.Lock()
	defer uc.ownerMutex.Unlock()

	return uc.registerOwnerLocked(cfgPath, userName, providerID)
}

func (uc *userConfig) registerOwnerIfNeeded(cfgPath, userName, providerID string) error {
	uc.ownerMutex.Lock()
	defer uc.ownerMutex.Unlock()

	if uc.shouldRegisterOwner() {
		return uc.registerOwnerLocked(cfgPath, userName, providerID)
	}
	if strings.HasPrefix(uc.owner, ownerProviderIDPrefix) || providerID == "" || !uc.isOwnerLocked(userName, providerID) {
		return nil
	}

	if err := uc.registerOwnerLocked(cfgPath, userName, providerID); err != nil {
		// Keep existing installations usable when their configuration cannot be
		// updated. Initial owner registration still requires a successful write.
		log.Warningf(context.Background(), "Could not bind the existing owner to its provider ID: %v", err)
	}
	return nil
}

// registerOwnerLocked requires ownerMutex to be locked.
func (uc *userConfig) registerOwnerLocked(cfgPath, userName, providerID string) error {
	// Reject any non-graphic characters in the username before writing it to
	// the INI drop-in config. Control characters allow INI key injection, and
	// other non-graphic runes are not valid content for a generated config
	// value.
	if strings.ContainsFunc(userName, func(r rune) bool { return !unicode.IsGraphic(r) }) {
		return fmt.Errorf("username %q contains invalid characters and cannot be used for owner registration", userName)
	}
	if strings.ContainsFunc(providerID, func(r rune) bool { return !unicode.IsGraphic(r) }) {
		return errors.New("provider ID contains invalid characters and cannot be used for owner registration")
	}
	owner := uc.provider.NormalizeUsername(userName)
	if providerID != "" {
		owner = ownerProviderIDPrefix + providerID
	} else if strings.HasPrefix(owner, ownerProviderIDPrefix) {
		return errors.New("owner username uses the reserved provider-id: prefix")
	}

	if cfgPath == "" {
		uc.owner = owner
		uc.firstUserBecomesOwner = false
		return nil
	}

	dropInDir := GetDropInDir(cfgPath)
	if uc.owner == "" || providerID == "" {
		//nolint:gosec // G301: Admins can list the directory; files protect secrets.
		if err := os.MkdirAll(dropInDir, 0755); err != nil {
			return fmt.Errorf("failed to create drop-in directory %q: %w", dropInDir, err)
		}
	}
	p := filepath.Join(dropInDir, ownerAutoRegistrationConfigPath)

	templateName := filepath.Base(ownerAutoRegistrationConfigTemplate)
	t, err := template.New(templateName).ParseFS(ownerAutoRegistrationConfig, ownerAutoRegistrationConfigTemplate)
	if err != nil {
		return fmt.Errorf("failed to open autoregistration template: %v", err)
	}

	var content bytes.Buffer
	env := templateEnv{Owner: owner}
	if providerID != "" {
		env.OwnerName = userName
	}
	if err := t.Execute(&content, env); err != nil {
		return fmt.Errorf("failed to render owner registration file: %v", err)
	}

	mainConfig, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	dropIns, err := readDropInFiles(cfgPath)
	if err != nil {
		return err
	}
	main := configFile{path: cfgPath, content: mainConfig}
	mode := fs.FileMode(0600)
	var originalStat *syscall.Stat_t
	if uc.owner != "" && providerID != "" {
		// Replace the effective declaration, not a lower-priority drop-in.
		// This also lets admins replace a binding with a name in the same file.
		configs := append([]configFile{main}, dropIns...)
		var source *ini.File
		target := 0
		for i, cfg := range configs {
			parsed, err := ini.Load(cfg.content)
			if err != nil {
				return err
			}
			if parsed.Section(usersSection).HasKey(ownerKey) {
				source, target = parsed, i
			}
		}
		if source == nil {
			return errors.New("owner configuration changed before binding")
		}
		effective, err := parseConfig(main, dropIns, uc.provider, true)
		if err != nil {
			return err
		}
		if effective.owner != uc.owner {
			return errors.New("owner configuration changed before binding")
		}
		key := source.Section(usersSection).Key(ownerKey)
		key.SetValue(owner)
		key.Comment = fmt.Sprintf("# Owner name when bound: %s\n# The ID below identifies this account at the identity provider.\n# The same account remains the owner if its name changes.", userName)
		content.Reset()
		if _, err := source.WriteTo(&content); err != nil {
			return err
		}
		p = configs[target].path
		configs[target].content = content.Bytes()
		main, dropIns = configs[0], configs[1:]
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
		originalStat, _ = info.Sys().(*syscall.Stat_t)
	} else {
		dropIns = slices.DeleteFunc(dropIns, func(c configFile) bool { return c.path == p })
		dropIns = append(dropIns, configFile{path: p, content: content.Bytes()})
		slices.SortFunc(dropIns, func(a, b configFile) int { return strings.Compare(a.path, b.path) })
	}
	effective, err := parseConfig(main, dropIns, uc.provider, true)
	if err != nil {
		return fmt.Errorf("invalid owner registration configuration: %w", err)
	}
	if effective.owner != owner {
		return errors.New("owner registration is overridden by another configuration file")
	}

	// Drop-in readers skip directories, so they cannot see an unfinished file.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(p), ".owner-")
	if err != nil {
		return fmt.Errorf("failed to stage owner registration file: %w", err)
	}
	defer os.Remove(stagingDir)
	f, err := os.CreateTemp(stagingDir, "owner")
	if err != nil {
		return fmt.Errorf("failed to create owner registration file: %v", err)
	}
	defer f.Close()
	defer os.Remove(f.Name())

	if originalStat != nil {
		if err := f.Chown(int(originalStat.Uid), int(originalStat.Gid)); err != nil {
			return fmt.Errorf("failed to preserve owner configuration ownership: %w", err)
		}
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("failed to preserve owner configuration permissions: %w", err)
	}
	if _, err := f.Write(content.Bytes()); err != nil {
		return fmt.Errorf("failed to write owner registration file: %v", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync owner registration file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close owner registration file: %w", err)
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return fmt.Errorf("failed to replace owner registration file: %w", err)
	}

	// We set the owner after we create the autoregistration file, so that in case of an error
	// the owner is not updated.
	uc.owner = owner
	uc.firstUserBecomesOwner = false

	return nil
}

// parseFlowsConfig parses the [flows] section and returns a flowsConfig with
// defaults for missing keys.
func parseFlowsConfig(section *ini.Section, registerDevice bool, p provider) (flowsConfig, error) {
	fc := defaultFlowsConfig(registerDevice)

	// Unparseable values fall back to the default here. validateConfigFile
	// already reported them per file, where the file path is known.
	if section != nil {
		if section.HasKey(flowsDeviceAuthKey) {
			val, err := section.Key(flowsDeviceAuthKey).Bool()
			if err == nil {
				fc.DeviceAuth = val
			}
		}

		if section.HasKey(flowsEntraAuthKey) {
			val, err := section.Key(flowsEntraAuthKey).Bool()
			if err == nil {
				fc.EntraAuth = val
			}
		}
	}

	supportedModes := p.SupportedOnlineAuthModes()
	if !hasEnabledSupportedFlow(fc, supportedModes) {
		return flowsConfig{}, invalidFlowsConfigError(supportedModes)
	}

	return fc, nil
}

func hasEnabledSupportedFlow(fc flowsConfig, supportedModes []string) bool {
	deviceAuthSupported := slices.Contains(supportedModes, authmodes.Device) || slices.Contains(supportedModes, authmodes.DeviceQr)
	entraAuthSupported := slices.Contains(supportedModes, authmodes.EntraAuth)

	return (fc.DeviceAuth && deviceAuthSupported) || (fc.EntraAuth && entraAuthSupported)
}

// Keep the error provider-specific so it only suggests flows the broker can
// actually offer, such as not mentioning Entra authentication for Google.
func invalidFlowsConfigError(supportedModes []string) error {
	var flowKeys []string
	if slices.Contains(supportedModes, authmodes.Device) || slices.Contains(supportedModes, authmodes.DeviceQr) {
		flowKeys = append(flowKeys, flowsDeviceAuthKey)
	}
	if slices.Contains(supportedModes, authmodes.EntraAuth) {
		flowKeys = append(flowKeys, flowsEntraAuthKey)
	}

	switch len(flowKeys) {
	case 0:
		return fmt.Errorf("invalid [%s] configuration: all supported authentication flows are disabled", flowsSection)
	case 1:
		return fmt.Errorf("invalid [%s] configuration: no supported authentication flows are enabled; the %q flow must be enabled",
			flowsSection, flowKeys[0])
	default:
		return fmt.Errorf("invalid [%s] configuration: no supported authentication flows are enabled; at least one of the %q or %q flows must be enabled",
			flowsSection, flowKeys[0], flowKeys[1])
	}
}
