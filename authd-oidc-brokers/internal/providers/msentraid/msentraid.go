//go:build withmsentraid

// Package msentraid is the Microsoft Entra ID specific extension.
package msentraid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/log"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/k0kubun/pp"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"golang.org/x/oauth2"
)

func init() {
	pp.ColoringEnabled = false
}

const (
	localGroupPrefix   = "linux-"
	defaultMSGraphHost = "graph.microsoft.com"
	msgraphAPIVersion  = "v1.0"
)

// Provider is the Microsoft Entra ID provider implementation.
type Provider struct {
	expectedScopes []string

	// graphClientSecret, when non-empty, enables the app-only (client credentials)
	// path for group lookups. The secret belongs to the same client_id configured
	// in [oidc]; the app must have the GroupMember.Read.All *Application* permission
	// admin-consented in Entra. Populated by SetGraphClientSecret after parsing config.
	graphClientSecret string

	// Used as the token scopes of the access token for the Microsoft Graph API in tests.
	tokenScopesForGraphAPI []string
}

// SetGraphClientSecret stores the OIDC app's client secret so that GetGroups can
// fall back to the app-only (client credentials) Graph API path when the
// user's delegated token lacks the GroupMember.Read.All scope.
func (p *Provider) SetGraphClientSecret(secret string) {
	p.graphClientSecret = secret
}

// New returns a new MSEntraID provider.
func New() *Provider {
	return &Provider{
		expectedScopes: append(consts.DefaultScopes, "GroupMember.Read.All", "User.Read"),
	}
}

// AdditionalScopes returns the generic scopes required by the EntraID provider.
func (p *Provider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess, "GroupMember.Read.All", "User.Read"}
}

// DisplayName returns the display name of the provider.
func (p *Provider) DisplayName() string {
	return "Microsoft Entra ID"
}

// AuthOptions returns the generic auth options required by the EntraID provider.
func (p *Provider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

func (p *Provider) getTokenScopes(token *jwt.Token) ([]string, error) {
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("failed to cast token claims to MapClaims: %v", token.Claims)
	}
	scopesStr, ok := claims["scp"].(string)
	if !ok {
		return nil, fmt.Errorf("failed to cast scp claim to string: %v", claims["scp"])
	}
	return strings.Split(scopesStr, " "), nil
}

func (p *Provider) getAppID(token *jwt.Token) (string, error) {
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("failed to cast token claims to MapClaims: %v", token.Claims)
	}
	appID, ok := claims["appid"].(string)
	if !ok {
		return "", fmt.Errorf("failed to cast appid claim to string: %v", claims["appid"])
	}
	return appID, nil
}

// GetExtraFields returns the extra fields of the token which should be stored persistently.
func (p *Provider) GetExtraFields(token *oauth2.Token) map[string]interface{} {
	return map[string]interface{}{
		"scope":              token.Extra("scope"),
		"scp":                token.Extra("scp"),
		"preferred_username": token.Extra("preferred_username"),
		"sub":                token.Extra("sub"),
		"name":               token.Extra("name"),
	}
}

// GetMetadata returns relevant metadata about the provider.
func (p *Provider) GetMetadata(provider *oidc.Provider) (map[string]interface{}, error) {
	var claims struct {
		MSGraphHost string `json:"msgraph_host"`
	}

	if err := provider.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to get provider claims: %v", err)
	}

	return map[string]interface{}{
		"msgraph_host": fmt.Sprintf("https://%s/%s", claims.MSGraphHost, msgraphAPIVersion),
	}, nil
}

// GetUserInfo returns the user info from the provided Claimer.
func (p *Provider) GetUserInfo(claimer info.Claimer, _ bool) (info.User, error) {
	var err error

	userClaims, err := p.userClaims(claimer)
	if err != nil {
		return info.User{}, err
	}

	return info.NewUser(
		userClaims.PreferredUserName,
		userClaims.Home,
		userClaims.Sub,
		userClaims.Shell,
		userClaims.Gecos,
		nil,
	), nil
}

