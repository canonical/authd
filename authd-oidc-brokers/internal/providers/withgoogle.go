//go:build withgoogle

package providers

import "github.com/ubuntu/authd-oidc-brokers/internal/providers/google"

// CurrentProviderInfo returns a Google provider implementation.
func CurrentProviderInfo() ProviderInfoer {
	return google.New()
}
