//go:build withmsentraid

package providers

import (
	"github.com/ubuntu/oidc-broker/internal/providers/microsoft_entra_id"
)

// CurrentProviderInfo returns a Microsoft Entra ID provider implementation.
func CurrentProviderInfo() ProviderInfoer {
	return microsoft_entra_id.MSEntraIDProvider{}
}
