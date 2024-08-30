package consts

import "github.com/coreos/go-oidc/v3/oidc"

var (
	// DefaultScopes contains the OIDC scopes that we require for all providers.
	// Provider implementations can append additional scopes.
	DefaultScopes = []string{oidc.ScopeOpenID, "profile", "email"}
)
