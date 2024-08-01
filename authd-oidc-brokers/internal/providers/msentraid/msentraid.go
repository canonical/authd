// Package msentraid is the Microsoft Entra ID specific extension.
package msentraid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/k0kubun/pp"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	msgraphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

func init() {
	pp.ColoringEnabled = false
}

const localGroupPrefix = "linux-"

// Provider is the Microsoft Entra ID provider implementation.
type Provider struct{}

// AdditionalScopes returns the generic scopes required by the EntraID provider.
func (p Provider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess}
}

// AuthOptions returns the generic auth options required by the EntraID provider.
func (p Provider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetUserInfo is a no-op when no specific provider is in use.
func (p Provider) GetUserInfo(ctx context.Context, accessToken *oauth2.Token, idToken *oidc.IDToken) (info.User, error) {
	userClaims, err := p.userClaims(idToken)
	if err != nil {
		return info.User{}, err
	}

	userGroups, err := p.getGroups(accessToken)
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
		return claims{}, fmt.Errorf("could not get user info: %v", err)
	}
	return userClaims, nil
}

// getGroups access the Microsoft Graph API to get the groups the user is a member of.
func (p Provider) getGroups(token *oauth2.Token) ([]info.Group, error) {
	cred := azureTokenCredential{token: token}
	auth, err := msgraphauth.NewAzureIdentityAuthenticationProvider(cred)
	if err != nil {
		return nil, err
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(auth)
	if err != nil {
		return nil, err
	}

	client := msgraphsdk.NewGraphServiceClient(adapter)

	// Check GroupMember.Read.All access
	var topOne int32 = 1
	requestOptions := &msgraphgroups.GroupsRequestBuilderGetRequestConfiguration{
		QueryParameters: &msgraphgroups.GroupsRequestBuilderGetQueryParameters{
			Top: &topOne, // Limit to only one group
		},
	}
	if _, err = client.Groups().Get(context.Background(), requestOptions); err != nil {
		return nil, fmt.Errorf("could not access user's groups: %v", err)
	}

	m, err := client.Me().TransitiveMemberOf().Get(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	var groups []info.Group
	for _, obj := range m.GetValue() {
		unknown := "Unknown"
		msGroup, ok := obj.(*msgraphmodels.Group)
		if !ok {
			id, oType := obj.GetId(), obj.GetOdataType()
			if id == nil {
				id = &unknown
			}
			if oType == nil {
				oType = &unknown
			}
			slog.Debug(fmt.Sprintf(
				"Found non-group object with ID: %q of type: %q in graphsdk response. Ignoring it",
				*id, *oType,
			))
			continue
		}

		v, err := msGroup.GetBackingStore().Get("displayName")
		if err != nil {
			return nil, err
		}
		name, ok := v.(*string)
		if !ok || name == nil {
			id := msGroup.GetId()
			if id == nil {
				id = &unknown
			}
			slog.Warn(pp.Sprintf("Could not get displayName from group object (ID: %s) found: %v", *id, *msGroup))
			return nil, errors.New("could not parse group name")
		}
		groupName := strings.ToLower(*name)

		// Local group
		if strings.HasPrefix(groupName, localGroupPrefix) {
			groupName = strings.TrimPrefix(groupName, localGroupPrefix)
			groups = append(groups, info.Group{Name: groupName})
			continue
		}

		v, err = msGroup.GetBackingStore().Get("id")
		if err != nil {
			return nil, err
		}
		id, ok := v.(*string)
		if !ok || id == nil {
			slog.Warn(pp.Sprintf("Could not get ID for group %q: %v", groupName, *msGroup))
			return nil, errors.New("could not parse group id")
		}

		groups = append(groups, info.Group{Name: groupName, UGID: *id})
	}

	return groups, nil
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
