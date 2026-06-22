package tokenverify_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/tokenverify"
	"github.com/stretchr/testify/require"
)

const testKID = "test-kid"
const testTenant = "03c73201-ef9e-4182-ae04-0adb51f4a0b6"

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signToken builds a JWT served as `servedHeaderJSON.payload.sig`, where the
// signature is computed over `signedHeaderJSON.payload`. Passing different served
// and signed headers reproduces Microsoft's nonce behavior (served header carries
// the plaintext nonce; the signature was made over the SHA256-rewritten header).
func signToken(t *testing.T, key *rsa.PrivateKey, servedHeaderJSON, signedHeaderJSON, payloadJSON []byte) string {
	t.Helper()
	pSeg := b64(payloadJSON)
	signingInput := b64(signedHeaderJSON) + "." + pSeg
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err, "signing test token")
	return b64(servedHeaderJSON) + "." + pSeg + "." + b64(sig)
}

func hashedNonce(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestVerify(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	resolver := func(kid string) (*rsa.PublicKey, error) {
		if kid != testKID {
			return nil, fmt.Errorf("unexpected kid %q", kid)
		}
		return &key.PublicKey, nil
	}
	payload, err := json.Marshal(map[string]any{"tid": testTenant, "upn": "user@example.com"})
	require.NoError(t, err)

	t.Run("Valid token without a nonce", func(t *testing.T) {
		t.Parallel()
		hdr, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID})
		require.NoError(t, err)
		tok := signToken(t, key, hdr, hdr, payload)
		require.NoError(t, tokenverify.Verify(tok, testTenant, resolver))
	})

	t.Run("Valid token with a header nonce (Microsoft rewrite)", func(t *testing.T) {
		t.Parallel()
		nonce := "L6EHQ7sCDM6k8EzwUmsHIDihoWwKBOaFXu4ShzY33J8"
		served, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID, "nonce": nonce})
		require.NoError(t, err)
		signed, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID, "nonce": hashedNonce(nonce)})
		require.NoError(t, err)
		tok := signToken(t, key, served, signed, payload)
		// Plain verification (no rewrite) must fail; our Verify must pass.
		require.NoError(t, tokenverify.Verify(tok, testTenant, resolver),
			"a nonce-bearing token must verify via the SHA256 header rewrite")
	})

	t.Run("Nonce rewrite preserves header byte order", func(t *testing.T) {
		t.Parallel()
		// Hand-craft a header with a non-alphabetical key order and the nonce not
		// last, to prove Verify rewrites in place rather than re-marshalling (which
		// would reorder keys and break the signature).
		nonce := "ZZZnonceVALUE-0123456789_abcdefghijklmnopq"
		served := []byte(`{"typ":"JWT","nonce":"` + nonce + `","alg":"RS256","kid":"` + testKID + `"}`)
		signed := []byte(`{"typ":"JWT","nonce":"` + hashedNonce(nonce) + `","alg":"RS256","kid":"` + testKID + `"}`)
		tok := signToken(t, key, served, signed, payload)
		require.NoError(t, tokenverify.Verify(tok, testTenant, resolver))
	})

	t.Run("Tampered payload fails", func(t *testing.T) {
		t.Parallel()
		hdr, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID})
		require.NoError(t, err)
		tok := signToken(t, key, hdr, hdr, payload)
		// Swap the payload segment for a different (unsigned) one.
		evil, err := json.Marshal(map[string]any{"tid": testTenant, "upn": "attacker@example.com"})
		require.NoError(t, err)
		tampered := b64(hdr) + "." + b64(evil) + "." + tokenSig(tok)
		require.Error(t, tokenverify.Verify(tampered, testTenant, resolver))
	})

	t.Run("Wrong tenant fails", func(t *testing.T) {
		t.Parallel()
		hdr, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID})
		require.NoError(t, err)
		tok := signToken(t, key, hdr, hdr, payload)
		require.Error(t, tokenverify.Verify(tok, "some-other-tenant", resolver))
	})

	t.Run("Non-RS256 alg is rejected", func(t *testing.T) {
		t.Parallel()
		hdr, err := json.Marshal(map[string]any{"alg": "none", "kid": testKID})
		require.NoError(t, err)
		tok := signToken(t, key, hdr, hdr, payload)
		require.Error(t, tokenverify.Verify(tok, testTenant, resolver))
	})

	t.Run("Not a JWT is rejected", func(t *testing.T) {
		t.Parallel()
		require.Error(t, tokenverify.Verify("accesstoken", testTenant, resolver))
	})
}

func tokenSig(tok string) string {
	// last dot-separated segment
	for i := len(tok) - 1; i >= 0; i-- {
		if tok[i] == '.' {
			return tok[i+1:]
		}
	}
	return ""
}
