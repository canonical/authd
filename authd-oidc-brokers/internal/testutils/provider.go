package testutils

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/genericprovider"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"github.com/ubuntu/authd/log"
	"golang.org/x/oauth2"
)

const (
	// ExpiredRefreshToken is used to test the expired refresh token error.
	ExpiredRefreshToken = "expired-refresh-token"
	// IsForDeviceRegistrationClaim is the claim used to indicate to the mock provider if the token is for device registration.
	IsForDeviceRegistrationClaim = "is_for_device_registration"
)

// MockKey is the RSA key used to sign the JWTs for the mock provider.
var MockKey *rsa.PrivateKey

var mockCertificate *x509.Certificate

func init() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("Setup: Could not generate RSA key for the Mock: %v", err))
	}
	MockKey = key

	certTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2024),
		Subject: pkix.Name{
			Organization: []string{"Mocks ltd."},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		SubjectKeyId:          []byte{1, 2, 3, 4, 5},
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	c, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &MockKey.PublicKey, MockKey)
	if err != nil {
		panic("Setup: Could not create certificate for the Mock")
	}

	cert, err := x509.ParseCertificate(c)
	if err != nil {
		panic("Setup: Could not parse certificate for the Mock")
	}
	mockCertificate = cert
}

// EndpointHandler is a function that handles a request to an OIDC provider endpoint.
type EndpointHandler func(http.ResponseWriter, *http.Request)

type providerServerOption struct {
	handlers map[string]EndpointHandler
}

// ProviderServerOption is a function that allows to override default options of the mock provider.
type ProviderServerOption func(*providerServerOption)

// WithHandler returns a ProviderServerOption that adds a handler for a provider endpoint specified by path.
func WithHandler(path string, handler func(http.ResponseWriter, *http.Request)) ProviderServerOption {
	return func(o *providerServerOption) {
		o.handlers[path] = handler
	}
}

// StartMockProviderServer starts a new HTTP server to be used as an OIDC provider for tests.
func StartMockProviderServer(address string, tokenHandlerOpts *TokenHandlerOptions, args ...ProviderServerOption) (string, func()) {
	servMux := http.NewServeMux()
	server := httptest.NewUnstartedServer(servMux)

	if address != "" {
		l, err := net.Listen("tcp", address)
		if err != nil {
			panic(fmt.Sprintf("error starting listener: %v", err))
		}
		server.Listener = l
	}
	server.Start()

	opts := providerServerOption{
		handlers: map[string]EndpointHandler{
			"/.well-known/openid-configuration": DefaultOpenIDHandler(server.URL),
			"/device_auth":                      DefaultDeviceAuthHandler(),
			"/token":                            TokenHandler(server.URL, tokenHandlerOpts),
			"/keys":                             DefaultJWKHandler(),
		},
	}
	for _, arg := range args {
		arg(&opts)
	}

	for path, handler := range opts.handlers {
		if handler == nil {
			continue
		}
		servMux.HandleFunc(path, handler)
	}

	return server.URL, func() {
		server.Close()
	}
}

// DefaultOpenIDHandler returns a handler that returns a default OIDC configuration.
func DefaultOpenIDHandler(serverURL string) EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		wellKnown := fmt.Sprintf(`{
			"issuer": "%[1]s",
			"authorization_endpoint": "%[1]s/auth",
			"device_authorization_endpoint": "%[1]s/device_auth",
			"token_endpoint": "%[1]s/token",
			"jwks_uri": "%[1]s/keys",
			"id_token_signing_alg_values_supported": ["RS256"]
		}`, serverURL)

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(wellKnown))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// OpenIDHandlerWithNoDeviceEndpoint returns a handler that returns an OIDC configuration without device endpoint.
func OpenIDHandlerWithNoDeviceEndpoint(serverURL string) EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		wellKnown := fmt.Sprintf(`{
			"issuer": "%[1]s",
			"authorization_endpoint": "%[1]s/auth",
			"token_endpoint": "%[1]s/token",
			"jwks_uri": "%[1]s/keys",
			"id_token_signing_alg_values_supported": ["RS256"]
		}`, serverURL)

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(wellKnown))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// DefaultDeviceAuthHandler returns a handler that returns a default device auth response.
func DefaultDeviceAuthHandler() EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		response := `{
			"device_code": "device_code",
			"user_code": "user_code",
			"verification_uri": "https://verification_uri.com"
		}`

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// TokenHandlerOptions contains options for the token handler.
type TokenHandlerOptions struct {
	Scopes []string
	// A list of custom claims to be added to the ID token. Each time the
	// handler returns a token, the claims from the first element of the list
	// will be added to the token, and then that element will be removed from
	// the list.
	IDTokenClaims []map[string]interface{}
}

