// Package token provides functions to save and load tokens from disk.
package token

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ubuntu/authd-oidc-brokers/internal/providers"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// AuthCachedInfo represents the token that will be saved on disk for offline authentication.
type AuthCachedInfo struct {
	Token                  *oauth2.Token
	ExtraFields            map[string]interface{}
	RawIDToken             string
	ProviderMetadata       map[string]interface{}
	UserInfo               info.User
	DeviceRegistrationData []byte
	DeviceIsDisabled       bool
	UserIsDisabled         bool
}

// NewAuthCachedInfo creates a new AuthCachedInfo. It sets the provided token and rawIDToken and the provider-specific
// extra fields which should be stored persistently.
func NewAuthCachedInfo(token *oauth2.Token, rawIDToken string, provider providers.Provider) *AuthCachedInfo {
	return &AuthCachedInfo{
		Token:       token,
		RawIDToken:  rawIDToken,
		ExtraFields: provider.GetExtraFields(token),
	}
}

// CacheAuthInfo saves the token to the given path.
func CacheAuthInfo(path string, token *AuthCachedInfo) (err error) {
	jsonData, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("could not marshal token: %v", err)
	}

	// Create issuer specific cache directory if it doesn't exist.
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create token directory: %v", err)
	}

	if err = os.WriteFile(path, jsonData, 0600); err != nil {
		return fmt.Errorf("could not save token: %v", err)
	}

	return nil
}

// LoadAuthInfo reads the token from the given path.
func LoadAuthInfo(path string) (*AuthCachedInfo, error) {
	jsonData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read token: %v", err)
	}

	var cachedInfo AuthCachedInfo
	if err := json.Unmarshal(jsonData, &cachedInfo); err != nil {
		return nil, fmt.Errorf("could not unmarshal token: %v", err)
	}
	// Set the extra fields of the token.
	if cachedInfo.ExtraFields != nil {
		cachedInfo.Token = cachedInfo.Token.WithExtra(cachedInfo.ExtraFields)
	}

	return &cachedInfo, nil
}
