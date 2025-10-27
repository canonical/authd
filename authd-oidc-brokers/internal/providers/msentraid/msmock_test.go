//go:build withmsentraid

package msentraid_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
)

var mockMSServerForDeviceRegistration *mockMSServer
var mockMSServerForDeviceRegistrationOnce sync.Once

func ensureMockMSServerForDeviceRegistration(t *testing.T) {
	mockMSServerForDeviceRegistrationOnce.Do(func() {
		mockMSServerForDeviceRegistration, _ = startMockMSServer(t, &mockMSServerConfig{
			GroupEndpointHandler: simpleGroupHandler,
		})

		himmelblau.SetAuthorityBaseURL(t, mockMSServerForDeviceRegistration.URL)
		err := os.Setenv("HIMMELBLAU_DISCOVERY_URL", mockMSServerForDeviceRegistration.URL)
		require.NoError(t, err, "failed to set HIMMELBLAU_DISCOVERY_URL")
	})
}

type mockMSServer struct {
	*httptest.Server

	rsaPrivateKey      *rsa.PrivateKey
	transportKeyBySPKI map[string]*rsa.PublicKey
	transportKeyMu     sync.RWMutex
}

type mockMSServerConfig struct {
	// TenantID is the tenant ID to use in the token endpoint URL.
	// If empty, requests to the token endpoint will be accepted for any tenant.
	TenantID             string
	GroupEndpointHandler http.HandlerFunc
}

func startMockMSServer(t *testing.T, config *mockMSServerConfig) (mockServer *mockMSServer, cleanup func()) {
	if config == nil {
		config = &mockMSServerConfig{}
	}

	if config.GroupEndpointHandler == nil {
		config.GroupEndpointHandler = simpleGroupHandler
	}

	rsaPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "failed to generate RSA private key")

	m := &mockMSServer{
		rsaPrivateKey:      rsaPrivateKey,
		transportKeyBySPKI: make(map[string]*rsa.PublicKey),
	}

	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "Mock MS server received request: %s %s\n", r.Method, r.URL.Path)

		switch {
		// ===== login.microsoftonline.com =====
		case r.Method == http.MethodPost && isTokenEndpoint(r.URL.Path, config.TenantID):
			m.handleTokenRequest(t, w, r)

		case r.Method == http.MethodGet && isAuthorizeEndpoint(r.URL.Path, config.TenantID):
			m.handleAuthorizeRequest(t, w, r)

		// ===== enterpriseregistration.windows.net =====
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/Discover"):
			m.handleDiscoverRequest(t, w, r)

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/EnrollmentServer/device/"):
			m.handleDeviceEnrollmentRequest(t, w, r)

		// ===== graph.microsoft.com =====
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/me/transitiveMemberOf/graph.group"):
			config.GroupEndpointHandler(w, r)

		default:
			require.Fail(t, "unexpected request", "path=%s, method=%s", r.URL.Path, r.Method)
		}
	}))

	cleanup = func() { m.Close() }

	return m, cleanup
}

func (m *mockMSServer) Close() {
	m.Server.Close()
}

// isTokenEndpoint returns true if the given path is the token endpoint for the given tenant.
// Both the v2.0 and v1.0 endpoints are supported.
// If tenantID is empty, requests for any tenant will be accepted.
func isTokenEndpoint(path, tenantID string) bool {
	path = strings.ToLower(path)

	if tenantID != "" {
		return path == "/"+tenantID+"/oauth2/v2.0/token" || path == "/"+tenantID+"/oauth2/token"
	}
	return strings.HasSuffix(path, "/oauth2/v2.0/token") || strings.HasSuffix(path, "/oauth2/token")
}

func isAuthorizeEndpoint(path, tenantID string) bool {
	path = strings.ToLower(path)

	if tenantID != "" {
		return path == "/"+tenantID+"/oauth2/v2.0/authorize"
	}
	return strings.HasSuffix(path, "/oauth2/v2.0/authorize")
}

// ----- handlers -----

func (m *mockMSServer) handleTokenRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	require.NoError(t, err, "failed to parse form")

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err, "failed to read request body")
	fmt.Fprintf(os.Stderr, "Mock MS server received token request - form: %s, body: %s\n", r.Form, string(body))

	// The grant_type can be passed as form data or as the body
	grantType := r.Form.Get("grant_type")
	if grantType == "" && strings.HasPrefix(string(body), "grant_type=") {
		grantType = strings.TrimPrefix(string(body), "grant_type=")
	}

	switch grantType {
	case "refresh_token":
		m.handleRefreshTokenRequest(t, w, r)

	case "srv_challenge":
		m.handleNonceRequest(t, w, r)

	case "urn:ietf:params:oauth:grant-type:jwt-bearer":
		m.handlePRTRequest(t, w, r)

	case "authorization_code":
		m.handleAuthorizationCodeRequest(t, w, r)

	default:
		t.Fatalf("unexpected grant_type in token request: %s", grantType)
	}
}

