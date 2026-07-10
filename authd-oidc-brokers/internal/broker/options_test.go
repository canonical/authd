package broker

import "github.com/canonical/authd/authd-oidc-brokers/internal/providers"

// WithCustomProvider returns an option that sets a custom provider for the broker.
func WithCustomProvider(p providers.Provider) Option {
	return func(o *option) {
		o.provider = p
	}
}

// FIDOAuthenticator re-exports the unexported fidoAuthenticator interface so
// that external test packages can provide mocks.
type FIDOAuthenticator = fidoAuthenticator

// WithCustomFIDOAuthenticator returns an option that sets a custom FIDO
// authenticator for the broker.
func WithCustomFIDOAuthenticator(a FIDOAuthenticator) Option {
	return func(o *option) {
		o.fido = a
	}
}
