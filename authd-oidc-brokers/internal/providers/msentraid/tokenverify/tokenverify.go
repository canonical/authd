// Package tokenverify verifies the RS256 signature of Microsoft Entra access
// tokens. It is deliberately free of any cgo/libhimmelblau dependency so it can
// be unit-tested on its own.
//
// Microsoft first-party access tokens (e.g. Microsoft Graph-scoped) carry a
// "nonce" in the JWT header that Entra replaces with its SHA256 (base64url
// no-padding) value *before* signing, then serves with the plaintext nonce. As a
// result such tokens do not verify with a standard JWT check; the resource
// applies the same rewrite before validating. Verify reproduces that rewrite so
// both nonce-bearing and plain tokens validate against the tenant JWKS.
package tokenverify

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// KeyForKID returns the RSA public key published for the given JWT "kid", or an
// error if it cannot be resolved.
type KeyForKID func(kid string) (*rsa.PublicKey, error)

// Verify checks that rawToken is an RS256 JWT whose signature validates against
// the key resolved for its "kid", and that its "tid" (tenant) claim equals
// expectedTenantID. It returns nil only on success.
func Verify(rawToken, expectedTenantID string, keyForKID KeyForKID) error {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return errors.New("access token is not a JWT")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("could not decode token header: %w", err)
	}
	var header struct {
		Alg   string `json:"alg"`
		Kid   string `json:"kid"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("could not parse token header: %w", err)
	}
	if header.Alg != "RS256" {
		return fmt.Errorf("unsupported token signature algorithm %q (want RS256)", header.Alg)
	}

	// Reconstruct the bytes Entra actually signed over. For nonce-bearing tokens
	// that means the header with the nonce replaced by its SHA256. We rewrite the
	// raw header bytes in place (rather than re-marshalling) so the byte order is
	// preserved exactly — re-marshalling would reorder keys and break the signature.
	signingInput := parts[0] + "." + parts[1]
	if header.Nonce != "" {
		sum := sha256.Sum256([]byte(header.Nonce))
		hashed := base64.RawURLEncoding.EncodeToString(sum[:])
		rewritten := strings.Replace(string(headerJSON), header.Nonce, hashed, 1)
		signingInput = base64.RawURLEncoding.EncodeToString([]byte(rewritten)) + "." + parts[1]
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("could not decode token signature: %w", err)
	}
	key, err := keyForKID(header.Kid)
	if err != nil {
		return fmt.Errorf("could not resolve signing key %q: %w", header.Kid, err)
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("token signature verification failed: %w", err)
	}

	// Bind the (now signature-verified) token to the expected tenant.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("could not decode token payload: %w", err)
	}
	var claims struct {
		Tid string `json:"tid"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return fmt.Errorf("could not parse token payload: %w", err)
	}
	if expectedTenantID != "" && claims.Tid != expectedTenantID {
		return fmt.Errorf("token tenant %q does not match expected tenant %q", claims.Tid, expectedTenantID)
	}

	return nil
}
