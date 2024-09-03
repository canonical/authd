//go:build withmsentraid

package providers

import (
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/msentraid"
)

// CurrentProviderInfo returns a Microsoft Entra ID provider implementation.
func CurrentProviderInfo() ProviderInfoer {
	return msentraid.New()
}