// GetGroups retrieves the groups the user is a member of via the Microsoft Graph API.
//
// There are three ways the groups can be resolved, tried in this order:
//
//  1. Client credentials (app-only): used when a [oidc] client_secret is
//     configured and the current token does not already carry the
//     GroupMember.Read.All scope. This is the path that makes the
//     entra_password + MFA flow work *without* device registration: the
//     delegated token issued by the Microsoft Broker App during native MFA
//     cannot be exchanged for a Graph-scoped delegated token (the FOCI scope
//     wall), so we fall back to an application-level token. It requires the app
//     registration to hold the GroupMember.Read.All *Application* permission
//     with tenant admin consent. Trade-off: an app-only token reflects the
//     directory's group membership, not the user's delegated session, so a
//     per-user session revocation is not observed at this step (the MFA
//     challenge itself is the live per-user check).
//  2. Device-registration token exchange: used when needsAccessTokenForGraphAPI
//     is set (the cached token was obtained via device registration). The PRT is
//     exchanged for a Graph-scoped access token.
//  3. The current delegated token directly, when it already carries
//     GroupMember.Read.All.
//
// Strategy 1 is what lets register_device=false deployments resolve groups; if
// the project later decides to require register_device=true for the MFA flow,
// the client_secret path (and SetGraphClientSecret/GraphClientSecretSetter) can
// be dropped in favour of strategy 2 alone.
func (p *Provider) GetGroups(
	ctx context.Context,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationDataJSON []byte,
	needsAccessTokenForGraphAPI bool,
) ([]info.Group, error) {
	accessTokenStr := token.AccessToken
	accessTokenHasGraphScope := false
	// Parse early to check whether the token already carries the required graph scope.
	accessToken, _, parseErr := new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	if parseErr == nil {
		if scopes, scopeErr := p.getTokenScopes(accessToken); scopeErr == nil {
			accessTokenHasGraphScope = slices.Contains(scopes, "GroupMember.Read.All")
		}
	}

	// If a client secret is configured and the token lacks the Graph scope, use
	// the app-only (client credentials) path instead of the delegated-token path.
	// This bypasses the FOCI scope wall that prevents third-party apps from
	// exchanging MFA tokens for Graph-scoped delegated tokens.
	//
	// Exclude tokens that need device-registration token exchange
	// (needsAccessTokenForGraphAPI): those have a PRT that can be exchanged for a
	// Graph-scoped token (strategy 2), which preserves the user's delegated
	// session semantics. This keeps register_device=true logins — both
	// device-code and entra_password — on the PRT path even when a client_secret
	// is configured, so only entra_password-without-device-registration tokens
	// take the app-only path.
	if p.graphClientSecret != "" && !accessTokenHasGraphScope && !needsAccessTokenForGraphAPI {
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse access token for client credentials group lookup: %w", parseErr)
		}
		oid, oidErr := p.getOIDFromToken(accessToken)
		if oidErr != nil {
			log.Noticef(ctx, "Could not extract OID from access token for client credentials path, falling back: %v", oidErr)
		} else {
			host := resolveMSGraphHost(providerMetadata)
			// Make the path switch observable: this resolves groups from the
			// directory's view of the user via an app-only token, NOT the user's
			// delegated session, so per-user session/account-status revocation is
			// not reflected here (see the GetGroups doc comment).
			log.Infof(ctx, "Resolving groups for OID %s via app-only client credentials (delegated token lacks GroupMember.Read.All)", oid)
			return p.fetchUserGroupsByClientCredentials(ctx, clientID, issuerURL, oid, host)
		}
	}

	if needsAccessTokenForGraphAPI && !accessTokenHasGraphScope {
		var data himmelblau.DeviceRegistrationData
		err := json.Unmarshal(deviceRegistrationDataJSON, &data)
		if err != nil {
			log.Noticef(ctx, "Device registration JSON data: %s", deviceRegistrationDataJSON)
			return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}

		tenantID := tenantID(issuerURL)
		accessTokenStr, err = himmelblau.AcquireAccessTokenForGraphAPI(ctx, clientID, tenantID, token, data)
		if errors.Is(err, himmelblau.ErrDeviceDisabled) {
			return nil, fmt.Errorf("%w: %w", providerErrors.ErrDeviceDisabled, err)
		}
		if errors.Is(err, himmelblau.ErrInvalidRedirectURI) {
			msg := "Token acquisition failed: The app is misconfigured in Microsoft Entra (the redirect URI is missing or invalid). Please contact your administrator."
			return nil, &providerErrors.ForDisplayError{Message: msg, Err: fmt.Errorf("%w: %w", providerErrors.ErrInvalidRedirectURI, err)}
		}
		var tokenAcquisitionError himmelblau.TokenAcquisitionError
		if errors.As(err, &tokenAcquisitionError) {
			return nil, &providerErrors.RetryWithDeviceAuthError{Err: fmt.Errorf("failed to acquire access token for Microsoft Graph API: %w", err)}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to acquire access token for Microsoft Graph API: %w", err)
		}

		// Re-parse the newly acquired token.
		accessToken, _, parseErr = new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	}
	// Parse the access token without signature verification, because we're not the audience of the token (that's
	// the Microsoft Graph API) and we don't use it for authentication, but only to access the Microsoft Graph API.
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse access token: %w", parseErr)
	}

	msgraphHost := resolveMSGraphHost(providerMetadata)

	return p.fetchUserGroups(accessToken, msgraphHost)
}

