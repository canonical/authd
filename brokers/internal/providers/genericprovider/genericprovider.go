// Package genericprovider is the generic oidc extension.
package genericprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/canonical/authd/brokers/internal/broker/authmodes"
	providerErrors "github.com/canonical/authd/brokers/internal/providers/errors"
	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/log"
	"golang.org/x/oauth2"
)

const (
	// DefaultGroupsClaim is the name of the ID token claim that holds the
	// groups when no other claim is configured.
	DefaultGroupsClaim = "groups"

	// localGroupPrefix marks a group from the identity provider as a local
	// group. It is the same prefix that the msentraid provider uses.
	localGroupPrefix = "linux-"
)

// GenericProvider is a generic OIDC provider.
//
// SetGroupsClaimConfig and GetGroups have pointer receivers. So only a
// *GenericProvider reads groups from the ID token claim, and providers that
// embed a GenericProvider value (like the Google provider) do not.
type GenericProvider struct {
	// groupsClaim is the name of the ID token claim that holds the groups.
	groupsClaim string
	// allowedGroups are the local groups that the groups claim may add the
	// user to. If it is empty, the provider does not fetch any groups.
	allowedGroups map[string]struct{}
}

// New returns a new GenericProvider.
func New() GenericProvider {
	return GenericProvider{}
}

// DisplayName returns the display name of the provider.
func (p GenericProvider) DisplayName() string {
	return "the identity provider"
}

// AdditionalScopes returns the generic scopes required by the provider.
func (p GenericProvider) AdditionalScopes() []string {
	return []string{}
}

