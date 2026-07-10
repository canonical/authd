//go:build !withmsentraid

package broker

// defaultFIDOAuthenticator returns nil in builds without msentraid support:
// libfido2 is only linked behind the withmsentraid tag, and a nil
// authenticator disables the FIDO auth modes.
func defaultFIDOAuthenticator() fidoAuthenticator {
	return nil
}
