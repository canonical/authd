// Package microsoft_entra_id is the Microsoft Entra ID specific extension.
package microsoft_entra_id

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/kr/pretty"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/group"
	"golang.org/x/oauth2"
)

const localGroupPrefix = "linux-"

// MSEntraIDProvider is the Microsoft Entra ID provider implementation.
type MSEntraIDProvider struct{}

// AdditionalScopes returns the generic scopes required by the EntraID provider.
func (p MSEntraIDProvider) AdditionalScopes() []string {
	return []string{oidc.ScopeOfflineAccess}
}

// AuthOptions returns the generic auth options required by the EntraID provider.
func (p MSEntraIDProvider) AuthOptions() []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{}
}

// GetGroups access the Microsoft Graph API to get the groups the user is a member of.
func (p MSEntraIDProvider) GetGroups(token *oauth2.Token) ([]group.Info, error) {
	cred := azureTokenCredential{token: token}
	auth, err := msauth.NewAzureIdentityAuthenticationProvider(cred)
	if err != nil {
		return nil, err
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(auth)
	if err != nil {
		return nil, err
	}

	client := msgraphsdk.NewGraphServiceClient(adapter)

	m, err := client.Me().MemberOf().Get(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	var groups []group.Info
	for _, obj := range m.GetValue() {
		unknown := "Unknown"
		msGroup, ok := obj.(*models.Group)
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
			slog.Warn(pretty.Sprintf("Could not get displayName from group object (ID: %s) found: %v", *id, msGroup))
			return nil, errors.New("could not parse group name")
		}
		groupName := strings.ToLower(*name)

		// Local group
		if strings.HasPrefix(groupName, localGroupPrefix) {
			groupName = strings.TrimPrefix(groupName, localGroupPrefix)
			groups = append(groups, group.Info{Name: groupName})
			continue
		}

		v, err = msGroup.GetBackingStore().Get("id")
		if err != nil {
			return nil, err
		}
		id, ok := v.(*string)
		if !ok || id == nil {
			slog.Warn(pretty.Sprintf("Could not get ID for group %q: %v", groupName, msGroup))
			return nil, errors.New("could not parse group id")
		}

		groups = append(groups, group.Info{Name: groupName, UGID: *id})
	}

	return groups, nil
}

// CurrentAuthenticationModesOffered returns the generic authentication modes supported by the provider.
//
// Token validity is not considered, only the presence of a token.
func (p MSEntraIDProvider) CurrentAuthenticationModesOffered(
	sessionMode string,
	supportedAuthModes map[string]string,
	tokenExists bool,
	providerReachable bool,
	endpoints map[string]string,
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
		if providerReachable && endpoints["device_auth"] != "" {
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