var idTokenClaimsMutex sync.Mutex

// TokenHandler returns a handler that returns a default token response.
func TokenHandler(serverURL string, opts *TokenHandlerOptions) EndpointHandler {
	if opts == nil {
		opts = &TokenHandlerOptions{}
	}
	if opts.Scopes == nil {
		opts.Scopes = consts.DefaultScopes
	}

	return func(w http.ResponseWriter, r *http.Request) {
		s, err := httputil.DumpRequest(r, true)
		if err != nil {
			log.Errorf(context.Background(), "could not dump request: %v", err)
		}
		log.Debugf(context.Background(), "/token endpoint request:\n%s", s)

		// Handle expired refresh token
		refreshToken := r.FormValue("refresh_token")
		if refreshToken == ExpiredRefreshToken {
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			// This is an msentraid specific error code and description.
			_, _ = w.Write([]byte(`{"error": "invalid_grant", "error_description": "AADSTS50173: The refresh token has expired."}`))
			return
		}

		// Mimics user going through auth process
		time.Sleep(2 * time.Second)

		claims := jwt.MapClaims{
			"iss":                serverURL,
			"sub":                "test-user-id",
			"aud":                "test-client-id",
			"exp":                9999999999,
			"name":               "test-user",
			"preferred_username": "test-user-preferred-username@email.com",
			"email":              "test-user@email.com",
			"email_verified":     true,
		}

		idTokenClaimsMutex.Lock()
		// Override the default claims with the custom claims
		if len(opts.IDTokenClaims) > 0 {
			for k, v := range opts.IDTokenClaims[0] {
				claims[k] = v
			}
			opts.IDTokenClaims = opts.IDTokenClaims[1:]
		}
		idTokenClaimsMutex.Unlock()

		idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

		rawToken, err := idToken.SignedString(MockKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}

		response := fmt.Sprintf(`{
			"access_token": "accesstoken",
			"refresh_token": "refreshtoken",
			"token_type": "Bearer",
			"scope": "%s",
			"expires_in": 3600,
			"id_token": "%s"
		}`, strings.Join(opts.Scopes, " "), rawToken)

		w.Header().Add("Content-Type", "application/json")
		if _, err := w.Write([]byte(response)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// DefaultJWKHandler returns a handler that provides the signing keys from the broker.
//
// Meant to be used an the endpoint for /keys.
func DefaultJWKHandler() EndpointHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		jwk := jose.JSONWebKey{
			Key:          &MockKey.PublicKey,
			KeyID:        "fa834459-66c6-475a-852f-444262a07c13_sig_rs256",
			Algorithm:    "RS256",
			Use:          "sig",
			Certificates: []*x509.Certificate{mockCertificate},
		}

		encodedJWK, err := jwk.MarshalJSON()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}

		response := fmt.Sprintf(`{"keys": [%s]}`, encodedJWK)
		w.Header().Add("Content-Type", "application/json")
		if _, err := w.Write([]byte(response)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// UnavailableHandler returns a handler that returns a 503 Service Unavailable response.
func UnavailableHandler() EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// BadRequestHandler returns a handler that returns a 400 Bad Request response.
func BadRequestHandler() EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}
}

// CustomResponseHandler returns a handler that returns a custom token response.
func CustomResponseHandler(response string) EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// HangingHandler returns a handler that hangs the request until the duration has elapsed.
func HangingHandler(d time.Duration) EndpointHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(d)

		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
	}
}

