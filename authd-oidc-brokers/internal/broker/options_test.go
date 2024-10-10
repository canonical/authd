package broker

import "github.com/ubuntu/authd-oidc-brokers/internal/providers"

// WithCustomProviderInfo returns an option that sets a custom provider infoer for the broker.
func WithCustomProviderInfo(p providers.ProviderInfoer) Option {
	return func(o *option) {
		o.providerInfo = p
	}
}
