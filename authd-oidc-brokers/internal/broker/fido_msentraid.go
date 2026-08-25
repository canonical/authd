//go:build withmsentraid

package broker

import "github.com/canonical/authd/authd-oidc-brokers/internal/fido"

// defaultFIDOAuthenticator returns the libfido2-backed authenticator used to
// perform WebAuthn assertions with a locally connected security key.
func defaultFIDOAuthenticator() fidoAuthenticator {
	return fido.Authenticator{}
}
