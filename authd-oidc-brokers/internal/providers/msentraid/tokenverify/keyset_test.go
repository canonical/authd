package tokenverify_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/tokenverify"
	"github.com/stretchr/testify/require"
)

// jwksServer serves a JWKS containing the given RSA public key under testKID.
func jwksServer(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	doc := map[string]any{"keys": []map[string]string{{
		"kid": testKID, "kty": "RSA",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(doc))
	}))
}

func TestRemoteKeySetVerifyEndToEnd(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := jwksServer(t, &key.PublicKey)
	defer srv.Close()

	ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
	resolver := func(kid string) (*rsa.PublicKey, error) {
		return ks.KeyForKID(context.Background(), kid)
	}

	payload, err := json.Marshal(map[string]any{"tid": testTenant})
	require.NoError(t, err)
	hdr, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKID})
	require.NoError(t, err)
	tok := signToken(t, key, hdr, hdr, payload)

	require.NoError(t, tokenverify.Verify(tok, testTenant, resolver),
		"token must verify against the key served by the JWKS endpoint")
}

func TestRemoteKeySetUnknownKID(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := jwksServer(t, &key.PublicKey)
	defer srv.Close()

	ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
	_, err = ks.KeyForKID(context.Background(), "not-present")
	require.Error(t, err, "an absent kid must error")
}
