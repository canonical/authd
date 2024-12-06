package broker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/ini.v1"
)

// Configuration sections and keys.
const (
	// oidcSection is the section name in the config file for the OIDC specific configuration.
	oidcSection = "oidc"
	// issuerKey is the key in the config file for the issuer.
	issuerKey = "issuer"
	// clientIDKey is the key in the config file for the client ID.
	clientIDKey = "client_id"
	// clientSecret is the optional client secret for this client.
	clientSecret = "client_secret"

	// usersSection is the section name in the config file for the users and broker specific configuration.
	usersSection = "users"
	// allowedUsersKey is the key in the config file for the users that are allowed to access the machine.
	allowedUsersKey = "allowed_users"
	// ownerKey is the key in the config file for the owner of the machine.
	ownerKey = "owner"
	// homeDirKey is the key in the config file for the home directory prefix.
	homeDirKey = "home_base_dir"
	// SSHSuffixKey is the key in the config file for the SSH allowed suffixes.
	sshSuffixesKey = "ssh_allowed_suffixes"

	// allUsersKeyword is the keyword for the `allowed_users` key that allows access to all users.
	allUsersKeyword = "ALL"
	// ownerUserKeyword is the keyword for the `allowed_users` key that allows access to the owner.
	ownerUserKeyword = "OWNER"
)

func getDropInFiles(cfgPath string) ([]any, error) {
	// Check if a .d directory exists and return the paths to the files in it.
	dropInDir := cfgPath + ".d"
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
		dropInFiles = append(dropInFiles, filepath.Join(dropInDir, file.Name()))
	}

	return dropInFiles, nil
}

func (uc *userConfig) populateUsersConfig(users *ini.Section) {
	if users == nil {
		// The default behavior is to allow only the owner
		uc.ownerAllowed = true
		uc.firstUserBecomesOwner = true
		return
	}

	uc.homeBaseDir = users.Key(homeDirKey).String()
	uc.allowedSSHSuffixes = strings.Split(users.Key(sshSuffixesKey).String(), ",")

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

		uc.allowedUsers[user] = struct{}{}
	}

	// We need to read the owner key after we call HasKey, because the key is created
	// when we call the "Key" function and we can't distinguish between empty and unset.
	uc.owner = users.Key(ownerKey).String()
}

// parseConfigFile parses the config file and returns a map with the configuration keys and values.
func parseConfigFile(cfgPath string) (userConfig, error) {
	cfg := userConfig{}

	dropInFiles, err := getDropInFiles(cfgPath)
	if err != nil {
		return cfg, err
	}

	iniCfg, err := ini.Load(cfgPath, dropInFiles...)
	if err != nil {
		return cfg, err
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
		return cfg, fmt.Errorf("config file has invalid values, did you edit the file %q?\n%w", cfgPath, err)
	}

	oidc := iniCfg.Section(oidcSection)
	if oidc != nil {
		cfg.issuerURL = oidc.Key(issuerKey).String()
		cfg.clientID = oidc.Key(clientIDKey).String()
		cfg.clientSecret = oidc.Key(clientSecret).String()
	}

	cfg.populateUsersConfig(iniCfg.Section(usersSection))

	return cfg, nil
}
