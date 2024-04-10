package broker

import "github.com/ubuntu/oidc-broker/internal/providers"

// WithSkipSignatureCheck returns an option that skips the JWT signature check.
func WithSkipSignatureCheck() Option {
	return func(o *option) {
		o.skipJWTSignatureCheck = true
	}
}

// WithCustomProviderInfo returns an option that sets a custom provider infoer for the broker.
func WithCustomProviderInfo(p providers.ProviderInfoer) Option {
	return func(o *option) {
		o.providerInfo = p
	}
}
