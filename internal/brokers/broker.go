package brokers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
	"github.com/godbus/dbus/v5"
	"golang.org/x/exp/slices"
)

// LocalBrokerName is the name of the local broker.
const LocalBrokerName = "local"

type brokerer interface {
	NewSession(ctx context.Context, username, lang, mode string) (sessionID, encryptionKey string, err error)
	GetAuthenticationModes(ctx context.Context, sessionID string, supportedUILayouts []map[string]string) (authenticationModes []map[string]string, msg string, err error)
	SelectAuthenticationMode(ctx context.Context, sessionID, authenticationModeName string) (uiLayoutInfo map[string]string, err error)
	IsAuthenticated(ctx context.Context, sessionID, authenticationData string) (access, data string, err error)
	EndSession(ctx context.Context, sessionID string) (err error)
	CancelIsAuthenticated(ctx context.Context, sessionID string)

	UserPreCheck(ctx context.Context, username string) (userinfo string, err error)
}

// Broker represents a broker object that can be used for authentication.
type Broker struct {
	ID                    string
	Name                  string
	BrandIconPath         string
	layoutValidators      map[string]map[string]layoutValidator
	layoutValidatorsMu    *sync.Mutex
	ongoingUserRequests   map[string]string
	ongoingUserRequestsMu *sync.Mutex

	brokerer brokerer
}

type layoutValidator map[string]fieldValidator

type fieldValidator struct {
	supportedValues []string
	required        bool
}

// newBroker creates a new broker object based on the provided config file. No config means local broker.
func newBroker(ctx context.Context, configFile string, bus *dbus.Conn) (b Broker, err error) {
	defer decorate.OnError(&err, "can't create broker from %q", configFile)

	name := LocalBrokerName
	id := LocalBrokerName
	var brandIcon string
	var broker brokerer

	if configFile != "" {
		log.Debugf(ctx, "Loading broker from %q", configFile)
		broker, name, brandIcon, err = newDbusBroker(ctx, bus, configFile)
		if err != nil {
			return Broker{}, err
		}
		h := fnv.New32a()
		// This can’t error out in Hash32 implementation.
		_, _ = h.Write([]byte(name))
		id = fmt.Sprint(h.Sum32())
	}

	return Broker{
		ID:                    id,
		Name:                  name,
		BrandIconPath:         brandIcon,
		brokerer:              broker,
		layoutValidators:      make(map[string]map[string]layoutValidator),
		layoutValidatorsMu:    &sync.Mutex{},
		ongoingUserRequests:   make(map[string]string),
		ongoingUserRequestsMu: &sync.Mutex{},
	}, nil
}

// newSession calls the broker corresponding method, expanding sessionID with the broker ID prefix.
func (b Broker) newSession(ctx context.Context, username, lang, mode string) (sessionID, encryptionKey string, err error) {
	sessionID, encryptionKey, err = b.brokerer.NewSession(ctx, username, lang, mode)
	if err != nil {
		return "", "", err
	}

	if sessionID == "" {
		return "", "", errors.New("no session ID provided by broker")
	}

	b.ongoingUserRequestsMu.Lock()
	b.ongoingUserRequests[sessionID] = username
	b.ongoingUserRequestsMu.Unlock()

	return fmt.Sprintf("%s-%s", b.ID, sessionID), encryptionKey, nil
}

// GetAuthenticationModes calls the broker corresponding method, stripping broker ID prefix from sessionID.
func (b *Broker) GetAuthenticationModes(ctx context.Context, sessionID string, supportedUILayouts []map[string]string) (authenticationModes []map[string]string, msg string, err error) {
	sessionID = b.parseSessionID(sessionID)

	b.layoutValidatorsMu.Lock()
	b.layoutValidators[sessionID] = generateValidators(ctx, sessionID, supportedUILayouts)
	b.layoutValidatorsMu.Unlock()

	authenticationModes, msg, err = b.brokerer.GetAuthenticationModes(ctx, sessionID, supportedUILayouts)
	if err != nil {
		return nil, "", err
	}

	for _, a := range authenticationModes {
		for _, key := range []string{layouts.ID, layouts.Label} {
			if _, exists := a[key]; !exists {
				return nil, "", fmt.Errorf("invalid authentication mode, missing %q key: %v", key, a)
			}
		}
	}

	return authenticationModes, msg, nil
}

