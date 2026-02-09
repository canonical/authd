//go:build withmsentraid

// Package msentraid is the Microsoft Entra ID specific extension.
package msentraid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/k0kubun/pp"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/ubuntu/authd/log"
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
	expectedScopes              []string
	needsAccessTokenForGraphAPI bool

	// Used as the token scopes of the access token for the Microsoft Graph API in tests.
	tokenScopesForGraphAPI []string
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
		"scope": token.Extra("scope"),
		"scp":   token.Extra("scp"),
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

// GetUserInfo returns the user info from the ID token.
func (p *Provider) GetUserInfo(idToken info.Claimer) (info.User, error) {
	var err error

	userClaims, err := p.userClaims(idToken)
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
func (p *Provider) GetGroups(
	ctx context.Context,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationDataJSON []byte,
) ([]info.Group, error) {
	accessTokenStr := token.AccessToken
	if p.needsAccessTokenForGraphAPI {
		var data himmelblau.DeviceRegistrationData
		err := json.Unmarshal(deviceRegistrationDataJSON, &data)
		if err != nil {
			log.Noticef(ctx, "Device registration JSON data: %s", deviceRegistrationDataJSON)
			return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}

		tenantID := tenantID(issuerURL)
		accessTokenStr, err = himmelblau.AcquireAccessTokenForGraphAPI(ctx, clientID, tenantID, token, data)
		if errors.Is(err, himmelblau.ErrDeviceDisabled) {
			return nil, err
		}
		if errors.Is(err, himmelblau.ErrInvalidRedirectURI) {
			msg := "Token acquisition failed: The app is misconfigured in Microsoft Entra (the redirect URI is missing or invalid). Please contact your administrator."
			return nil, &providerErrors.ForDisplayError{Message: msg, Err: err}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to acquire access token for Microsoft Graph API: %w", err)
		}
	}
	// Parse the access token without signature verification, because we're not the audience of the token (that's
	// the Microsoft Graph API) and we don't use it for authentication, but only to access the Microsoft Graph API.
	accessToken, _, err := new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse access token: %w", err)
	}

	msgraphHost := fmt.Sprintf("https://%s/%s", defaultMSGraphHost, msgraphAPIVersion)
	if providerMetadata["msgraph_host"] != nil {
		var ok bool
		msgraphHost, ok = providerMetadata["msgraph_host"].(string)
		if !ok {
			return nil, fmt.Errorf("failed to cast msgraph_host to string: %v", providerMetadata["msgraph_host"])
		}

		// Handle the case that the provider metadata only contains the host without the protocol and API version,
		// as was the case before 5fc98520c45294ffb85bb27a81929e2ec1b89fcb. This fixes #858.
		if !strings.Contains(msgraphHost, "://") {
			msgraphHost = fmt.Sprintf("https://%s/%s", msgraphHost, msgraphAPIVersion)
		}
	}

	return p.fetchUserGroups(accessToken, msgraphHost)
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

	cred := azureTokenCredential{token: token}
	auth, err := msgraphauth.NewAzureIdentityAuthenticationProvider(cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create AzureIdentityAuthenticationProvider: %v", err)
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphRequestAdapter: %v", err)
	}
	adapter.SetBaseUrl(msgraphHost)

	client := msgraphsdk.NewGraphServiceClient(adapter)

	// Get the groups (only the groups, not directory roles or administrative units, because that would require
	// additional permissions) which the user is a member of.
	graphGroups, err := getSecurityGroups(client)
	if err != nil {
		return nil, err
	}

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

func getSecurityGroups(client *msgraphsdk.GraphServiceClient) ([]msgraphmodels.Groupable, error) {
	// Initial request to get groups
	requestBuilder := client.Me().TransitiveMemberOf().GraphGroup()
	result, err := requestBuilder.Get(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get user groups: %v", err)
	}
	if result == nil {
		log.Debug(context.Background(), "Got nil response from Microsoft Graph API for user's groups, assuming that user is not a member of any group.")
		return []msgraphmodels.Groupable{}, nil
	}

	groups := result.GetValue()

	// Continue fetching groups using paging if a next link is available
	for result.GetOdataNextLink() != nil {
		nextLink := *result.GetOdataNextLink()

		result, err = requestBuilder.WithUrl(nextLink).Get(context.Background(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of user groups: %v", err)
		}

		groups = append(groups, result.GetValue()...)
	}

	// Remove the groups which are not security groups (but for example Microsoft 365 groups, which can be created
	// by non-admin users).
	groups = removeNonSecurityGroups(groups)

	var groupNames []string
	for _, group := range groups {
		groupNamePtr := group.GetDisplayName()
		if groupNamePtr != nil {
			groupNames = append(groupNames, *groupNamePtr)
		}
	}
	log.Debugf(context.Background(), "Got groups: %s", strings.Join(groupNames, ", "))

	return groups, nil
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
	return []string{authmodes.Device, authmodes.DeviceQr}
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

// SupportsDeviceRegistration checks if the provider supports device registration.
func (p *Provider) SupportsDeviceRegistration() bool {
	// The Microsoft Entra ID provider supports device registration.
	return true
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
	// If this function is called, it means that the token that we have is for device registration,
	// so we can't use it to access the Microsoft Graph API.
	p.needsAccessTokenForGraphAPI = true

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
	return err.ErrorCode == "invalid_grant" && (strings.HasPrefix(err.ErrorDescription, "AADSTS50173:") || strings.HasPrefix(err.ErrorDescription, "AADSTS70043:"))
}

// IsUserDisabledError returns true if the reason for the error is that the user is disabled.
func (p *Provider) IsUserDisabledError(err *oauth2.RetrieveError) bool {
	return err.ErrorCode == "invalid_grant" && strings.HasPrefix(err.ErrorDescription, "AADSTS50057:")
}