// resolveMSGraphHost resolves the Microsoft Graph API host URL from provider metadata,
// falling back to the default public endpoint when metadata is absent or malformed.
func resolveMSGraphHost(providerMetadata map[string]interface{}) string {
	if providerMetadata["msgraph_host"] == nil {
		return fmt.Sprintf("https://%s/%s", defaultMSGraphHost, msgraphAPIVersion)
	}
	msgraphHost, ok := providerMetadata["msgraph_host"].(string)
	if !ok {
		return fmt.Sprintf("https://%s/%s", defaultMSGraphHost, msgraphAPIVersion)
	}
	// Handle the case that the provider metadata only contains the host without the protocol and API version,
	// as was the case before 5fc98520c45294ffb85bb27a81929e2ec1b89fcb. This fixes #858.
	if !strings.Contains(msgraphHost, "://") {
		msgraphHost = fmt.Sprintf("https://%s/%s", msgraphHost, msgraphAPIVersion)
	}
	return msgraphHost
}

type claims struct {
	PreferredUserName string `json:"preferred_username"`
	Sub               string `json:"sub"`
	Home              string `json:"home"`
	Shell             string `json:"shell"`
	Gecos             string `json:"name"`
}

// userClaims returns the user claims parsed from the ID token.
func (p *Provider) userClaims(idToken info.Claimer) (claims, error) {
	var userClaims claims
	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}
	return userClaims, nil
}

// newGraphServiceClient builds a Microsoft Graph client that authenticates with
// the given parsed JWT against the resolved Graph host.
func newGraphServiceClient(token *jwt.Token, msgraphHost string) (*msgraphsdk.GraphServiceClient, error) {
	cred := azureTokenCredential{token: token}
	auth, err := msgraphauth.NewAzureIdentityAuthenticationProvider(cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create AzureIdentityAuthenticationProvider: %w", err)
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphRequestAdapter: %w", err)
	}
	adapter.SetBaseUrl(msgraphHost)

	return msgraphsdk.NewGraphServiceClient(adapter), nil
}

// fetchUserGroupsByClientCredentials acquires an application-level Graph API token
// via client credentials and fetches groups for the given user OID. It is used
// when the delegated token lacks GroupMember.Read.All (e.g., native Entra MFA flow).
func (p *Provider) fetchUserGroupsByClientCredentials(ctx context.Context, clientID, issuerURL, userOID, msgraphHost string) ([]info.Group, error) {
	log.Debugf(ctx, "Getting user groups via client credentials for OID %s", userOID)

	appTokenStr, err := acquireClientCredentialsToken(ctx, issuerURL, clientID, p.graphClientSecret, msgraphHost)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire client credentials token for Graph API: %w", err)
	}

	appToken, _, err := new(jwt.Parser).ParseUnverified(appTokenStr, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse client credentials token: %w", err)
	}

	client, err := newGraphServiceClient(appToken, msgraphHost)
	if err != nil {
		return nil, err
	}

	graphGroups, err := getSecurityGroupsByUserID(client, userOID)
	if err != nil {
		return nil, err
	}

	return processSecurityGroups(graphGroups)
}

