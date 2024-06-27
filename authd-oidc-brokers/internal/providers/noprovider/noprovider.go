// Package noprovider is the generic oidc extension.
package noprovider

import (
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/oidc-broker/internal/providers/group"
	"golang.org/x/oauth2"
)

// NoProvider is a generic OIDC provider.
type NoProvider struct{}

// AdditionalScopes returns the generic scopes required by the provider.
func (p NoProvider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess}
}

// AuthOptions is a no-op when no specific provider is in use.
func (p NoProvider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetGroups is a no-op when no specific provider is in use.
func (p NoProvider) GetGroups(_ *oauth2.Token) ([]group.Info, error) {
	return nil, nil
}

// CurrentAuthenticationModesOffered returns the generic authentication modes supported by the provider.
func (p NoProvider) CurrentAuthenticationModesOffered(
	sessionMode string,
	supportedAuthModes map[string]string,
	tokenExists bool,
	providerReachable bool,
	endpoints map[string]string,
	currentAuthStep int,
) ([]string, error) {
	var offeredModes []string
	switch sessionMode {
	case "passwd":
		if !tokenExists {
			return nil, errors.New("user has no cached token")
		}
		offeredModes = []string{"password"}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}

	default: // auth mode
		if providerReachable && endpoints["device_auth"] != "" {
			offeredModes = []string{"device_auth"}
		}
		if tokenExists {
			offeredModes = append([]string{"password"}, offeredModes...)
		}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}
	}

	for _, mode := range offeredModes {
		if _, ok := supportedAuthModes[mode]; !ok {
			return nil, fmt.Errorf("auth mode %q required by the provider, but is not supported locally", mode)
		}
	}

	return offeredModes, nil
}
