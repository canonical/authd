// Package token provides functions to save and load tokens from disk.
package token

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
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
	// GroupsResolved records that UserInfo.Groups was successfully fetched
	// from the provider at least once. A group-fetch failure may only fall
	// back to cached groups when this is set.
	GroupsResolved bool
	// DeviceRegistrationDataValidationPending is set when fresh registration
	// data was obtained but group lookup has not yet succeeded.
	// The data is retained and reused until group lookup succeeds.
	DeviceRegistrationDataValidationPending bool `json:",omitempty"`
	// DeviceRegistrationDataValidationFailureSince stores the Unix timestamp
	// of the first device-authentication failure while validation is pending.
	DeviceRegistrationDataValidationFailureSince int64 `json:",omitempty"`
	DeviceIsDisabled                             bool
	UserIsDisabled                               bool
	// ObtainedViaEntraAuth is set when the token was obtained through the
	// entra_auth flow. On a returning login it selects the refresh path:
	// these tokens are refreshed as the Microsoft Broker App (public client, no
	// client_secret) for the liveness/revocation check, rather than via the OIDC
	// app refresh used by device-auth tokens.
	ObtainedViaEntraAuth bool
}

// UnmarshalJSON keeps legacy caches usable after GroupsResolved was added.
// Legacy caches with cached groups retain the previous cached-group fallback
// behavior. A provider ID alone is not enough because device registration can
// be cached before group lookup succeeds.
func (a *AuthCachedInfo) UnmarshalJSON(data []byte) error {
	type authCachedInfo AuthCachedInfo

	var decoded authCachedInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	// CacheAuthInfo always marshals the exact Go field name, so key
	// presence is a plain lookup.
	_, groupsResolvedPresent := fields["GroupsResolved"]
	if !groupsResolvedPresent && decoded.Token != nil && decoded.UserInfo.Name != "" &&
		decoded.UserInfo.Groups != nil {
		decoded.GroupsResolved = true
	}

	*a = AuthCachedInfo(decoded)
	return nil
}

// NewAuthCachedInfo creates a new AuthCachedInfo. It sets the provided token and rawIDToken and the provider-specific
// extra fields which should be stored persistently.
func NewAuthCachedInfo(token *oauth2.Token, rawIDToken string, extraFields map[string]interface{}) *AuthCachedInfo {
	return &AuthCachedInfo{
		Token:       token,
		RawIDToken:  rawIDToken,
		ExtraFields: extraFields,
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