// getOIDFromToken extracts the object ID (oid claim) from a parsed JWT token.
func (p *Provider) getOIDFromToken(token *jwt.Token) (string, error) {
	if token == nil {
		return "", errors.New("access token is nil")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("failed to cast token claims to MapClaims")
	}
	oid, ok := claims["oid"].(string)
	if !ok || oid == "" {
		return "", errors.New("oid claim not found or empty in access token")
	}
	return oid, nil
}

// acquireClientCredentialsToken obtains an application-level access token for
// the resolved Microsoft Graph host using the OAuth2 client credentials flow.
func acquireClientCredentialsToken(ctx context.Context, issuerURL, clientID, clientSecret, msgraphHost string) (string, error) {
	tokenURL, err := clientCredentialsTokenURL(issuerURL)
	if err != nil {
		return "", err
	}
	scope, err := graphDefaultScope(msgraphHost)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("scope", scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to build client credentials request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("client credentials token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("client credentials token request returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("client credentials token error %q: %s", result.Error, result.ErrorDescription)
	}
	if result.AccessToken == "" {
		return "", errors.New("empty access_token in client credentials response")
	}
	return result.AccessToken, nil
}

func clientCredentialsTokenURL(issuerURL string) (string, error) {
	issuer, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse issuer URL: %w", err)
	}
	if issuer.Scheme == "" || issuer.Host == "" {
		return "", fmt.Errorf("issuer URL %q must include a scheme and host", issuerURL)
	}

	tid := tenantID(issuerURL)
	if tid == "" {
		return "", fmt.Errorf("tenant ID not found in issuer URL %q", issuerURL)
	}

	baseURL := (&url.URL{Scheme: issuer.Scheme, Host: issuer.Host}).String()
	tokenURL, err := url.JoinPath(baseURL, tid, "oauth2", "v2.0", "token")
	if err != nil {
		return "", fmt.Errorf("failed to construct client credentials token URL: %w", err)
	}

	return tokenURL, nil
}

func graphDefaultScope(msgraphHost string) (string, error) {
	graphURL, err := url.Parse(msgraphHost)
	if err != nil {
		return "", fmt.Errorf("failed to parse Microsoft Graph host: %w", err)
	}
	if graphURL.Scheme == "" || graphURL.Host == "" {
		return "", fmt.Errorf("the Microsoft Graph host %q must include a scheme and host", msgraphHost)
	}

	return (&url.URL{Scheme: graphURL.Scheme, Host: graphURL.Host, Path: ".default"}).String(), nil
}

// fetchUserGroups access the Microsoft Graph API to get the groups the user is a member of.
func (p *Provider) fetchUserGroups(token *jwt.Token, msgraphHost string) ([]info.Group, error) {
	log.Debug(context.Background(), "Getting user groups from Microsoft Graph API")

	var err error
	scopes := p.tokenScopesForGraphAPI

	if scopes == nil {
		scopes, err = p.getTokenScopes(token)
		if err != nil {
			return nil, err
		}
	}

	// Check if the token has the GroupMember.Read.All scope
	if !slices.Contains(scopes, "GroupMember.Read.All") {
		msg := "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"
		return nil, &providerErrors.ForDisplayError{Message: msg}
	}

	client, err := newGraphServiceClient(token, msgraphHost)
	if err != nil {
		return nil, err
	}

	// Get the groups (only the groups, not directory roles or administrative units, because that would require
	// additional permissions) which the user is a member of.
	graphGroups, err := getSecurityGroups(client)
	if err != nil {
		return nil, err
	}

	return processSecurityGroups(graphGroups)
}

