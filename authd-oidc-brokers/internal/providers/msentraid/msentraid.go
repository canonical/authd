// Package msentraid is the Microsoft Entra ID specific extension.
package msentraid

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/k0kubun/pp"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker/authmodes"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	providerErrors "github.com/ubuntu/authd-oidc-brokers/internal/providers/errors"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
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
	expectedScopes []string
}

// New returns a new MSEntraID provider.
func New() Provider {
	return Provider{
		expectedScopes: append(consts.DefaultScopes, "GroupMember.Read.All", "User.Read"),
	}
}

// AdditionalScopes returns the generic scopes required by the EntraID provider.
func (p Provider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess, "GroupMember.Read.All", "User.Read"}
}

// AuthOptions returns the generic auth options required by the EntraID provider.
func (p Provider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// CheckTokenScopes checks if the token has the required scopes.
func (p Provider) CheckTokenScopes(token *oauth2.Token) error {
	scopes, err := p.getTokenScopes(token)
	if err != nil {
		return err
	}

	var missingScopes []string
	for _, s := range p.expectedScopes {
		if !slices.Contains(scopes, s) {
			missingScopes = append(missingScopes, s)
		}
	}
	if len(missingScopes) > 0 {
		return fmt.Errorf("missing required scopes: %s", strings.Join(missingScopes, ", "))
	}
	return nil
}

func (p Provider) getTokenScopes(token *oauth2.Token) ([]string, error) {
	scopesStr, ok := token.Extra("scope").(string)
	if !ok {
		return nil, fmt.Errorf("failed to cast token scopes to string: %v", token.Extra("scope"))
	}
	return strings.Split(scopesStr, " "), nil
}

// GetExtraFields returns the extra fields of the token which should be stored persistently.
func (p Provider) GetExtraFields(token *oauth2.Token) map[string]interface{} {
	return map[string]interface{}{
		"scope": token.Extra("scope"),
	}
}

// GetMetadata returns relevant metadata about the provider.
func (p Provider) GetMetadata(provider *oidc.Provider) (map[string]interface{}, error) {
	var claims struct {
		MSGraphHost string `json:"msgraph_host"`
	}

	if err := provider.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to get provider claims: %v", err)
	}

	return map[string]interface{}{
		"msgraph_host": claims.MSGraphHost,
	}, nil
}

