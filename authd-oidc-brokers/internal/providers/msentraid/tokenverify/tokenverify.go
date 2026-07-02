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
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KeyForKID returns the RSA public key published for the given JWT "kid", or an
// error if it cannot be resolved.
type KeyForKID func(kid string) (*rsa.PublicKey, error)

// replaceNonceValue rewrites the value of the "nonce" field in headerJSON with
// replacement, byte-for-byte, leaving every other byte untouched. Unlike a plain
// substring replace, it anchors to the "nonce" key itself, so it cannot match a
// coincidental occurrence of nonce's text inside another header field's value.
//
// replacement is spliced in raw, without JSON encoding — callers must ensure it
// contains no characters that require JSON escaping (no `"` or `\`). The current
// sole call site passes a SHA256 base64url-encoded string, which satisfies this
// invariant structurally.
func replaceNonceValue(headerJSON []byte, nonce, replacement string) ([]byte, error) {
	i := skipJSONWhitespace(headerJSON, 0)
	if i >= len(headerJSON) || headerJSON[i] != '{' {
		return nil, errors.New("token header is not a JSON object")
	}
	i++

	for {
		i = skipJSONWhitespace(headerJSON, i)
		if i >= len(headerJSON) {
			return nil, errors.New(`"nonce" key not found`)
		}
		if headerJSON[i] == '}' {
			return nil, errors.New(`"nonce" key not found`)
		}

		keyEnd, key, err := scanJSONString(headerJSON, i)
		if err != nil {
			return nil, fmt.Errorf("malformed token header key: %w", err)
		}
		i = skipJSONWhitespace(headerJSON, keyEnd)
		if i >= len(headerJSON) || headerJSON[i] != ':' {
			return nil, errors.New("malformed token header field")
		}
		i = skipJSONWhitespace(headerJSON, i+1)

		if key == "nonce" {
			valueEnd, parsed, err := scanJSONString(headerJSON, i)
			if err != nil {
				return nil, fmt.Errorf(`malformed "nonce" value: %w`, err)
			}
			if parsed != nonce {
				return nil, errors.New(`"nonce" value does not match the parsed header`)
			}

			rewritten := make([]byte, 0, len(headerJSON))
			rewritten = append(rewritten, headerJSON[:i+1]...)
			rewritten = append(rewritten, replacement...)
			rewritten = append(rewritten, headerJSON[valueEnd-1:]...)
			return rewritten, nil
		}

		next, err := skipJSONValue(headerJSON, i)
		if err != nil {
			return nil, fmt.Errorf("malformed token header field %q: %w", key, err)
		}
		i = next
		i = skipJSONWhitespace(headerJSON, i)
		if i >= len(headerJSON) {
			return nil, errors.New("unterminated token header object")
		}
		switch headerJSON[i] {
		case ',':
			i++
		case '}':
			return nil, errors.New(`"nonce" key not found`)
		default:
			return nil, errors.New("malformed token header object")
		}
	}
}

func skipJSONWhitespace(data []byte, start int) int {
	for start < len(data) {
		switch data[start] {
		case ' ', '\n', '\r', '\t':
			start++
		default:
			return start
		}
	}
	return start
}

func scanJSONString(data []byte, start int) (end int, value string, err error) {
	if start >= len(data) || data[start] != '"' {
		return 0, "", errors.New("expected JSON string")
	}
	for end := start + 1; end < len(data); end++ {
		if data[end] == '\\' {
			end++
			continue
		}
		if data[end] == '"' {
			if err := json.Unmarshal(data[start:end+1], &value); err != nil {
				return 0, "", err
			}
			return end + 1, value, nil
		}
	}
	return 0, "", errors.New("unterminated JSON string")
}

func skipJSONValue(data []byte, start int) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(data[start:]))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return 0, err
	}
	return start + int(decoder.InputOffset()), nil
}

// Verify checks that rawToken is an RS256 JWT whose signature validates against
// the key resolved for its "kid", and that its "tid" (tenant) claim equals
// expectedTenantID. It returns nil only on success.
func Verify(rawToken, expectedTenantID string, keyForKID KeyForKID) error {
	if expectedTenantID == "" {
		return errors.New("expectedTenantID must not be empty")
	}

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
		rewritten, err := replaceNonceValue(headerJSON, header.Nonce, hashed)
		if err != nil {
			return fmt.Errorf("could not locate nonce field in token header: %w", err)
		}
		signingInput = base64.RawURLEncoding.EncodeToString(rewritten) + "." + parts[1]
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
		Tid string      `json:"tid"`
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return fmt.Errorf("could not parse token payload: %w", err)
	}
	// Reject expired tokens. Microsoft Entra access tokens typically have a
	// 1-hour lifetime; allow a 60-second clock-skew window.
	exp, err := claims.Exp.Int64()
	if err != nil || exp == 0 {
		return errors.New("token payload missing valid exp claim")
	}
	now := time.Now().Unix()
	if now-60 > exp {
		return fmt.Errorf("token expired at %d (now: %d)", exp, now)
	}
	if claims.Tid != expectedTenantID {
		return fmt.Errorf("token tenant %q does not match expected tenant %q", claims.Tid, expectedTenantID)
	}

	return nil
}