// processSecurityGroups converts a slice of Graph API group objects into the
// internal info.Group representation, deduplicating and normalising names.
func processSecurityGroups(graphGroups []msgraphmodels.Groupable) ([]info.Group, error) {
	var groups []info.Group
	var msGroupNames []string
	for _, msGroup := range graphGroups {
		var group info.Group

		idPtr := msGroup.GetId()
		if idPtr == nil {
			log.Warning(context.Background(), pp.Sprintf("Could not get ID for group: %v", msGroup))
			return nil, errors.New("could not get group id")
		}
		id := *idPtr

		msGroupNamePtr := msGroup.GetDisplayName()
		if msGroupNamePtr == nil {
			log.Warning(context.Background(), pp.Sprintf("Could not get display name for group object (ID: %s): %v", id, msGroup))
			return nil, errors.New("could not get group name")
		}
		msGroupName := *msGroupNamePtr

		// Check if there is a name conflict with another group returned by the Graph API. It's not clear in which case
		// the Graph API returns multiple groups with the same name (or the same group twice), but we've seen it happen
		// in https://github.com/canonical/authd/issues/789.
		if checkGroupIsDuplicate(msGroupName, msGroupNames) {
			continue
		}

		// Microsoft groups are case-insensitive, see https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules
		group.Name = strings.ToLower(msGroupName)

		isLocalGroup := strings.HasPrefix(group.Name, localGroupPrefix)
		if isLocalGroup {
			group.Name = strings.TrimPrefix(group.Name, localGroupPrefix)
		}

		// Don't set the UGID for local groups, because that's how the user manager differentiates between local and
		// remote groups.
		if !isLocalGroup {
			group.UGID = id
		}

		groups = append(groups, group)
		msGroupNames = append(msGroupNames, msGroupName)
	}

	return groups, nil
}

func checkGroupIsDuplicate(groupName string, groupNames []string) bool {
	for _, name := range groupNames {
		// We don't want to treat local groups without the prefix as duplicates of non-local groups
		// (e.g. "linux-sudo" and "sudo"), so we compare the names as returned by the Graph API - except that we
		// ignore the case, because we use the group names in lowercase.
		if !strings.EqualFold(name, groupName) {
			// Not a duplicate
			continue
		}

		// To make debugging easier, check if the groups differ in case, and mention that in the log message.
		if name == groupName {
			log.Warningf(context.Background(), "The Microsoft Graph API returned the group %q multiple times, ignoring the duplicate", name)
		} else {
			log.Warningf(context.Background(), "The Microsoft Graph API returned the group %[1]q multiple times, but with different case (%[2]q and %[1]q), ignoring the duplicate", groupName, name)
		}

		return true
	}

	return false
}

func removeNonSecurityGroups(groups []msgraphmodels.Groupable) []msgraphmodels.Groupable {
	var securityGroups []msgraphmodels.Groupable
	for _, group := range groups {
		if !isSecurityGroup(group) {
			var s string
			if groupNamePtr := group.GetDisplayName(); groupNamePtr != nil {
				s = *groupNamePtr
			} else if description := group.GetDescription(); description != nil {
				s = *description
			} else if uniqueName := group.GetUniqueName(); uniqueName != nil {
				s = *uniqueName
			}
			if s == "" {
				log.Debugf(context.Background(), "Removing unnamed non-security group")
			} else {
				log.Debugf(context.Background(), "Removing non-security group %s", s)
			}
			continue
		}
		securityGroups = append(securityGroups, group)
	}
	return securityGroups
}

// collectSecurityGroups walks a paged Microsoft Graph group query to completion
// and returns the security groups. getPage fetches the first page when nextLink
// is empty, and the page at nextLink otherwise; a nil page (no response) is
// treated as "user is not a member of any group". logContext is appended to the
// debug log line (e.g. " for user <id>").
func collectSecurityGroups(logContext string, getPage func(nextLink string) ([]msgraphmodels.Groupable, *string, error)) ([]msgraphmodels.Groupable, error) {
	groups, nextLink, err := getPage("")
	if err != nil {
		return nil, err
	}
	for nextLink != nil {
		var page []msgraphmodels.Groupable
		page, nextLink, err = getPage(*nextLink)
		if err != nil {
			return nil, err
		}
		groups = append(groups, page...)
	}

	// Remove the groups which are not security groups (but for example Microsoft 365 groups, which can be created
	// by non-admin users).
	groups = removeNonSecurityGroups(groups)

	var groupNames []string
	for _, group := range groups {
		if groupNamePtr := group.GetDisplayName(); groupNamePtr != nil {
			groupNames = append(groupNames, *groupNamePtr)
		}
	}
	log.Debugf(context.Background(), "Got groups%s: %s", logContext, strings.Join(groupNames, ", "))

	return groups, nil
}

