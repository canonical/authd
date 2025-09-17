package msentraid_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

type mockMS struct {
	Server *httptest.Server

	tenant        string
	rsaPrivateKey *rsa.PrivateKey
}

func newMockMS(tenant string) (*mockMS, error) {
	rsaPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %v", err)
	}

	m := &mockMS{
		tenant:        tenant,
		rsaPrivateKey: rsaPrivateKey,
	}

	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "Mock MS received request: %s %s\n", r.Method, r.URL.Path)

		switch {
		// ===== login.microsoftonline.com =====
		case r.Method == http.MethodPost && r.URL.Path == "/"+tenant+"/oauth2/v2.0/token":
			m.handleTokenRequest(w, r)

		// ===== enterpriseregistration.windows.net =====
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/Discover"):
			m.handleDiscoverRequest(w, r)

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/EnrollmentServer/device/"):
			m.handleDeviceEnrollmentRequest(w, r)

		default:
			http.NotFound(w, r)
		}
	}))

	return m, nil
}

func (*mockMS) handleTokenRequest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	fmt.Fprintf(os.Stderr, "Mock MS received token request: %s\n", r.Form)

	switch r.Form.Get("grant_type") {
	case "refresh_token":
		fmt.Fprintf(os.Stderr, "Mock MS responding with access token from refresh\n")
		resp := map[string]any{
			"token_type":     "Bearer",
			"expires_in":     "3600",
			"ext_expires_in": "3600",
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
		_ = json.NewEncoder(w).Encode(resp)

	case "srv_challenge":
		fmt.Fprintf(os.Stderr, "Mock MS responding with nonce\n")
		resp := map[string]string{
			"nonce": "mock-nonce-1234",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)

	case "urn:ietf:params:oauth:grant-type:jwt-bearer":
		fmt.Fprintf(os.Stderr, "Mock MS responding with access token\n")
		resp := map[string]string{
			"token_type":     "Bearer",
			"expires_in":     "3600",
			"ext_expires_in": "3600",
			"access_token":   "mock-access-token",
			"refresh_token":  "mock-refresh-token",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)

	default:
		fmt.Fprintf(os.Stderr, "Mock MS received unsupported grant_type: %s\n", r.Form.Get("grant_type"))
		http.Error(w, "unsupported grant_type", http.StatusBadRequest)
	}
}

func (m *mockMS) handleDiscoverRequest(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintf(os.Stderr, "Mock MS responding with enrollment discovery\n")
	resp := map[string]any{
		"DeviceJoinService": map[string]string{
			"JoinEndpoint": m.Server.URL + "/EnrollmentServer/device/",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *mockMS) handleDeviceEnrollmentRequest(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(os.Stderr, "Mock MS responding with device enrollment\n")

	var req struct {
		CertificateRequest struct {
			Data string `json:"Data"`
			Type string `json:"Type"`
		} `json:"CertificateRequest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("failed to parse request: %v", err), http.StatusBadRequest)
		return
	}

	csrDER, err := base64.StdEncoding.DecodeString(req.CertificateRequest.Data)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to decode CSR: %v", err), http.StatusBadRequest)
		return
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to parse CSR: %v", err), http.StatusBadRequest)
		return
	}

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
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create certificate: %v", err), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"Certificate": map[string]string{
			"RawBody": base64.StdEncoding.EncodeToString(certDER),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *mockMS) Close() {
	m.Server.Close()
}
