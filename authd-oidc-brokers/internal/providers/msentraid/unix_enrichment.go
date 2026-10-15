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

	// unavailable is set when the groups use a token without the Unix attribute scopes.
	unavailable error
}

// acquireAccessTokenForGraphAPI is a variable so that tests can replace it.
var acquireAccessTokenForGraphAPI = himmelblau.AcquireAccessTokenForGraphAPI

func (p *Provider) acquireGraphAccess(
	ctx context.Context,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationDataJSON []byte,
	requiredScopes []string,
	fallbackScopes []string,
	needsAccessTokenForGraphAPI bool,
) (*graphAccess, error) {
	if token == nil {
		return nil, errors.New("access token is nil")
	}

	accessTokenStr := token.AccessToken
	accessToken, _, parseErr := new(jwt.Parser).ParseUnverified(accessTokenStr, jwt.MapClaims{})
	var accessTokenScopes []string
	if parseErr == nil {
		accessTokenScopes, _ = p.getTokenScopes(accessToken)
	}
	accessTokenHasGroupScope := slices.Contains(accessTokenScopes, graphGroupMemberReadAllScope)
	accessTokenHasRequiredScopes := missingGraphScope(accessTokenScopes, requiredScopes) == ""

	if p.graphClientSecret != "" && !accessTokenHasGroupScope && !needsAccessTokenForGraphAPI {
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

	var unavailable error
	if needsAccessTokenForGraphAPI && !accessTokenHasRequiredScopes {
		var data himmelblau.DeviceRegistrationData
		if err := json.Unmarshal(deviceRegistrationDataJSON, &data); err != nil {
			log.Noticef(ctx, "Could not decode device registration data: %v", err)
			return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}

		accessTokenStr, err := acquireAccessTokenForGraphAPI(ctx, clientID, tenantID(issuerURL), token, data, requiredScopes)
		if err != nil && fallbackScopes != nil && !errors.Is(err, himmelblau.ErrDeviceDisabled) && !errors.Is(err, himmelblau.ErrInvalidRedirectURI) {
			log.Warningf(ctx, "Could not get a Graph token for the Unix attribute scopes, retrying for groups only: %v", err)
			unavailable = graphTokenError(err)
			accessTokenStr, err = acquireAccessTokenForGraphAPI(ctx, clientID, tenantID(issuerURL), token, data, fallbackScopes)
			if err != nil {
				log.Warningf(ctx, "Could not get a Graph token for groups: %v", err)
				// The groups-only request blocked enrichment, so report its error.
				return nil, graphTokenError(err)
			}
		}
		if err != nil {
			return nil, graphTokenError(err)
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
	return &graphAccess{client: client, token: accessToken, unavailable: unavailable}, nil
}

// graphTokenError maps a Graph token acquisition error to the error that the broker handles.
func graphTokenError(err error) error {
	if errors.Is(err, himmelblau.ErrDeviceDisabled) {
		return fmt.Errorf("%w: %w", providerErrors.ErrDeviceDisabled, err)
	}
	if errors.Is(err, himmelblau.ErrInvalidRedirectURI) {
		msg := "Token acquisition failed: The app is misconfigured in Microsoft Entra (the redirect URI is missing or invalid). Please contact your administrator."
		return &providerErrors.ForDisplayError{Message: msg, Err: fmt.Errorf("%w: %w", providerErrors.ErrInvalidRedirectURI, err)}
	}
	var tokenAcquisitionError himmelblau.TokenAcquisitionError
	if errors.As(err, &tokenAcquisitionError) {
		return &providerErrors.RetryWithDeviceAuthError{Err: fmt.Errorf("failed to acquire access token for Microsoft Graph API: %w", err)}
	}
	return fmt.Errorf("failed to acquire access token for Microsoft Graph API: %w", err)
}

// EnrichUserWithUnixAttributes returns user with current Unix IDs.
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
	requiredScopes := graphScopesForUnixAttributes(config)
	var fallbackScopes []string
	if config.UIDAttribute != "" {
		fallbackScopes = []string{graphGroupMemberReadAllScope}
	}
	access, err := p.acquireGraphAccess(
		ctx,
		clientID,
		issuerURL,
		token,
		providerMetadata,
		deviceRegistrationData,
		requiredScopes,
		fallbackScopes,
		len(deviceRegistrationData) > 0,
	)
	if err != nil {
		return info.User{}, err
	}

	// Attributes that cannot be read keep their cached values. Only mandatory ones are reported as unreadable.
	var unreadable []error
	// abort stops the enrichment with err and keeps the mandatory failures found so far.
	abort := func(err error) (info.User, error) {
		return info.User{}, errors.Join(append(unreadable, err)...)
	}
	if access.unavailable != nil && config.UIDRequired {
		unreadable = append(unreadable, markRequiredAttribute(access.unavailable))
	}
	enriched := user
	if config.UIDAttribute == "" {
		enriched.UID = nil
	}
	cachedGroups := user.Groups
	enriched.Groups = nil

	var tokenScopes []string
	if !access.appOnly {
		tokenScopes = p.tokenScopesForGraphAPI
		if tokenScopes == nil {
			tokenScopes, err = p.getTokenScopes(access.token)
			if err != nil {
				return abort(err)
			}
		}
		if missingScope := missingGraphScope(tokenScopes, []string{graphGroupMemberReadAllScope}); missingScope != "" {
			return abort(&providerErrors.ForDisplayError{
				Message: fmt.Sprintf("Error: the Microsoft Entra ID app is missing the %s permission", missingScope),
			})
		}
	}

	if config.UIDAttribute != "" && access.unavailable == nil {
		uid, read, err := lookupUnixUID(ctx, access, tokenScopes, config)
		if read {
			enriched.UID = uid
		}
		if err != nil {
			if config.UIDRequired {
				unreadable = append(unreadable, markRequiredAttribute(err))
			} else {
				log.Warningf(ctx, "Could not read Unix UID attribute %q, keeping the cached value: %v", config.UIDAttribute, err)
			}
		}
	}

	fetchGroups := func(fields []string) ([]msgraphmodels.Groupable, error) {
		if access.appOnly {
			return getSecurityGroupsByUserIDWithSelect(ctx, access.client, access.userOID, fields)
		}
		return getSecurityGroupsWithSelect(ctx, access.client, fields)
	}
	gidsRead := true
	graphGroups, err := fetchGroups(groupSelectFields(config.GIDAttribute))
	if err != nil && config.GIDAttribute != "" {
		if config.GIDRequired {
			unreadable = append(unreadable, markRequiredAttribute(fmt.Errorf("could not read Unix GID attribute %q: %w", config.GIDAttribute, err)))
		} else {
			log.Warningf(ctx, "Could not read Unix GID attribute %q, keeping cached GIDs: %v", config.GIDAttribute, err)
		}
		gidsRead = false
		graphGroups, err = fetchGroups(groupSelectFields(""))
	}
	if err != nil {
		return abort(err)
	}

	enriched.Groups, err = processSecurityGroupsWithGID(graphGroups, config.GIDAttribute)
	if err != nil {
		return abort(err)
	}
	if !gidsRead {
		info.KeepCachedGIDs(enriched.Groups, cachedGroups)
	}
	if config.GIDAttribute != "" && config.GIDRequired {
		for _, group := range enriched.Groups {
			if group.UGID != "" && group.GID == nil {
				unreadable = append(unreadable, fmt.Errorf("%w: Unix GID is missing for group %q", info.ErrUnixGIDRequired, group.Name))
				break
			}
		}
	}

	if len(unreadable) > 0 {
		return enriched, fmt.Errorf("%w: %w", info.ErrUnixAttributePartial, errors.Join(unreadable...))
	}
	return enriched, nil
}

// lookupUnixUID reads the UID attribute. read is false when the attribute could not be read, and then the cached UID is kept.
func lookupUnixUID(ctx context.Context, access *graphAccess, tokenScopes []string, config info.UnixAttributeConfig) (uid *uint32, read bool, err error) {
	if access.appOnly {
		if config.UIDRequired {
			return nil, false, fmt.Errorf("%w: Unix UID attribute %q is unavailable with an app-only Graph token", info.ErrUnixUIDRequired, config.UIDAttribute)
		}
		log.Warningf(ctx, "Unix UID attribute %q is unavailable with an app-only Graph token", config.UIDAttribute)
		return nil, false, nil
	}
	if missingScope := missingGraphScope(tokenScopes, []string{graphUserReadScope}); missingScope != "" {
		return nil, false, &providerErrors.ForDisplayError{
			Message: fmt.Sprintf("Error: the Microsoft Entra ID app is missing the %s permission", missingScope),
		}
	}
	graphUser, err := getGraphUser(ctx, access.client, config.UIDAttribute)
	if err != nil {
		return nil, false, err
	}
	uid, err = parseConfiguredUnixID(graphUser.GetAdditionalData(), config.UIDAttribute, "user", config.UIDRequired)
	return uid, true, err
}

func graphScopesForUnixAttributes(config info.UnixAttributeConfig) []string {
	scopes := []string{graphGroupMemberReadAllScope}
	if config.UIDAttribute != "" {
		scopes = append(scopes, graphUserReadScope)
	}
	return scopes
}

func missingGraphScope(tokenScopes, requiredScopes []string) string {
	for _, scope := range requiredScopes {
		if !slices.Contains(tokenScopes, scope) {
			return scope
		}
	}
	return ""
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

func markRequiredAttribute(err error) error {
	if errors.Is(err, info.ErrUnixAttributeRequired) {
		return err
	}
	return fmt.Errorf("%w: %w", info.ErrUnixAttributeRequired, err)
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
