package broker

import (
	"errors"
	"fmt"
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

	// usersSection is the section name in the config file for the users and broker specific configuration.
	usersSection = "users"
	// homeDirKey is the key in the config file for the home directory prefix.
	homeDirKey = "home_base_dir"
	// SSHSuffixKey is the key in the config file for the SSH allowed suffixes.
	sshSuffixesKey = "ssh_allowed_suffixes"
)

// parseConfigFile parses the config file and returns a map with the configuration keys and values.
func parseConfigFile(cfgPath string) (userConfig, error) {
	cfg := userConfig{}

	iniCfg, err := ini.Load(cfgPath)
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
	}

	users := iniCfg.Section(usersSection)
	if users != nil {
		cfg.homeBaseDir = users.Key(homeDirKey).String()
		cfg.allowedSSHSuffixes = strings.Split(users.Key(sshSuffixesKey).String(), ",")
	}

	return cfg, nil
}