// ExpiryDeviceAuthHandler returns a handler that returns a device auth response with a short expiry time.
func ExpiryDeviceAuthHandler() EndpointHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		response := `{
			"device_code": "device_code",
			"user_code": "user_code",
			"verification_uri": "https://verification_uri.com",
			"expires_in": 1
		}`

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// MockProvider is a mock that implements the Provider interface.
type MockProvider struct {
	genericprovider.GenericProvider
	Scopes                             []string
	Options                            []oauth2.AuthCodeOption
	GetGroupsFunc                      func() ([]info.Group, error)
	FirstCallDelay                     int
	SecondCallDelay                    int
	GetGroupsFails                     bool
	ProviderSupportsDeviceRegistration bool

	numCalls     int
	numCallsLock sync.Mutex
}

// AdditionalScopes returns the additional scopes required by the provider.
func (p *MockProvider) AdditionalScopes() []string {
	if p.Scopes != nil {
		return p.Scopes
	}
	return p.GenericProvider.AdditionalScopes()
}

// AuthOptions returns the additional options required by the provider.
func (p *MockProvider) AuthOptions() []oauth2.AuthCodeOption {
	if p.Options != nil {
		return p.Options
	}
	return p.GenericProvider.AuthOptions()
}

// NormalizeUsername parses a username into a normalized version.
func (p *MockProvider) NormalizeUsername(username string) string {
	return strings.ToLower(username)
}

// GetMetadata is a no-op when no specific provider is in use.
func (p *MockProvider) GetMetadata(provider *oidc.Provider) (map[string]interface{}, error) {
	return nil, nil
}

// GetUserInfo returns the user info parsed from the ID token.
func (p *MockProvider) GetUserInfo(idToken info.Claimer) (info.User, error) {
	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	p.numCallsLock.Lock()
	numCalls := p.numCalls
	p.numCalls++
	p.numCallsLock.Unlock()

	if numCalls == 0 && p.FirstCallDelay > 0 {
		time.Sleep(time.Duration(p.FirstCallDelay) * time.Second)
	}
	if numCalls == 1 && p.SecondCallDelay > 0 {
		time.Sleep(time.Duration(p.SecondCallDelay) * time.Second)
	}

	return info.NewUser(
		userClaims.Email,
		userClaims.Home,
		userClaims.Sub,
		userClaims.Shell,
		userClaims.Gecos,
		nil,
	), nil
}

// GetGroups returns the groups the user is a member of.
func (p *MockProvider) GetGroups(ctx context.Context, clientID string, issuerURL string, token *oauth2.Token, providerMetadata map[string]interface{}, deviceRegistrationData []byte) ([]info.Group, error) {
	if p.GetGroupsFails {
		return nil, errors.New("error requested in the mock")
	}

	userGroups := []info.Group{
		{Name: "remote-test-group", UGID: "12345"},
		{Name: "local-test-group", UGID: ""},
	}

	var err error
	if p.GetGroupsFunc != nil {
		userGroups, err = p.GetGroupsFunc()
		if err != nil {
			return nil, err
		}
	}

	return userGroups, nil
}

// IsTokenForDeviceRegistration checks if the token is for device registration.
func (p *MockProvider) IsTokenForDeviceRegistration(token *oauth2.Token) (bool, error) {
	if token == nil {
		return false, errors.New("token is nil")
	}

	isForDeviceRegistration, ok := token.Extra(IsForDeviceRegistrationClaim).(bool)
	if !ok {
		return false, fmt.Errorf("token does not contain %q claim", IsForDeviceRegistrationClaim)
	}

	return isForDeviceRegistration, nil
}

// SupportsDeviceRegistration checks if the provider supports device registration.
func (p *MockProvider) SupportsDeviceRegistration() bool {
	return p.ProviderSupportsDeviceRegistration
}

type claims struct {
	Email string `json:"email"`
	Sub   string `json:"sub"`
	Home  string `json:"home"`
	Shell string `json:"shell"`
	Gecos string `json:"gecos"`
}

// userClaims returns the user claims parsed from the ID token.
func (p *MockProvider) userClaims(idToken info.Claimer) (claims, error) {
	var userClaims claims
	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}
	return userClaims, nil
}