// SelectAuthenticationMode calls the broker corresponding method, stripping broker ID prefix from sessionID.
func (b Broker) SelectAuthenticationMode(ctx context.Context, sessionID, authenticationModeName string) (uiLayoutInfo map[string]string, err error) {
	sessionID = b.parseSessionID(sessionID)
	uiLayoutInfo, err = b.brokerer.SelectAuthenticationMode(ctx, sessionID, authenticationModeName)
	if err != nil {
		return nil, err
	}
	return b.validateUILayout(sessionID, uiLayoutInfo)
}

// IsAuthenticated calls the broker corresponding method, stripping broker ID prefix from sessionID.
func (b Broker) IsAuthenticated(ctx context.Context, sessionID, authenticationData string) (access string, data string, err error) {
	sessionID = b.parseSessionID(sessionID)

	// monitor ctx in goroutine to call cancel
	done := make(chan struct{})
	go func() {
		access, data, err = b.brokerer.IsAuthenticated(ctx, sessionID, authenticationData)
		close(done)
	}()

	select {
	case <-done:
		if errors.Is(err, context.Canceled) {
			log.Debugf(ctx, "Authentication for session %s was canceled", sessionID)
			return auth.Cancelled, "{}", nil
		}
		if err != nil {
			return "", "", err
		}
	case <-ctx.Done():
		log.Warningf(ctx, "Authentication aborted: PAM client disconnected unexpectedly (session %s)", sessionID)
		log.Debugf(ctx, "Cancelling broker authentication (session %s)", sessionID)
		b.cancelIsAuthenticated(ctx, sessionID)
		<-done
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf(ctx, "Authentication failed: %v", err)
		}
		return auth.Cancelled, "{}", nil
	}

	// Validate access authentication.
	if !slices.Contains(auth.Replies, access) {
		return "", "", fmt.Errorf("invalid broker response: %q", access)
	}

	if data == "" {
		data = "{}"
	}

	switch access {
	case auth.Granted:
		rawUserInfo, err := unmarshalAndGetKey(data, "userinfo")
		if err != nil {
			return "", "", err
		}

		info, err := unmarshalUserInfo(rawUserInfo)
		if err != nil {
			return "", "", err
		}

		if err = validateUserInfo(info); err != nil {
			return "", "", err
		}

		d, err := json.Marshal(info)
		if err != nil {
			return "", "", fmt.Errorf("can't marshal UserInfo: %v", err)
		}
		data = string(d)

	case auth.Denied, auth.Retry:
		if _, err := unmarshalAndGetKey(data, "message"); err != nil {
			return "", "", err
		}

	case auth.Next:
		if data == "{}" {
			break
		}
		if _, err := unmarshalAndGetKey(data, "message"); err != nil {
			return "", "", err
		}

	case auth.Cancelled:
		if data != "{}" {
			return "", "", fmt.Errorf("access mode %q should not return any data, got: %v", access, data)
		}
	}

	return access, data, nil
}

// endSession calls the broker corresponding method, stripping broker ID prefix from sessionID.
func (b Broker) endSession(ctx context.Context, sessionID string) (err error) {
	sessionID = b.parseSessionID(sessionID)

	b.ongoingUserRequestsMu.Lock()
	defer b.ongoingUserRequestsMu.Unlock()
	delete(b.ongoingUserRequests, sessionID)

	return b.brokerer.EndSession(ctx, sessionID)
}

// cancelIsAuthenticated calls the broker corresponding method.
// If the session does not have a pending IsAuthenticated call, this is a no-op.
//
// Even though this is a public method, it should only be interacted with through IsAuthenticated and ctx cancellation.
func (b Broker) cancelIsAuthenticated(ctx context.Context, sessionID string) {
	b.brokerer.CancelIsAuthenticated(ctx, sessionID)
}

// UserPreCheck calls the broker corresponding method.
func (b Broker) UserPreCheck(ctx context.Context, username string) (userinfo string, err error) {
	log.Debugf(context.TODO(), "Pre-checking user %q", username)
	return b.brokerer.UserPreCheck(ctx, username)
}

// generateValidators generates layout validators based on what is supported by the system.
//
// The layout validators are in the form:
//
//	{
//	    "LAYOUT_TYPE": {
//	        "FIELD_NAME": fieldValidator{
//	            supportedValues: []string{"SUPPORTED_VALUE_1", "SUPPORTED_VALUE_2"},
//	            required: true,
//	        }
//	    }
//	}
func generateValidators(ctx context.Context, sessionID string, supportedUILayouts []map[string]string) map[string]layoutValidator {
	validators := make(map[string]layoutValidator)
	for _, layout := range supportedUILayouts {
		if _, exists := layout[layouts.Type]; !exists {
			log.Errorf(ctx, "layout %v provided with missing type for session %s, it will be ignored", layout, sessionID)
			continue
		}

		layoutValidator := make(layoutValidator)
		for key, value := range layout {
			if key == layouts.Type {
				continue
			}

			kind, supportedValues := layouts.ParseItems(value)
			validator := fieldValidator{
				supportedValues: supportedValues,
				required:        (kind == layouts.Required),
			}
			layoutValidator[key] = validator
		}
		validators[layout[layouts.Type]] = layoutValidator
	}
	return validators
}

