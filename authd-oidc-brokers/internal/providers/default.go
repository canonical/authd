//go:build !withgoogle && !withmsentraid

package providers

import (
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/noprovider"
)

// CurrentProvider returns a generic oidc provider implementation.
func CurrentProvider() Provider {
	return noprovider.New()
}
