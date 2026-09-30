//go:build withgoogle

package providers

import "github.com/canonical/authd/brokers/internal/providers/google"

// CurrentProvider returns a Google provider implementation.
func CurrentProvider() Provider {
	return google.New()
}
