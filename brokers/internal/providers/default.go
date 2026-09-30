//go:build !withgoogle && !withmsentraid

package providers

import (
	"github.com/canonical/authd/brokers/internal/providers/genericprovider"
)

// CurrentProvider returns a generic oidc provider implementation.
func CurrentProvider() Provider {
	return genericprovider.New()
}
