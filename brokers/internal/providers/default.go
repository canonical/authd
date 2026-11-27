//go:build !withgoogle && !withmsentraid

package providers

import (
	"github.com/canonical/authd/brokers/internal/providers/genericprovider"
)

// CurrentProvider returns a generic oidc provider implementation.
func CurrentProvider() Provider {
	// Return a pointer, so that the broker can configure the groups claim
	// through the optional GroupsClaimConfigurer interface.
	p := genericprovider.New()
	return &p
}
