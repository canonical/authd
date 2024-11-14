//go:build withgoogle

package providers

import "github.com/ubuntu/authd-oidc-brokers/internal/providers/google"

// CurrentProvider returns a Google provider implementation.
func CurrentProvider() Provider {
	return google.New()
}
