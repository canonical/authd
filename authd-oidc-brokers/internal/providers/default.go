//go:build !withmsentraid

package providers

import (
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/noprovider"
)

// CurrentProviderInfo returns a generic oidc provider implementation.
func CurrentProviderInfo() ProviderInfoer {
	return noprovider.New()
}