// GetUserInfo returns the user info from the ID token and the groups the user is a member of, which are retrieved via
// the Microsoft Graph API.
func (p Provider) GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken, providerMetadata map[string]interface{}) (info.User, error) {
	msgraphHost := providerMetadata["msgraph_host"]
	if msgraphHost == nil {
		msgraphHost = defaultMSGraphHost
	}

	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	userGroups, err := p.getGroups(accessToken, msgraphHost.(string))
	if err != nil {
		return info.User{}, err
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
func (p Provider) userClaims(idToken *oidc.IDToken) (claims, error) {
	var userClaims claims
	if err := idToken.Claims(&userClaims); err != nil {
		return claims{}, fmt.Errorf("failed to get ID token claims: %v", err)
	}
	return userClaims, nil
}

// getGroups access the Microsoft Graph API to get the groups the user is a member of.
func (p Provider) getGroups(token *oauth2.Token, msgraphHost string) ([]info.Group, error) {
	log.Debug(context.Background(), "Getting user groups from Microsoft Graph API")

	// Check if the token has the GroupMember.Read.All scope
	scopes, err := p.getTokenScopes(token)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(scopes, "GroupMember.Read.All") {
		return nil, providerErrors.NewForDisplayError("the Microsoft Entra ID app is missing the GroupMember.Read.All permission")
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
	adapter.SetBaseUrl(fmt.Sprintf("https://%s/%s", msgraphHost, msgraphAPIVersion))

	client := msgraphsdk.NewGraphServiceClient(adapter)

	// Get the groups (only the groups, not directory roles or administrative units, because that would require
	// additional permissions) which the user is a member of.
	graphGroups, err := getSecurityGroups(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get user groups: %v", err)
	}

	var groups []info.Group
	for _, msGroup := range graphGroups {
		idPtr := msGroup.GetId()
		if idPtr == nil {
			log.Warning(context.Background(), pp.Sprintf("Could not get ID for group: %v", msGroup))
			return nil, errors.New("could not get group id")
		}
		id := *idPtr

		groupNamePtr := msGroup.GetDisplayName()
		if groupNamePtr == nil {
			log.Warning(context.Background(), pp.Sprintf("Could not get display name for group object (ID: %s): %v", id, msGroup))
			return nil, errors.New("could not get group name")
		}
		groupName := strings.ToLower(*groupNamePtr)

		// Check if the group is a local group, in which case we don't set the UGID (because that's how the user manager
		// differentiates between local and remote groups).
		if strings.HasPrefix(groupName, localGroupPrefix) {
			groupName = strings.TrimPrefix(groupName, localGroupPrefix)
			groups = append(groups, info.Group{Name: groupName})
			continue
		}

		groups = append(groups, info.Group{Name: groupName, UGID: id})
	}

	return groups, nil
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
	for i, group := range groups {
		if !isSecurityGroup(group) {
			log.Debugf(context.Background(), "Removing non-security group %s", *group.GetDisplayName())
			groups = append(groups[:i], groups[i+1:]...)
		}
	}

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

// CurrentAuthenticationModesOffered returns the generic authentication modes supported by the provider.
//
// Token validity is not considered, only the presence of a token.
func (p Provider) CurrentAuthenticationModesOffered(
	sessionMode string,
	supportedAuthModes map[string]string,
	tokenExists bool,
	providerReachable bool,
	endpoints map[string]struct{},
	currentAuthStep int,
) ([]string, error) {
	log.Debugf(context.Background(), "In CurrentAuthenticationModesOffered: sessionMode=%q, supportedAuthModes=%q, tokenExists=%t, providerReachable=%t, endpoints=%q, currentAuthStep=%d\n", sessionMode, supportedAuthModes, tokenExists, providerReachable, endpoints, currentAuthStep)
	var offeredModes []string
	switch sessionMode {
	case "passwd":
		if !tokenExists {
			return nil, errors.New("user has no cached token")
		}
		offeredModes = []string{authmodes.Password}
		if currentAuthStep > 0 {
			offeredModes = []string{authmodes.NewPassword}
		}

	default: // auth mode
		if _, ok := endpoints[authmodes.DeviceQr]; ok && providerReachable {
			offeredModes = []string{authmodes.DeviceQr}
		} else if _, ok := endpoints[authmodes.Device]; ok && providerReachable {
			offeredModes = []string{authmodes.Device}
		}
		if tokenExists {
			offeredModes = append([]string{authmodes.Password}, offeredModes...)
		}
		if currentAuthStep > 0 {
			offeredModes = []string{authmodes.NewPassword}
		}
	}
	log.Debugf(context.Background(), "Offered modes: %q", offeredModes)

	for _, mode := range offeredModes {
		if _, ok := supportedAuthModes[mode]; !ok {
			return nil, fmt.Errorf("auth mode %q required by the provider, but is not supported locally", mode)
		}
	}

	return offeredModes, nil
}

// NormalizeUsername parses a username into a normalized version.
func (p Provider) NormalizeUsername(username string) string {
	// Microsoft Entra usernames are case-insensitive. We can safely use strings.ToLower here without worrying about
	// different Unicode characters that fold to the same lowercase letter, because the Microsoft Entra username policy
	// (which we check in VerifyUsername) ensures that the username only contains ASCII characters.
	return strings.ToLower(username)
}

// VerifyUsername checks if the authenticated username matches the requested username and that both are valid.
func (p Provider) VerifyUsername(requestedUsername, authenticatedUsername string) error {
	if p.NormalizeUsername(requestedUsername) != p.NormalizeUsername(authenticatedUsername) {
		return fmt.Errorf("requested username %q does not match the authenticated user %q", requestedUsername, authenticatedUsername)
	}

	// Check that the usernames only contain the characters allowed by the Microsoft Entra username policy
	// https://learn.microsoft.com/en-us/entra/identity/authentication/concept-sspr-policy#username-policies
	usernameRegexp := regexp.MustCompile(`^[a-zA-Z0-9'.\-_!#^~@]+$`)
	if !usernameRegexp.MatchString(authenticatedUsername) {
		// If this error occurs, we should investigate and probably relax the username policy, so we ask the user
		// explicitly to report this error.
		return providerErrors.NewForDisplayError("the authenticated username %q contains invalid characters. Please report this error on https://github.com/ubuntu/authd/issues", authenticatedUsername)
	}
	if !usernameRegexp.MatchString(requestedUsername) {
		return fmt.Errorf("requested username %q contains invalid characters", requestedUsername)
	}

	return nil
}

type azureTokenCredential struct {
	token *oauth2.Token
}

// GetToken creates an azcore.AccessToken from an oauth2.Token.
func (c azureTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     c.token.AccessToken,
		ExpiresOn: c.token.Expiry,
	}, nil
}