func getSecurityGroups(client *msgraphsdk.GraphServiceClient) ([]msgraphmodels.Groupable, error) {
	requestBuilder := client.Me().TransitiveMemberOf().GraphGroup()
	return collectSecurityGroups("", func(nextLink string) ([]msgraphmodels.Groupable, *string, error) {
		rb := requestBuilder
		if nextLink != "" {
			rb = requestBuilder.WithUrl(nextLink)
		}
		result, err := rb.Get(context.Background(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get user groups: %v", err)
		}
		if result == nil {
			log.Debug(context.Background(), "Got nil response from Microsoft Graph API for user's groups, assuming that user is not a member of any group.")
			return nil, nil, nil
		}
		return result.GetValue(), result.GetOdataNextLink(), nil
	})
}

// getSecurityGroupsByUserID fetches security groups for a specific user OID using
// the application-permission endpoint /users/{id}/transitiveMemberOf/microsoft.graph.group.
// This requires GroupMember.Read.All Application permission and an app-only token.
func getSecurityGroupsByUserID(client *msgraphsdk.GraphServiceClient, userID string) ([]msgraphmodels.Groupable, error) {
	requestBuilder := client.Users().ByUserId(userID).TransitiveMemberOf().GraphGroup()
	return collectSecurityGroups(fmt.Sprintf(" for user %s", userID), func(nextLink string) ([]msgraphmodels.Groupable, *string, error) {
		rb := requestBuilder
		if nextLink != "" {
			rb = requestBuilder.WithUrl(nextLink)
		}
		result, err := rb.Get(context.Background(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get user groups by user ID: %v", err)
		}
		if result == nil {
			log.Debug(context.Background(), "Got nil response from Microsoft Graph API for user's groups, assuming that user is not a member of any group.")
			return nil, nil, nil
		}
		return result.GetValue(), result.GetOdataNextLink(), nil
	})
}

func isSecurityGroup(group msgraphmodels.Groupable) bool {
	// A group is a security group if the `securityEnabled` property is true and the `groupTypes` property does not
	// contain "Unified".
	securityEnabledPtr := group.GetSecurityEnabled()
	if securityEnabledPtr == nil || !*securityEnabledPtr {
		return false
	}

	return !slices.Contains(group.GetGroupTypes(), "Unified")
}

// NormalizeUsername parses a username into a normalized version.
func (p *Provider) NormalizeUsername(username string) string {
	// Microsoft Entra usernames are case-insensitive. We can safely use strings.ToLower here without worrying about
	// different Unicode characters that fold to the same lowercase letter, because the Microsoft Entra username policy
	// (which we check in VerifyUsername) ensures that the username only contains ASCII characters.
	return strings.ToLower(username)
}

// SupportedOIDCAuthModes returns the OIDC authentication modes supported by the provider.
func (p *Provider) SupportedOIDCAuthModes() []string {
	return []string{authmodes.EntraPassword, authmodes.EntraPasswordless, authmodes.Device, authmodes.DeviceQr}
}

// unmarshalOptionalDeviceRegistrationData decodes JSON device-registration data
// when present. Returns nil (and no error) when raw is empty.
func unmarshalOptionalDeviceRegistrationData(raw []byte) (*himmelblau.DeviceRegistrationData, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	data := &himmelblau.DeviceRegistrationData{}
	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
	}
	return data, nil
}

// InitiateEntraPasswordAuth starts the Entra password + MFA flow.
func (p *Provider) InitiateEntraPasswordAuth(
	ctx context.Context,
	clientID string,
	issuerURL string,
	username, password string,
	deviceRegistrationData []byte,
	withDeviceScope bool,
) (*himmelblau.MFAFlowState, *himmelblau.MFAChallengeInfo, error) {
	tid := tenantID(issuerURL)

	data, err := unmarshalOptionalDeviceRegistrationData(deviceRegistrationData)
	if err != nil {
		return nil, nil, err
	}

	return himmelblau.InitiateMFAFlowWithPassword(ctx, clientID, tid, data, username, password, withDeviceScope)
}

// AcquireTokenByMFAFlow completes the MFA challenge.
func (p *Provider) AcquireTokenByMFAFlow(
	ctx context.Context,
	clientID string,
	issuerURL string,
	username string,
	flow *himmelblau.MFAFlowState,
	authData string,
	pollAttempt int,
	deviceRegistrationData []byte,
) (*oauth2.Token, error) {
	tid := tenantID(issuerURL)

	data, err := unmarshalOptionalDeviceRegistrationData(deviceRegistrationData)
	if err != nil {
		return nil, err
	}

	return himmelblau.AcquireTokenByMFAFlow(ctx, clientID, tid, data, username, flow, authData, pollAttempt)
}

// RefreshEntraPasswordToken refreshes the cached Entra password + MFA refresh token
// as the Microsoft Broker app (a public client, no client_secret) for basic scopes
// only, to re-verify the account on a returning login. The Broker app is the client
// that issued the family refresh token during the MFA flow; the configured OIDC app
// cannot redeem it. Basic scopes (never Microsoft Graph) avoid the Broker-app↔Graph
// preauthorization wall (AADSTS65002), so this works for any register_device setting.
// A failure is returned as the underlying *oauth2.RetrieveError so the broker can
// classify it exactly like the device-auth refresh.
func (p *Provider) RefreshEntraPasswordToken(ctx context.Context, issuerURL, refreshToken string) (*oauth2.Token, error) {
	tokenURL, err := clientCredentialsTokenURL(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("could not build token URL for Entra password refresh: %w", err)
	}

	cfg := oauth2.Config{
		ClientID: consts.MicrosoftBrokerAppID,
		Scopes:   []string{"openid", "profile", "offline_access"},
		Endpoint: oauth2.Endpoint{TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}

	return cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
}

// VerifyUsername checks if the authenticated username matches the requested username and that both are valid.
func (p *Provider) VerifyUsername(requestedUsername, authenticatedUsername string) error {
	if p.NormalizeUsername(requestedUsername) != p.NormalizeUsername(authenticatedUsername) {
		msg := fmt.Sprintf("Authentication failure: requested username %q does not match the authenticated username %q", requestedUsername, authenticatedUsername)
		return &providerErrors.ForDisplayError{Message: msg}
	}

	// Check that the usernames only contain the characters allowed by the Microsoft Entra username policy
	// https://learn.microsoft.com/en-us/entra/identity/authentication/concept-sspr-policy#username-policies
	usernameRegexp := regexp.MustCompile(`^[a-zA-Z0-9'.\-_!#^~@]+$`)
	if !usernameRegexp.MatchString(authenticatedUsername) {
		// If this error occurs, we should investigate and probably relax the username policy, so we ask the user
		// explicitly to report this error.
		msg := fmt.Sprintf("Authentication failure: the authenticated username %q contains invalid characters. Please report this error on https://github.com/canonical/authd/issues", authenticatedUsername)
		return &providerErrors.ForDisplayError{Message: msg}
	}
	if !usernameRegexp.MatchString(requestedUsername) {
		msg := fmt.Sprintf("Authentication failure: requested username %q contains invalid characters", requestedUsername)
		return &providerErrors.ForDisplayError{Message: msg}
	}

	return nil
}

// IsTokenForDeviceRegistration checks if the token is for device registration.
func (p *Provider) IsTokenForDeviceRegistration(token *oauth2.Token) (bool, error) {
	accessToken, _, err := new(jwt.Parser).ParseUnverified(token.AccessToken, jwt.MapClaims{})
	if err != nil {
		return false, fmt.Errorf("failed to parse access token: %v", err)
	}

	appID, err := p.getAppID(accessToken)
	if err != nil {
		return false, fmt.Errorf("failed to get app ID from access token: %v", err)
	}

	return appID == consts.MicrosoftBrokerAppID, nil
}

// MaybeRegisterDevice checks if the device is already registered and registers it if not.
func (p *Provider) MaybeRegisterDevice(
	ctx context.Context,
	token *oauth2.Token,
	username string,
	issuerURL string,
	jsonData []byte,
) (registrationData []byte, cleanup func(), err error) {
	nop := func() {}

	// Check if the device is already registered
	if len(jsonData) > 0 {
		var data himmelblau.DeviceRegistrationData
		if err := json.Unmarshal(jsonData, &data); err != nil {
			log.Noticef(ctx, "Device registration JSON data: %s", string(jsonData))
			return nil, nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}
		if data.IsValid() {
			return jsonData, nop, nil
		}
	}

	nameParts := strings.Split(username, "@")
	if len(nameParts) != 2 {
		return nil, nop, fmt.Errorf("invalid username format: %s, expected format is 'username@domain'", username)
	}
	domain := nameParts[1]

	data, cleanup, err := himmelblau.RegisterDevice(ctx, token, tenantID(issuerURL), domain)
	if err != nil {
		return nil, nop, err
	}

	// Ensure that the cleanup function is called if we return an error.
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	jsonData, err = json.Marshal(data)
	if err != nil {
		return nil, nop, fmt.Errorf("failed to marshal device registration data: %v", err)
	}

	return jsonData, cleanup, nil
}

// tenantID extracts the tenant ID from a Microsoft Entra ID issuer URL.
// For example, given: https://login.microsoftonline.com/8de88d99-6d0f-44d7-a8a5-925b012e5940/v2.0
// it returns: 8de88d99-6d0f-44d7-a8a5-925b012e5940.
func tenantID(issuerURL string) string {
	if issuer, err := url.Parse(issuerURL); err == nil {
		issuerPath := strings.Trim(issuer.Path, "/")
		if issuerPath != "" {
			return strings.Split(issuerPath, "/")[0]
		}
	}

	return strings.Split(strings.TrimPrefix(issuerURL, "https://login.microsoftonline.com/"), "/")[0]
}

type azureTokenCredential struct {
	token *jwt.Token
}

// GetToken creates an azcore.AccessToken from an oauth2.Token.
func (c azureTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	claims, ok := c.token.Claims.(jwt.MapClaims)
	if !ok {
		return azcore.AccessToken{}, fmt.Errorf("failed to cast token claims to MapClaims: %v", c.token.Claims)
	}
	expiresOn, ok := claims["exp"].(float64)
	if !ok {
		return azcore.AccessToken{}, fmt.Errorf("failed to cast token expiration to float64: %v", claims["exp"])
	}

	return azcore.AccessToken{
		Token:     c.token.Raw,
		ExpiresOn: time.Unix(int64(expiresOn), 0),
	}, nil
}

// IsTokenExpiredError returns true if the reason for the error is that the refresh token is expired.
func (p *Provider) IsTokenExpiredError(err *oauth2.RetrieveError) bool {
	if err.ErrorCode != "invalid_grant" {
		return false
	}

	expiredPrefixes := []string{
		"AADSTS50173:",  // grant revoked (password change/reset)
		"AADSTS70043:",  // refresh token expired due to sign-in frequency (Conditional Access)
		"AADSTS700082:", // refresh token expired due to inactivity
	}

	return slices.ContainsFunc(expiredPrefixes, func(prefix string) bool {
		return strings.HasPrefix(err.ErrorDescription, prefix)
	})
}

// IsUserDisabledError returns true if the reason for the error is that the user is disabled.
func (p *Provider) IsUserDisabledError(err *oauth2.RetrieveError) bool {
	return err.ErrorCode == "invalid_grant" && strings.HasPrefix(err.ErrorDescription, "AADSTS50057:")
}