// AuthOptions is a no-op when no specific provider is in use.
func (p GenericProvider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetUserInfo returns user information from the claims of the provided Claimer.
func (p GenericProvider) GetUserInfo(claimer info.Claimer, _ bool) (info.User, error) {
	var claimsMap map[string]interface{}
	if err := claimer.Claims(&claimsMap); err != nil {
		return info.User{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}

	// Check required claims
	providerID, ok := claimsMap["sub"].(string)
	if !ok || providerID == "" {
		return info.User{}, providerErrors.NewMissingClaimError("sub")
	}

	email, ok := claimsMap["email"].(string)
	if !ok || email == "" {
		return info.User{}, providerErrors.NewMissingClaimError("email")
	}

	rawEmailVerified, present := claimsMap["email_verified"]
	if !present {
		return info.User{}, &providerErrors.ForDisplayError{
			Message: "Authentication failure: email not verified",
			Err:     providerErrors.NewMissingClaimError("email_verified"),
		}
	}
	if verified, ok := rawEmailVerified.(bool); !ok || !verified {
		return info.User{}, &providerErrors.ForDisplayError{
			Message: "Authentication failure: email not verified",
			Err:     errors.New("email_verified claim value is false or malformed"),
		}
	}

	// Optional claims: home, shell, name
	home, _ := claimsMap["home"].(string)
	shell, _ := claimsMap["shell"].(string)
	gecos, _ := claimsMap["name"].(string)

	return info.NewUser(
		email,
		home,
		providerID,
		shell,
		gecos,
		nil,
	), nil
}

// NormalizeUsername parses a username into a normalized version.
func (p GenericProvider) NormalizeUsername(username string) string {
	return username
}

// VerifyUsername checks if the requested username matches the authenticated user.
func (p GenericProvider) VerifyUsername(requestedUsername, username string) error {
	if p.NormalizeUsername(requestedUsername) != p.NormalizeUsername(username) {
		msg := fmt.Sprintf("Authentication failure: requested username %q does not match the authenticated user %q", requestedUsername, username)
		return &providerErrors.ForDisplayError{Message: msg}
	}
	return nil
}

// SupportedOnlineAuthModes returns the authentication modes supported by the
// provider that require a connection to the identity provider.
func (p GenericProvider) SupportedOnlineAuthModes() []string {
	return []string{authmodes.Device, authmodes.DeviceQr}
}

// IsTokenExpiredError returns true if the reason for the error is that the refresh token is expired.
func (p GenericProvider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	if err.ErrorCode != "invalid_grant" {
		return false
	}

	expiredDescriptions := []string{
		"Session not active",         // Keycloak: online user session expired
		"Offline session not active", // Keycloak: offline session expired or revoked
		"Token is not active",        // Keycloak: refresh token JWT expired (exp/nbf check)
		"Stale token",                // Keycloak: token issued before not-before policy or reuse detected
	}

	return slices.ContainsFunc(expiredDescriptions, func(desc string) bool {
		return strings.Contains(err.ErrorDescription, desc)
	})
}

// SetGroupsClaimConfig sets the claim that holds the groups and the local
// groups that the claim may add the user to. An empty claim means
// DefaultGroupsClaim. Group names are compared in lower case, like the
// msentraid provider does.
func (p *GenericProvider) SetGroupsClaimConfig(claim string, allowedGroups []string) {
	p.groupsClaim = strings.TrimSpace(claim)
	if p.groupsClaim == "" {
		p.groupsClaim = DefaultGroupsClaim
	}

	p.allowedGroups = make(map[string]struct{}, len(allowedGroups))
	for _, g := range allowedGroups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if lower := strings.ToLower(g); lower != g {
			log.Warningf(context.Background(), "Allowed local group %q is used in lower case: %q", g, lower)
			g = lower
		}
		if strings.HasPrefix(g, localGroupPrefix) {
			log.Warningf(context.Background(), "Allowed local group %q starts with %q: it only matches the group %q in the claim. Use the name of the local group, without the prefix.",
				g, localGroupPrefix, localGroupPrefix+g)
		}
		p.allowedGroups[g] = struct{}{}
	}
}

// GetGroups returns the local groups of the user from the groups claim of the
// ID token.
//
// Only groups with the "linux-" prefix are used, without the prefix, and only
// if they are in the allowed groups. The comparison ignores case. For example,
// "linux-devops" adds the user to the local group "devops" if "devops" is
// allowed. All other groups are ignored. If no allowed groups are configured,
// it returns no groups, so the broker behaves as before.
//
// If the claim is missing or malformed, it returns no groups without an
// error, so the user can still log in, but without the groups from the claim.
//
// The ID token is not verified again here: the broker gets it directly from
// the token endpoint and verifies it before it fetches the groups. If a token
// refresh response has no ID token, there is nothing to read, so it returns an
// error, and the broker keeps the cached groups if the login goes on.
func (p *GenericProvider) GetGroups(
	_ context.Context,
	_ string,
	_ string,
	token *oauth2.Token,
	_ map[string]interface{},
	_ []byte,
	_ bool,
) ([]info.Group, error) {
	if len(p.allowedGroups) == 0 {
		return nil, nil
	}

	if token == nil {
		return nil, groupsError(errors.New("no token"))
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return nil, groupsError(errors.New("the token response does not contain an ID token"))
	}

	claims, err := idTokenClaims(rawIDToken)
	if err != nil {
		return nil, groupsError(err)
	}

	names, err := groupNames(claims, p.groupsClaim)
	if err != nil {
		// The user keeps logging in, but without groups from the claim.
		log.Warningf(context.Background(), "Ignoring the groups claim: %v", err)
		return []info.Group{}, nil
	}

	groups := []info.Group{}
	seen := make(map[string]struct{})
	for _, name := range names {
		name = strings.ToLower(name)
		localName, ok := strings.CutPrefix(name, localGroupPrefix)
		if !ok || localName == "" {
			continue
		}
		if _, ok := p.allowedGroups[localName]; !ok {
			log.Infof(context.Background(), "Ignoring group %q: %q is not an allowed local group", name, localName)
			continue
		}
		if _, ok := seen[localName]; ok {
			continue
		}
		seen[localName] = struct{}{}

		// An empty UGID makes authd treat the group as a local group, which
		// must already exist in /etc/group.
		groups = append(groups, info.Group{Name: localName})
	}

	return groups, nil
}

// groupsError logs err and wraps it in an error that is shown to the user. The
// error message that is shown does not include err, so it is logged here.
func groupsError(err error) error {
	log.Warningf(context.Background(), "Could not read the groups claim: %v", err)
	return &providerErrors.ForDisplayError{
		Message: "Could not read the groups from the identity provider",
		Err:     err,
	}
}

// compactJWS matches the JWS compact serialization: three base64url parts
// without padding, separated by dots. A JWT, like an ID token, always uses it,
// without line breaks or whitespace (RFC 7519, sections 1 and 7.2).
var compactJWS = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// idTokenClaims returns the claims of the payload of a raw ID token. It does
// not verify the token.
//
// The verifier of the broker also accepts the JWS JSON serialization, where
// other members can hold text with dots. So only the compact serialization is
// read here: then the payload is the one that the broker verified.
func idTokenClaims(rawIDToken string) (map[string]interface{}, error) {
	if !compactJWS.MatchString(rawIDToken) {
		return nil, errors.New("malformed ID token: not a JWS in compact serialization")
	}
	parts := strings.Split(rawIDToken, ".")

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed ID token payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("malformed ID token claims: %w", err)
	}

	return claims, nil
}

// groupNames returns the group names in the given claim. The claim can be a
// list of strings or a single string.
func groupNames(claims map[string]interface{}, claim string) ([]string, error) {
	value, ok := claims[claim]
	if !ok {
		return nil, fmt.Errorf("the ID token has no %q claim", claim)
	}

	switch v := value.(type) {
	case string:
		return []string{v}, nil
	case []interface{}:
		names := make([]string, 0, len(v))
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("the %q claim has a value of type %T, not a string", claim, item)
			}
			names = append(names, name)
		}
		return names, nil
	default:
		return nil, fmt.Errorf("the %q claim is of type %T, not a list of strings", claim, value)
	}
}
