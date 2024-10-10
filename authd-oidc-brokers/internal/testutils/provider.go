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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
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

// ProviderHandler is a function that handles a request to the mock provider.
type ProviderHandler func(http.ResponseWriter, *http.Request)

type optionProvider struct {
	handlers map[string]ProviderHandler
}

// OptionProvider is a function that allows to override default options of the mock provider.
type OptionProvider func(*optionProvider)

// WithHandler specifies a handler to the requested path in the mock provider.
func WithHandler(path string, handler func(http.ResponseWriter, *http.Request)) OptionProvider {
	return func(o *optionProvider) {
		o.handlers[path] = handler
	}
}

// StartMockProvider starts a new HTTP server to be used as an OpenID Connect provider for tests.
func StartMockProvider(address string, args ...OptionProvider) (*httptest.Server, func()) {
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

	opts := optionProvider{
		handlers: map[string]ProviderHandler{
			"/.well-known/openid-configuration": DefaultOpenIDHandler(server.URL),
			"/device_auth":                      DefaultDeviceAuthHandler(),
			"/token":                            DefaultTokenHandler(server.URL, consts.DefaultScopes),
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

	return server, func() {
		server.Close()
	}
}

// DefaultOpenIDHandler returns a handler that returns a default OpenID Connect configuration.
func DefaultOpenIDHandler(serverURL string) ProviderHandler {
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

// OpenIDHandlerWithNoDeviceEndpoint returns a handler that returns an OpenID Connect configuration without device endpoint.
func OpenIDHandlerWithNoDeviceEndpoint(serverURL string) ProviderHandler {
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
func DefaultDeviceAuthHandler() ProviderHandler {
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

// DefaultTokenHandler returns a handler that returns a default token response.
func DefaultTokenHandler(serverURL string, scopes []string) ProviderHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		// Mimics user going through auth process
		time.Sleep(2 * time.Second)

		idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss":                serverURL,
			"sub":                "test-user-id",
			"aud":                "test-client-id",
			"exp":                9999999999,
			"name":               "test-user",
			"preferred_username": "test-user@email.com",
			"email":              "test-user@anotheremail.com",
			"email_verified":     true,
		})

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
		}`, strings.Join(scopes, " "), rawToken)

		w.Header().Add("Content-Type", "application/json")
		if _, err := w.Write([]byte(response)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// DefaultJWKHandler returns a handler that provides the signing keys from the broker.
//
// Meant to be used an the endpoint for /keys.
func DefaultJWKHandler() ProviderHandler {
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
func UnavailableHandler() ProviderHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// BadRequestHandler returns a handler that returns a 400 Bad Request response.
func BadRequestHandler() ProviderHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}
}

// CustomResponseHandler returns a handler that returns a custom token response.
func CustomResponseHandler(response string) ProviderHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// HangingHandler returns a handler that hangs the request until the duration has elapsed.
func HangingHandler(d time.Duration) ProviderHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(d)

		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
	}
}

// ExpiryDeviceAuthHandler returns a handler that returns a device auth response with a short expiry time.
func ExpiryDeviceAuthHandler() ProviderHandler {
	return func(w http.ResponseWriter, _ *http.Request) {
		response := `{
			"device_code": "device_code",
			"user_code": "user_code",
			"verification_uri": "https://verification_uri.com",
			"expires_in": 4
		}`

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// MockProviderInfoer is a mock that implements the ProviderInfoer interface.
type MockProviderInfoer struct {
	Scopes    []string
	Options   []oauth2.AuthCodeOption
	Groups    []info.Group
	GroupsErr bool
}

// CheckTokenScopes checks if the token has the required scopes.
func (p *MockProviderInfoer) CheckTokenScopes(token *oauth2.Token) error {
	scopesStr, ok := token.Extra("scope").(string)
	if !ok {
		return fmt.Errorf("failed to cast token scopes to string: %v", token.Extra("scope"))
	}

	scopes := strings.Split(scopesStr, " ")
	var missingScopes []string
	for _, s := range consts.DefaultScopes {
		if !slices.Contains(scopes, s) {
			missingScopes = append(missingScopes, s)
		}
	}
	if len(missingScopes) > 0 {
		return fmt.Errorf("missing required scopes: %s", strings.Join(missingScopes, ", "))
	}
	return nil
}

// AdditionalScopes returns the additional scopes required by the provider.
func (p *MockProviderInfoer) AdditionalScopes() []string {
	if p.Scopes != nil {
		return p.Scopes
	}
	return []string{oidc.ScopeOfflineAccess}
}

// AuthOptions returns the additional options required by the provider.
func (p *MockProviderInfoer) AuthOptions() []oauth2.AuthCodeOption {
	if p.Options != nil {
		return p.Options
	}
	return []oauth2.AuthCodeOption{}
}

// GetUserInfo is a no-op when no specific provider is in use.
func (p *MockProviderInfoer) GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken) (info.User, error) {
	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	userGroups, err := p.getGroups(accessToken)
	if err != nil {
		return info.User{}, err
	}

	// This is a special case for testing purposes. If the username starts with "user-timeout-", we will delay the
	// return for a while to control the authentication order for multiple users.
	if strings.HasPrefix(userClaims.PreferredUserName, "user-timeout") {
		d, err := strconv.Atoi(strings.TrimPrefix(userClaims.PreferredUserName, "user-timeout-"))
		if err != nil {
			return info.User{}, err
		}
		time.Sleep(time.Duration(d) * time.Second)
	}

	return info.NewUser(
		userClaims.PreferredUserName,
		userClaims.Home,
		userClaims.Sub,
		userClaims.Shell,
		userClaims.Gecos,
		userGroups,
	), nil
}

type claims struct {
	PreferredUserName string `json:"preferred_username"`
	Sub               string `json:"sub"`
	Home              string `json:"home"`
	Shell             string `json:"shell"`
	Gecos             string `json:"gecos"`
}

// userClaims returns the user claims parsed from the ID token.
func (p *MockProviderInfoer) userClaims(idToken *oidc.IDToken) (claims, error) {
	var userClaims claims
	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}
	return userClaims, nil
}

// GetGroups returns the groups the user is a member of.
func (p *MockProviderInfoer) getGroups(*oauth2.Token) ([]info.Group, error) {
	if p.GroupsErr {
		return nil, errors.New("error requested in the mock")
	}
	if p.Groups != nil {
		return p.Groups, nil
	}
	return nil, nil
}

// CurrentAuthenticationModesOffered returns the authentication modes supported by the provider.
func (p MockProviderInfoer) CurrentAuthenticationModesOffered(
	sessionMode string,
	supportedAuthModes map[string]string,
	tokenExists bool,
	providerReachable bool,
	endpoints map[string]struct{},
	currentAuthStep int,
) ([]string, error) {
	var offeredModes []string
	switch sessionMode {
	case "passwd":
		if !tokenExists {
			return nil, errors.New("user has no cached token")
		}
		offeredModes = []string{"password"}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}

	default: // auth mode
		if _, ok := endpoints["device_auth"]; ok && providerReachable {
			offeredModes = []string{"device_auth"}
		}
		if tokenExists {
			offeredModes = append([]string{"password"}, offeredModes...)
		}
		if currentAuthStep > 0 {
			offeredModes = []string{"newpassword"}
		}
	}

	for _, mode := range offeredModes {
		if _, ok := supportedAuthModes[mode]; !ok {
			return nil, fmt.Errorf("auth mode %q required by the provider, but is not supported locally", mode)
		}
	}

	return offeredModes, nil
}

// VerifyUsername checks if the requested username matches the authenticated user.
func (p *MockProviderInfoer) VerifyUsername(requestedUsername, username string) error {
	if requestedUsername != username {
		return fmt.Errorf("requested username %q does not match the authenticated user %q", requestedUsername, username)
	}
	return nil
}
