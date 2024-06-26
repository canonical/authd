package testutils

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/oidc-broker/internal/providers/group"
	"golang.org/x/oauth2"
)

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
func StartMockProvider(args ...OptionProvider) (*httptest.Server, func()) {
	servMux := http.NewServeMux()
	server := httptest.NewServer(servMux)

	opts := optionProvider{
		handlers: map[string]ProviderHandler{
			"/.well-known/openid-configuration": DefaultOpenIDHandler(server.URL),
			"/device_auth":                      DefaultDeviceAuthHandler(),
			"/token":                            DefaultTokenHandler(server.URL),
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
func DefaultTokenHandler(serverURL string) ProviderHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		// Mimics user going through auth process
		time.Sleep(3 * time.Second)

		idToken := fmt.Sprintf(`{
			"iss": "%s",
			"sub": "test-user-id",
			"aud": "test-client-id",
			"exp": 9999999999,
			"name": "test-user",
			"preferred_username": "User Test",
			"email": "test-user@email.com",
			"email_verified": true
		}`, serverURL)

		// The token must be JWT formatted, even though we ignore the validation in the broker during the tests.
		rawToken := fmt.Sprintf(".%s.", base64.RawURLEncoding.EncodeToString([]byte(idToken)))

		response := fmt.Sprintf(`{
			"access_token": "accesstoken",
			"refresh_token": "refreshtoken",
			"token_type": "Bearer",
			"scope": "offline_access openid profile",
			"expires_in": 3600,
			"id_token": "%s"
		}`, rawToken)

		w.Header().Add("Content-Type", "application/json")
		_, err := w.Write([]byte(response))
		if err != nil {
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

// HangingHandler returns a handler that hangs the request until the context is done.
func HangingHandler(ctx context.Context) ProviderHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		<-ctx.Done()

		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
	}
}

// MockProviderInfoer is a mock that implements the ProviderInfoer interface.
type MockProviderInfoer struct {
	Scopes    []string
	Options   []oauth2.AuthCodeOption
	Groups    []group.Info
	GroupsErr bool
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

// GetGroups returns the groups the user is a member of.
func (p *MockProviderInfoer) GetGroups(*oauth2.Token) ([]group.Info, error) {
	if p.GroupsErr {
		return nil, errors.New("error requested in the mock")
	}
	if p.Groups != nil {
		return p.Groups, nil
	}
	return nil, nil
}

// CurrentAuthenticationModesOffered returns the authentication modes supported by the provider.
func (p *MockProviderInfoer) CurrentAuthenticationModesOffered(sessionMode string, supportedAuthModes map[string]string, tokenExists bool, currentAuthStep int) ([]string, error) {
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
		offeredModes = []string{"device_auth"}
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
