package dbusservice

// Configuration sections and keys.
const (
	// authdSection is the section name in the config file for the authentication daemon specific configuration.
	authdSection = "authd"
	// dbusNameKey is the key in the config file for the dbus name of the authentication daemon.
	dbusNameKey = "dbus_name"
	// dbusObjectKey is the key in the config file for the dbus object of the authentication daemon.
	dbusObjectKey = "dbus_object"

	// oidcSection is the section name in the config file for the OIDC specific configuration.
	oidcSection = "oidc"
	// issuerKey is the key in the config file for the issuer.
	issuerKey = "issuer"
	// clientIDKey is the key in the config file for the client ID.
	clientIDKey = "client_id"
	// homeDirKey is the key in the config file for the home directory prefix.
	homeDirKey = "home_base_dir"
	// SSHSuffixKey is the key in the config file for the SSH allowed suffixes.
	sshSuffixesKey = "ssh_allowed_suffixes"
)