func (m *mockMSServer) handleAuthorizeRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	// Example path: /<tenant>/oAuth2/v2.0/authorize
	// Example query: client_id=...&response_type=code&redirect_uri=...&client-request-id=...&scope=...

	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	respType := q.Get("response_type")
	scope := q.Get("scope")
	reqID := q.Get("client-request-id")

	fmt.Fprintf(os.Stderr, "Mock MS server authorize request: client_id=%s, response_type=%s, redirect_uri=%s, scope=%s, client-request-id=%s\n",
		clientID, respType, redirectURI, scope, reqID)

	require.Equal(t, "code", respType, "unexpected response_type")
	require.NotEmpty(t, redirectURI, "missing redirect_uri")

	// Construct the final redirect URL: {redirect_uri}?code=...
	redir, err := url.Parse(redirectURI)
	require.NoError(t, err, "failed to parse redirect_uri")
	params := redir.Query()
	params.Set("code", "mock-code")
	redir.RawQuery = params.Encode()
	redirectStr := redir.String()

	// The client’s success branch looks for:
	//   document.location.replace("...")  (with \u0026 allowed for '&')
	jsURL := strings.ReplaceAll(redirectStr, "&", `\u0026`)

	htmlBody := fmt.Sprintf(`<!doctype html>
<html>
  <head><meta charset="utf-8"><title>Working…</title></head>
  <body>
    <script>document.location.replace("%s")</script>
    <noscript><a href="%s">Continue</a></noscript>
  </body>
</html>`, jsURL, html.EscapeString(redirectStr))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(htmlBody))
}

func (m *mockMSServer) handlePRTRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server responding with PRT\n")

	reqJWT := r.Form.Get("request")
	spki := spkiFromJWTx5c(t, reqJWT)
	m.transportKeyMu.Lock()
	transportKey, ok := m.transportKeyBySPKI[spki]
	m.transportKeyMu.Unlock()
	require.True(t, ok, "no transport key for SPKI %s, transport key map: %v", spki, m.transportKeyBySPKI)

	resp := map[string]any{
		"token_type":               "Bearer",
		"expires_in":               "3600",
		"ext_expires_in":           "3600",
		"expires_on":               "9999999999",
		"refresh_token":            "mock-refresh-token",
		"refresh_token_expires_in": 7200,
		"session_key_jwe":          m.generateMockJWE(t, transportKey),
		"id_token": map[string]string{
			"name": "Mock User",
			"oid":  "00000000-0000-0000-0000-000000000000",
			"tid":  "11111111-1111-1111-1111-111111111111",
		},
		"client_info": map[string]string{
			"uid":  "11111111-1111-1111-1111-111111111111",
			"utid": "22222222-2222-2222-2222-222222222222",
		},
	}
	fmt.Fprintf(os.Stderr, "Mock MS server responding with PRT: %+v\n", resp)

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

