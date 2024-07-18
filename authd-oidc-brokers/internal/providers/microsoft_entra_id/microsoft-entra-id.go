//go:build withmsentraid

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
	"github.com/k0kubun/pp"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
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

	var ok bool
	var name, id *string
	var groups []group.Info
	for _, obj := range m.GetValue() {
		v, err := obj.GetBackingStore().Get("displayName")
		if err != nil {
			return nil, err
		}
		name, ok = v.(*string)
		if !ok || name == nil {
			slog.Warn(pp.Sprintf("Invalid group found: %v", obj))
			return nil, errors.New("could not parse group name")
		}

		groupName := strings.ToLower(*name)

		// Local group
		if strings.HasPrefix(groupName, localGroupPrefix) {
			groupName = strings.TrimPrefix(groupName, localGroupPrefix)
			groups = append(groups, group.Info{Name: groupName})
			continue
		}

		v, err = obj.GetBackingStore().Get("id")
		if err != nil {
			return nil, err
		}
		id, ok = v.(*string)
		if !ok || id == nil {
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
