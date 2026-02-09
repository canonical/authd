//go:build withmsentraid

package providers

import (
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid"
)

// CurrentProvider returns a Microsoft Entra ID provider implementation.
func CurrentProvider() Provider {
	return msentraid.New()
}
