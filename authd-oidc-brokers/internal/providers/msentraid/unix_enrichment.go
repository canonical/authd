//go:build withmsentraid

package msentraid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	providerErrors "github.com/canonical/authd/authd-oidc-brokers/internal/providers/errors"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/log"
	"github.com/golang-jwt/jwt/v5"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	msgraphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
	"golang.org/x/oauth2"
)

type graphAccess struct {
	client  *msgraphsdk.GraphServiceClient
	token   *jwt.Token
	appOnly bool
	userOID string
}

func (p *Provider) acquireGraphAccess(
	ctx context.Context,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationDataJSON []byte,
	needsAccessTokenForGraphAPI bool,
) (*graphAccess, error) {
	if token == nil {
		return nil, errors.New("access token is nil")
	}

	accessTokenStr := token.AccessToken
	accessToken, _, parseErr := new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	accessTokenHasGraphScope := false
	if parseErr == nil {
		if scopes, scopeErr := p.getTokenScopes(accessToken); scopeErr == nil {
			accessTokenHasGraphScope = slices.Contains(scopes, "GroupMember.Read.All")
		}
	}

	if p.graphClientSecret != "" && !accessTokenHasGraphScope && !needsAccessTokenForGraphAPI {
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse access token for client credentials group lookup: %w", parseErr)
		}
		oid, oidErr := p.getOIDFromToken(accessToken)
		if oidErr != nil {
			log.Noticef(ctx, "Could not extract OID from access token for client credentials path, falling back: %v", oidErr)
		} else {
			host := resolveMSGraphHost(providerMetadata)
			log.Infof(ctx, "Resolving groups for OID %s via app-only client credentials (delegated token lacks GroupMember.Read.All)", oid)
			appTokenStr, err := acquireClientCredentialsToken(ctx, issuerURL, clientID, p.graphClientSecret, host)
			if err != nil {
				return nil, fmt.Errorf("failed to acquire client credentials token for Graph API: %w", err)
			}
			appToken, _, err := new(jwt.Parser).ParseUnverified(appTokenStr, jwt.MapClaims{})
			if err != nil {
				return nil, fmt.Errorf("failed to parse client credentials token: %w", err)
			}
			client, err := newGraphServiceClient(appToken, host)
			if err != nil {
				return nil, err
			}
			return &graphAccess{client: client, token: appToken, appOnly: true, userOID: oid}, nil
		}
	}

	if needsAccessTokenForGraphAPI && !accessTokenHasGraphScope {
		var data himmelblau.DeviceRegistrationData
		if err := json.Unmarshal(deviceRegistrationDataJSON, &data); err != nil {
			log.Noticef(ctx, "Device registration JSON data: %s", deviceRegistrationDataJSON)
			return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}

		accessTokenStr, err := himmelblau.AcquireAccessTokenForGraphAPI(ctx, clientID, tenantID(issuerURL), token, data)
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

		accessToken, _, parseErr = new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	}
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse access token: %w", parseErr)
	}

	client, err := newGraphServiceClient(accessToken, resolveMSGraphHost(providerMetadata))
	if err != nil {
		return nil, err
	}
	return &graphAccess{client: client, token: accessToken}, nil
}

// EnrichUserWithUnixAttributes returns a complete user snapshot with current
// Microsoft Entra Unix UID and GID attributes.
func (p *Provider) EnrichUserWithUnixAttributes(
	ctx context.Context,
	user info.User,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationData []byte,
	config info.UnixAttributeConfig,
) (info.User, error) {
	access, err := p.acquireGraphAccess(ctx, clientID, issuerURL, token, providerMetadata, deviceRegistrationData, len(deviceRegistrationData) > 0)
	if err != nil {
		return info.User{}, err
	}

	enriched := user
	enriched.UID = nil
	enriched.Groups = nil

	if !access.appOnly {
		scopes := p.tokenScopesForGraphAPI
		if scopes == nil {
			scopes, err = p.getTokenScopes(access.token)
			if err != nil {
				return info.User{}, err
			}
		}
		if !slices.Contains(scopes, "GroupMember.Read.All") {
			return info.User{}, &providerErrors.ForDisplayError{Message: "Error: the Microsoft Entra ID app is missing the GroupMember.Read.All permission"}
		}
	}

	if config.UIDAttribute != "" {
		if access.appOnly {
			if config.UIDRequired {
				return info.User{}, fmt.Errorf("%w: Unix UID attribute %q is unavailable with an app-only Graph token", info.ErrUnixUIDRequired, config.UIDAttribute)
			}
			log.Warningf(ctx, "Unix UID attribute %q is unavailable with an app-only Graph token", config.UIDAttribute)
		} else {
			graphUser, err := getGraphUser(ctx, access.client, config.UIDAttribute)
			if err != nil {
				return info.User{}, err
			}
			uid, err := parseConfiguredUnixID(graphUser.GetAdditionalData(), config.UIDAttribute, "user", config.UIDRequired)
			if err != nil {
				return info.User{}, err
			}
			enriched.UID = uid
		}
	}

	selectFields := groupSelectFields(config.GIDAttribute)
	var graphGroups []msgraphmodels.Groupable
	if access.appOnly {
		graphGroups, err = getSecurityGroupsByUserIDWithSelect(ctx, access.client, access.userOID, selectFields)
	} else {
		graphGroups, err = getSecurityGroupsWithSelect(ctx, access.client, selectFields)
	}
	if err != nil {
		return info.User{}, err
	}

	enriched.Groups, err = processSecurityGroupsWithGID(graphGroups, config.GIDAttribute, config.GIDRequired)
	if err != nil {
		return info.User{}, err
	}
	return enriched, nil
}

func getGraphUser(ctx context.Context, client *msgraphsdk.GraphServiceClient, attribute string) (msgraphmodels.Userable, error) {
	result, err := client.Me().Get(ctx, &msgraphusers.UserItemRequestBuilderGetRequestConfiguration{
		QueryParameters: &msgraphusers.UserItemRequestBuilderGetQueryParameters{Select: []string{attribute}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get user Unix attribute %q: %w", attribute, err)
	}
	if result == nil {
		return nil, fmt.Errorf("failed to get user Unix attribute %q: empty Graph response", attribute)
	}
	return result, nil
}

func requiredUnixAttributeError(object string) error {
	if object == "user" {
		return info.ErrUnixUIDRequired
	}
	return info.ErrUnixGIDRequired
}

func parseConfiguredUnixID(additionalData map[string]any, attribute, object string, required bool) (*uint32, error) {
	value, ok := additionalData[attribute]
	if !ok || value == nil {
		if required {
			return nil, fmt.Errorf("%w: Unix attribute %q is missing from %s", requiredUnixAttributeError(object), attribute, object)
		}
		return nil, nil
	}

	parsed, err := parseUnixID(value)
	if err != nil {
		log.Warningf(context.Background(), "Invalid Unix attribute %q in %s: %v", attribute, object, err)
		if required {
			return nil, fmt.Errorf("%w: invalid Unix attribute %q in %s: %w", requiredUnixAttributeError(object), attribute, object, err)
		}
		return nil, nil
	}
	return parsed, nil
}