func (m *mockMSServer) handleAuthorizationCodeRequest(t *testing.T, w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server handling authorization code request\n")

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{})
	accessTokenStr, err := accessToken.SignedString(m.rsaPrivateKey)
	require.NoError(t, err, "failed to sign access token")

	resp := map[string]interface{}{
		"token_type":     "Bearer",
		"expires_in":     3600,
		"ext_expires_in": 3600,
		"access_token":   accessTokenStr,
		"refresh_token":  "mock-refresh-token",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

func (m *mockMSServer) handleNonceRequest(t *testing.T, w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server responding with nonce\n")
	resp := map[string]string{
		"Nonce": "mock-nonce-1234",
	}
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

func (m *mockMSServer) handleRefreshTokenRequest(t *testing.T, w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server responding with access token from refresh\n")
	resp := map[string]any{
		"token_type":     "Bearer",
		"expires_in":     3600,
		"ext_expires_in": 3600,
		"access_token":   "mock_access_token",
		"refresh_token":  "mock_refresh_token",
		"id_token": map[string]string{
			"name": "Mock User",
			"oid":  "00000000-0000-0000-0000-000000000000",
			"tid":  "11111111-1111-1111-1111-111111111111",
		},
		"client_info": map[string]string{
			"uid":  "11111111-1111-1111-1111-111111111111",
			"utid": "22222222-2222-2222-2222-222222222222",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

func (m *mockMSServer) handleDiscoverRequest(t *testing.T, w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server responding with enrollment discovery\n")
	resp := map[string]any{
		"DeviceJoinService": map[string]string{
			"JoinEndpoint": m.URL + "/EnrollmentServer/device/",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

func (m *mockMSServer) handleDeviceEnrollmentRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(os.Stderr, "Mock MS server responding with device enrollment\n")

	var req struct {
		CertificateRequest struct {
			Data string `json:"Data"`
			Type string `json:"Type"`
		} `json:"CertificateRequest"`
		TransportKey string `json:"TransportKey"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	require.NoError(t, err, "failed to parse request")

	csrDER, err := base64.StdEncoding.DecodeString(req.CertificateRequest.Data)
	require.NoError(t, err, "failed to decode CSR data")

	csr, err := x509.ParseCertificateRequest(csrDER)
	require.NoError(t, err, "failed to parse CSR")

	m.transportKeyMu.RLock()
	m.transportKeyBySPKI[spkiFingerprint(csr.RawSubjectPublicKeyInfo)] = parseBcryptRSAPublicBlob(t, req.TransportKey)
	m.transportKeyMu.RUnlock()

	// Create a self-signed cert using the CSR's public key
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               csr.Subject,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, csr.PublicKey, m.rsaPrivateKey)
	require.NoError(t, err, "failed to create certificate")

	resp := map[string]any{
		"Certificate": map[string]string{
			"RawBody": base64.StdEncoding.EncodeToString(certDER),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(resp)
	require.NoError(t, err, "failed to encode response")
}

// ----- group endpoint handlers -----

// simpleGroupHandler simulates a successful response with a list of groups.
func simpleGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// localGroupHandler simulates a successful response with a list of local groups.
func localGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "local-id1", "displayName": "linux-local1", "securityEnabled": true},
			{"id": "local-id2", "displayName": "linux-local2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// mixedGroupHandler simulates a successful response with a list of mixed remote and local groups.
func mixedGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "local-id1", "displayName": "linux-local1", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// nonSecurityGroupHandler simulates a successful response with a list of groups including non-security groups.
func nonSecurityGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "non-security-id", "displayName": "non-security"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// missingIDGroupHandler simulates a successful response with a list of groups missing the ID field.
func missingIDGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"displayName": "Group1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// missingDisplayNameGroupHandler simulates a successful response with a list of groups missing the displayName field.
func missingDisplayNameGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// errorGroupHandler simulates an error response from the server.
func errorGroupHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
}

// ----- helpers -----

// parseBcryptRSAPublicBlob decodes a BCRYPT_RSAPUBLIC_BLOB ("RSA1" magic, little-endian header) as used by the Windows CryptoAPI.
// This format is not supported by the Go standard library.
func parseBcryptRSAPublicBlob(t *testing.T, b64 string) *rsa.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err, "failed to base64-decode transport key")

	require.Equal(t, "RSA1", string(raw[:4]), "invalid RSA1 blob")

	cbExp := binary.LittleEndian.Uint32(raw[8:12])
	cbMod := binary.LittleEndian.Uint32(raw[12:16])

	offset := 24
	eBytes := raw[offset : offset+int(cbExp)]
	offset += int(cbExp)
	mBytes := raw[offset : offset+int(cbMod)]

	// Convert exponent bytes (big-endian) to int
	e := 0
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}

	n := new(big.Int).SetBytes(mBytes) // modulus is big-endian in this blob

	return &rsa.PublicKey{N: n, E: e}
}

func (m *mockMSServer) generateMockJWE(t *testing.T, transportKey *rsa.PublicKey) string {
	opts := &jose.EncrypterOptions{}

	encrypter, err := jose.NewEncrypter(
		jose.A256GCM,
		jose.Recipient{Algorithm: jose.RSA_OAEP, Key: transportKey},
		opts,
	)
	require.NoError(t, err, "failed to create encrypter")

	// Payload is ignored by the client, keep it simple
	payload := []byte(`{}`)

	obj, err := encrypter.Encrypt(payload)
	require.NoError(t, err, "failed to encrypt payload")

	jwe, err := obj.CompactSerialize()
	require.NoError(t, err, "failed to serialize JWE")

	return jwe
}

// Extract SPKI fingerprint from the request JWT's header x5c.
func spkiFromJWTx5c(t *testing.T, reqJWT string) string {
	parts := strings.Split(reqJWT, ".")
	require.GreaterOrEqual(t, len(parts), 2, "invalid JWT")

	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err, "decode header")

	var hdr struct {
		X5c []string `json:"x5c"`
	}
	err = json.Unmarshal(hdrJSON, &hdr)
	require.NoError(t, err, "unmarshal header")
	require.NotEmpty(t, hdr.X5c, "x5c missing")

	der, err := base64.StdEncoding.DecodeString(hdr.X5c[0])
	require.NoError(t, err, "x5c b64")

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err, "x5c parse cert")

	return spkiFingerprint(cert.RawSubjectPublicKeyInfo)
}

func spkiFingerprint(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return hex.EncodeToString(sum[:])
}
