package tokenverify

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
)

// RemoteKeySet fetches and caches the RSA signing keys published at a JWKS URI
// (e.g. the tenant's `.../discovery/v2.0/keys`). It refetches on a cache miss so
// that key rotation is handled transparently.
type RemoteKeySet struct {
	jwksURI string
	client  *http.Client

	mu   sync.Mutex
	keys map[string]*rsa.PublicKey
}

// NewRemoteKeySet returns a RemoteKeySet for the given JWKS URI. If client is
// nil, http.DefaultClient is used.
func NewRemoteKeySet(jwksURI string, client *http.Client) *RemoteKeySet {
	if client == nil {
		client = http.DefaultClient
	}
	return &RemoteKeySet{jwksURI: jwksURI, client: client}
}

// KeyForKID returns the RSA public key for kid, fetching the JWKS if it is not
// already cached. It satisfies the KeyForKID signature used by Verify.
func (r *RemoteKeySet) KeyForKID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if k := r.cached(kid); k != nil {
		return k, nil
	}
	if err := r.fetch(ctx); err != nil {
		return nil, err
	}
	if k := r.cached(kid); k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("no signing key with kid %q in JWKS", kid)
}

func (r *RemoteKeySet) cached(kid string) *rsa.PublicKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[kid]
}

func (r *RemoteKeySet) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.jwksURI, nil)
	if err != nil {
		return fmt.Errorf("could not build JWKS request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("could not parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}

	r.mu.Lock()
	r.keys = keys
	r.mu.Unlock()
	return nil
}
