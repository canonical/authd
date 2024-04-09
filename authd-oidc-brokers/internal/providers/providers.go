// Package providers define provider-specific configurations and functions to be used by the OIDC broker.
package providers

import (
	"github.com/ubuntu/oidc-broker/internal/providers/group"
	"golang.org/x/oauth2"
)

// ProviderInfoer defines provider-specific methods to be used by the broker.
type ProviderInfoer interface {
	AdditionalScopes() []string
	AuthOptions() []oauth2.AuthCodeOption
	CurrentAuthenticationModesOffered(sessionMode string, supportedAuthModes map[string]string, tokenExists bool, currentAuthStep int) ([]string, error)
	GetGroups(*oauth2.Token) ([]group.Info, error)
}
