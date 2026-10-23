//go:build withmsentraid

package msentraid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	providerErrors "github.com/canonical/authd/brokers/internal/providers/errors"
	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/log"
	"github.com/golang-jwt/jwt/v5"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	msgraphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
	"golang.org/x/oauth2"
)

type graphAccess struct {
	client *msgraphsdk.GraphServiceClient
	token  *jwt.Token
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
	accessTokenHasRequiredScopes := missingGraphScope(accessTokenScopes, requiredScopes) == ""

	if needsAccessTokenForGraphAPI && !accessTokenHasRequiredScopes {
		var data himmelblau.DeviceRegistrationData
		if err := json.Unmarshal(deviceRegistrationDataJSON, &data); err != nil {
			log.Noticef(ctx, "Could not decode device registration data: %v", err)
			return nil, fmt.Errorf("failed to unmarshal device registration data: %v", err)
		}

		accessTokenStr, err := acquireGraphAccessTokenWithRetry(ctx, func() (string, error) {
			return acquireAccessTokenForGraphAPI(ctx, clientID, tenantID(issuerURL), token, data, requiredScopes)
		}, p.graphTokenAcquisitionRetryInterval, p.graphTokenAcquisitionRetryTimeout)
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
	return &graphAccess{client: client, token: accessToken}, nil
}

// graphTokenError maps a Graph token acquisition error to the error that the broker handles.
func graphTokenError(err error) error {
	return classifyGraphTokenAcquisitionError(err)
}

// EnrichUserWithUnixAttributes returns user with current Unix IDs.
func (p *Provider) EnrichUserWithUnixAttributes(
	ctx context.Context,
	user info.User,
	groupFetchResult info.GroupFetchResult,
	clientID string,
	issuerURL string,
	token *oauth2.Token,
	providerMetadata map[string]interface{},
	deviceRegistrationData []byte,
	config info.UnixAttributeConfig,
) (info.User, error) {
	// Attributes that cannot be read keep their cached values. Only mandatory ones are reported as unreadable.
	var unreadable []error
	enriched := user
	if config.UIDAttribute == "" {
		enriched.UID = nil
	}

	if config.UIDAttribute != "" {
		access, err := p.acquireGraphAccess(
			ctx,
			clientID,
			issuerURL,
			token,
			providerMetadata,
			deviceRegistrationData,
			graphScopesForUnixAttributes(config),
			len(deviceRegistrationData) > 0,
		)
		if err != nil {
			if errors.Is(err, providerErrors.ErrDeviceDisabled) || errors.Is(err, providerErrors.ErrInvalidRedirectURI) {
				return info.User{}, err
			}
			if config.UIDRequired {
				unreadable = append(unreadable, markRequiredAttribute(fmt.Errorf("%w: could not acquire a Graph token for Unix UID attribute %q: %w", info.ErrUnixUIDRequired, config.UIDAttribute, err)))
			} else {
				log.Warningf(ctx, "Could not acquire a Graph token for Unix UID attribute %q, keeping the cached value: %v", config.UIDAttribute, err)
			}
		} else {
			tokenScopes := p.tokenScopesForGraphAPI
			if tokenScopes == nil {
				tokenScopes, err = p.getTokenScopes(access.token)
				if err != nil {
					return info.User{}, errors.Join(append(unreadable, err)...)
				}
			}

			uid, read, err := lookupUnixUID(ctx, access, tokenScopes, config)
			if read {
				enriched.UID = uid
			}
			if err != nil {
				if config.UIDRequired {
					unreadable = append(unreadable, markRequiredAttribute(fmt.Errorf("%w: %w", info.ErrUnixUIDRequired, err)))
				} else {
					log.Warningf(ctx, "Could not read Unix UID attribute %q, keeping the cached value: %v", config.UIDAttribute, err)
				}
			}
		}
	}

	enriched.Groups = slices.Clone(groupFetchResult.Groups)
	if config.GIDAttribute != "" {
		if groupFetchResult.GIDAttributeError != nil {
			info.KeepCachedGIDs(enriched.Groups, user.Groups)
			if config.GIDRequired {
				unreadable = append(unreadable, markRequiredAttribute(fmt.Errorf("%w: could not read Unix GID attribute %q: %w", info.ErrUnixGIDRequired, config.GIDAttribute, groupFetchResult.GIDAttributeError)))
			} else {
				log.Warningf(ctx, "Could not read Unix GID attribute %q, keeping the cached value: %v", config.GIDAttribute, groupFetchResult.GIDAttributeError)
			}
		} else {
			enriched.Groups = enrichSecurityGroupGIDs(enriched.Groups, config.GIDAttribute)
		}
		if config.GIDRequired && groupFetchResult.GIDAttributeError == nil {
			for _, group := range enriched.Groups {
				if group.UGID != "" && group.GID == nil {
					unreadable = append(unreadable, fmt.Errorf("%w: %w: Unix GID is missing for group %q", info.ErrUnixGIDRequired, info.ErrUnixAttributeMissing, group.Name))
					break
				}
			}
		}
	}
	for i := range enriched.Groups {
		enriched.Groups[i].AdditionalData = nil
	}

	if len(unreadable) > 0 {
		return enriched, fmt.Errorf("%w: %w", info.ErrUnixAttributePartial, errors.Join(unreadable...))
	}
	return enriched, nil
}

// lookupUnixUID reads the UID attribute. read is false when the attribute could not be read, and then the cached UID is kept.
func lookupUnixUID(ctx context.Context, access *graphAccess, tokenScopes []string, config info.UnixAttributeConfig) (uid *uint32, read bool, err error) {
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
	if config.UIDAttribute != "" {
		return []string{graphUserReadScope}
	}
	return nil
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
			return nil, fmt.Errorf("%w: %w: Unix attribute %q is missing from %s", requiredUnixAttributeError(object), info.ErrUnixAttributeMissing, attribute, object)
		}
		return nil, nil
	}

	parsed, err := parseUnixID(value)
	if err != nil {
		log.Warningf(context.Background(), "Invalid Unix attribute %q in %s: %v", attribute, object, err)
		if required {
			return nil, fmt.Errorf("%w: %w: invalid Unix attribute %q in %s: %w", requiredUnixAttributeError(object), info.ErrUnixAttributeMissing, attribute, object, err)
		}
		return nil, nil
	}
	return parsed, nil
}
