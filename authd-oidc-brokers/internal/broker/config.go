package broker

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/ini.v1"
)

// Configuration sections and keys.
const (
	// forceProviderAuthenticationKey is the key in the config file for the option to force provider authentication during login.
	forceProviderAuthenticationKey = "force_provider_authentication"

	// oidcSection is the section name in the config file for the OIDC specific configuration.
	oidcSection = "oidc"
	// issuerKey is the key in the config file for the issuer.
	issuerKey = "issuer"
	// clientIDKey is the key in the config file for the client ID.
	clientIDKey = "client_id"
	// clientSecret is the optional client secret for this client.
	clientSecret = "client_secret"

	// entraIDSection is the section name in the config file for Microsoft Entra ID specific configuration.
	entraIDSection = "msentraid"
	// registerDeviceKey is the key in the config file for the setting that enables automatic device registration.
	registerDeviceKey = "register_device"

	// usersSection is the section name in the config file for the users and broker specific configuration.
	usersSection = "users"
	// allowedUsersKey is the key in the config file for the users that are allowed to access the machine.
	allowedUsersKey = "allowed_users"
	// ownerKey is the key in the config file for the owner of the machine.
	ownerKey = "owner"
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

	// ownerAutoRegistrationConfigPath is the name of the file that will be auto-generated to register the owner.
	ownerAutoRegistrationConfigPath     = "20-owner-autoregistration.conf"
	ownerAutoRegistrationConfigTemplate = "templates/20-owner-autoregistration.conf.tmpl"
)

var (
	//go:embed templates/20-owner-autoregistration.conf.tmpl
	ownerAutoRegistrationConfig embed.FS
)

type provider interface {
	NormalizeUsername(username string) string
}

type templateEnv struct {
	Owner string
}

type userConfig struct {
	clientID     string
	clientSecret string
	issuerURL    string

	forceProviderAuthentication bool
	registerDevice              bool

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

	provider provider
}

// GetDropInDir takes the broker configuration path and returns the drop in dir path.
func GetDropInDir(cfgPath string) string {
	return cfgPath + ".d"
}

func readDropInFiles(cfgPath string) ([]any, error) {
	// Check if a .d directory exists and return the paths to the files in it.
	dropInDir := GetDropInDir(cfgPath)
	files, err := os.ReadDir(dropInDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var dropInFiles []any
	// files is empty if the directory does not exist
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		dropInFile, err := os.ReadFile(filepath.Join(dropInDir, file.Name()))
		if err != nil {
			return nil, fmt.Errorf("could not read drop-in file %q: %v", file.Name(), err)
		}
		dropInFiles = append(dropInFiles, dropInFile)
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
	uc.owner = uc.provider.NormalizeUsername(users.Key(ownerKey).String())

	uc.extraGroups = users.Key(extraGroupsKey).Strings(",")
	uc.ownerExtraGroups = users.Key(ownerExtraGroupsKey).Strings(",")
}

// parseConfigFromPath parses the config file and returns a map with the configuration keys and values.
func parseConfigFromPath(cfgPath string, p provider) (userConfig, error) {
	cfgFile, err := os.ReadFile(cfgPath)
	if err != nil {
		return userConfig{}, fmt.Errorf("could not open config file %q: %v", cfgPath, err)
	}

	dropInFiles, err := readDropInFiles(cfgPath)
	if err != nil {
		return userConfig{}, err
	}

	return parseConfig(cfgFile, dropInFiles, p)
}

// parseConfig parses the config file and returns a userConfig struct with the configuration keys and values.
// It also checks if the keys contain any placeholders and returns an error if they do.
func parseConfig(cfgContent []byte, dropInContent []any, p provider) (userConfig, error) {
	cfg := userConfig{provider: p, ownerMutex: &sync.RWMutex{}}

	iniCfg, err := ini.Load(cfgContent, dropInContent...)
	if err != nil {
		return userConfig{}, err
	}

	// Check if any of the keys still contain the placeholders.
	for _, section := range iniCfg.Sections() {
		for _, key := range section.Keys() {
			if strings.Contains(key.Value(), "<") && strings.Contains(key.Value(), ">") {
				err = errors.Join(err, fmt.Errorf("found invalid character in section %q, key %q", section.Name(), key.Name()))
			}
		}
	}
	if err != nil {
		return userConfig{}, fmt.Errorf("config file has invalid values, did you edit the config file?\n%w", err)
	}

	oidc := iniCfg.Section(oidcSection)
	if oidc != nil {
		cfg.issuerURL = oidc.Key(issuerKey).String()
		cfg.clientID = oidc.Key(clientIDKey).String()
		cfg.clientSecret = oidc.Key(clientSecret).String()

		if oidc.HasKey(forceProviderAuthenticationKey) {
			cfg.forceProviderAuthentication, err = oidc.Key(forceProviderAuthenticationKey).Bool()
			if err != nil {
				return userConfig{}, fmt.Errorf("error parsing '%s': %w", forceProviderAuthenticationKey, err)
			}
		}
	}

	entraID := iniCfg.Section(entraIDSection)
	if entraID != nil && entraID.HasKey(registerDeviceKey) {
		cfg.registerDevice, err = entraID.Key(registerDeviceKey).Bool()
		if err != nil {
			return userConfig{}, fmt.Errorf("error parsing '%s': %w", registerDeviceKey, err)
		}
	}

	cfg.populateUsersConfig(iniCfg.Section(usersSection))

	return cfg, nil
}

func (uc *userConfig) userNameIsAllowed(userName string) bool {
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
	if uc.ownerAllowed && uc.owner == userName {
		return true
	}

	return uc.shouldRegisterOwner()
}

// shouldRegisterOwner returns true if the first user to log in should be registered as the owner.
// Only call this with the ownerMutex locked.
func (uc *userConfig) shouldRegisterOwner() bool {
	return uc.ownerAllowed && uc.firstUserBecomesOwner && uc.owner == ""
}

func (uc *userConfig) registerOwner(cfgPath, userName string) error {
	// We need to lock here to avoid a race condition where two users log in at the same time, causing both to be
	// considered the owner.
	uc.ownerMutex.Lock()
	defer uc.ownerMutex.Unlock()

	if cfgPath == "" {
		uc.owner = uc.provider.NormalizeUsername(userName)
		uc.firstUserBecomesOwner = false
		return nil
	}

	p := filepath.Join(GetDropInDir(cfgPath), ownerAutoRegistrationConfigPath)

	templateName := filepath.Base(ownerAutoRegistrationConfigTemplate)
	t, err := template.New(templateName).ParseFS(ownerAutoRegistrationConfig, ownerAutoRegistrationConfigTemplate)
	if err != nil {
		return fmt.Errorf("failed to open autoregistration template: %v", err)
	}

	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create owner registration file: %v", err)
	}
	defer f.Close()

	if err := t.Execute(f, templateEnv{Owner: userName}); err != nil {
		return fmt.Errorf("failed to write owner registration file: %v", err)
	}

	// We set the owner after we create the autoregistration file, so that in case of an error
	// the owner is not updated.
	uc.owner = uc.provider.NormalizeUsername(userName)
	uc.firstUserBecomesOwner = false

	return nil
}
