//go:build !withmsentraid

package providers

import (
	"github.com/ubuntu/oidc-broker/internal/providers/noprovider"
)

// CurrentProviderInfo returns a generic oidc provider implementation.
func CurrentProviderInfo() ProviderInfoer {
	return noprovider.NoProvider{}
}