// validateUILayout validates the layout fields and content according to the broker validators and returns the layout
// containing all required fields and the optional fields that were set.
//
// If the layout is not valid (missing required fields or invalid values), an error is returned instead.
func (b Broker) validateUILayout(sessionID string, layout map[string]string) (r map[string]string, err error) {
	defer decorate.OnError(&err, "could not validate UI layout")

	b.layoutValidatorsMu.Lock()
	defer b.layoutValidatorsMu.Unlock()

	layoutValidators, exists := b.layoutValidators[sessionID]
	if !exists {
		return nil, fmt.Errorf("session %q does not have any layout validator", sessionID)
	}

	// layoutValidator is UI Layout validator generated based on the supported layouts.
	layoutValidator, exists := layoutValidators[layout[layouts.Type]]
	if !exists {
		return nil, fmt.Errorf("no validator for UI layout type %q", layout[layouts.Type])
	}

	// Ensure that all fields provided in the layout returned by the broker are valid.
	for key := range layout {
		if key == layouts.Type {
			continue
		}
		if _, exists := layoutValidator[key]; !exists {
			return nil, fmt.Errorf("unrecognized field %q provided for layout %q", key, layout[layouts.Type])
		}
	}
	// Ensure that all required fields were provided and that the values are valid.
	for key, validator := range layoutValidator {
		value, exists := layout[key]
		if !exists || value == "" {
			if validator.required {
				return nil, fmt.Errorf("required field %q was not provided", key)
			}
			continue
		}
		if validator.supportedValues != nil && !slices.Contains(validator.supportedValues, value) {
			return nil, fmt.Errorf("field %q has invalid value %q, expected one of %s", key, value, strings.Join(validator.supportedValues, ","))
		}
	}
	return layout, nil
}

// parseSessionID strips broker ID prefix from sessionID.
func (b Broker) parseSessionID(sessionID string) string {
	return strings.TrimPrefix(sessionID, fmt.Sprintf("%s-", b.ID))
}

// unmarshalUserInfo tries to unmarshal the rawMsg into a userinfo.
func unmarshalUserInfo(rawMsg json.RawMessage) (types.UserInfo, error) {
	var u types.UserInfo
	if err := json.Unmarshal(rawMsg, &u); err != nil {
		return types.UserInfo{}, fmt.Errorf("message is not JSON formatted: %v", err)
	}
	return u, nil
}

// validateUserInfo checks if the specified userinfo is valid.
func validateUserInfo(uInfo types.UserInfo) (err error) {
	defer decorate.OnError(&err, "provided userinfo is invalid")

	// Validate username. We don't want to check here if it matches the username from the request, because it's the
	// broker's responsibility to do that and we don't know which usernames the provider considers equal, for example if
	// they are case-sensitive or not.
	if uInfo.Name == "" {
		return errors.New("empty username")
	}

	// Validate home and shell directories
	if !filepath.IsAbs(filepath.Clean(uInfo.Dir)) {
		return fmt.Errorf("value provided for homedir is not an absolute path: %s", uInfo.Dir)
	}
	if !filepath.IsAbs(filepath.Clean(uInfo.Shell)) {
		return fmt.Errorf("value provided for shell is not an absolute path: %s", uInfo.Shell)
	}

	// Validate groups
	for _, g := range uInfo.Groups {
		if g.Name == "" {
			return errors.New("group has empty name")
		}
	}

	return nil
}

// unmarshalAndGetKey tries to unmarshal the content in data and returns the value of the requested key.
func unmarshalAndGetKey(data, key string) (json.RawMessage, error) {
	var returnedData map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &returnedData); err != nil {
		return nil, fmt.Errorf("response returned by the broker is not a valid json: %v\nBroker returned: %v", err, data)
	}

	rawMsg, ok := returnedData[key]
	if !ok {
		return nil, fmt.Errorf("missing key %q in returned message, got: %v", key, data)
	}

	return rawMsg, nil
}
