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

func TestNewRemoteKeySetNilClientUsesDefault(t *testing.T) {
	t.Parallel()
	// Passing nil must not panic — it falls back to http.DefaultClient.
	ks := tokenverify.NewRemoteKeySet("http://localhost:0", nil)
	// A fetch to a definitely-closed port will error, but the important thing
	// is that the keyset was constructed without panic.
	_, err := ks.KeyForKID(context.Background(), "any-kid")
	require.Error(t, err, "should error when the JWKS endpoint is unreachable")
}

func TestRemoteKeySetFetchErrors(t *testing.T) {
	t.Parallel()

	t.Run("HTTP_fetch_failure", func(t *testing.T) {
		t.Parallel()
		// A server that closes immediately triggers a fetch error.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Close the connection without writing anything.
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack not supported", http.StatusInternalServerError)
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err := ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
	})

	t.Run("Non_200_status", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err := ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "500")
	})

	t.Run("Invalid_JSON_body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not-json"))
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err := ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
	})

	t.Run("Non_RSA_key_is_skipped", func(t *testing.T) {
		t.Parallel()
		// Serve a JWKS with a non-RSA key type; KeyForKID should skip it and
		// return a "no signing key" error.
		doc := map[string]any{"keys": []map[string]string{{
			"kid": testKID, "kty": "EC",
		}}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(doc))
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err := ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
	})

	t.Run("Invalid_base64_N_is_skipped", func(t *testing.T) {
		t.Parallel()
		doc := map[string]any{"keys": []map[string]string{{
			"kid": testKID, "kty": "RSA",
			"n": "!!!invalid-base64!!!",
			"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(doc))
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err := ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
	})

	t.Run("Invalid_base64_E_is_skipped", func(t *testing.T) {
		t.Parallel()
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		doc := map[string]any{"keys": []map[string]string{{
			"kid": testKID, "kty": "RSA",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": "!!!invalid-base64!!!",
		}}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(doc))
		}))
		defer srv.Close()

		ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())
		_, err = ks.KeyForKID(context.Background(), testKID)
		require.Error(t, err)
	})
}

func TestNewRemoteKeySetInvalidURLFailsOnFetch(t *testing.T) {
	t.Parallel()
	// A URL containing a control character causes http.NewRequestWithContext to
	// fail when the keyset tries to build its JWKS request.
	ks := tokenverify.NewRemoteKeySet("http://\x00invalid", http.DefaultClient)
	_, err := ks.KeyForKID(context.Background(), testKID)
	require.Error(t, err, "an invalid JWKS URI must error on fetch")
}

func TestRemoteKeySetCachesKeyAfterFetch(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		doc := map[string]any{"keys": []map[string]string{{
			"kid": testKID, "kty": "RSA",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}}
		require.NoError(t, json.NewEncoder(w).Encode(doc))
	}))
	defer srv.Close()

	ks := tokenverify.NewRemoteKeySet(srv.URL, srv.Client())

	// First call fetches from the server.
	k1, err := ks.KeyForKID(context.Background(), testKID)
	require.NoError(t, err)
	require.Equal(t, 1, fetchCount, "first call should fetch from the server")

	// Second call must return the cached key without a second fetch.
	k2, err := ks.KeyForKID(context.Background(), testKID)
	require.NoError(t, err)
	require.Equal(t, 1, fetchCount, "second call must use the cache, not re-fetch")
	require.Equal(t, k1, k2, "cached key must match the fetched key")
}
