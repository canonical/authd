package broker

import "github.com/ubuntu/authd-oidc-brokers/internal/providers"

// WithCustomProvider returns an option that sets a custom provider for the broker.
func WithCustomProvider(p providers.Provider) Option {
	return func(o *option) {
		o.provider = p
	}
}
