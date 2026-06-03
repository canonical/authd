//go:build withmsentraid

// Package himmelblau provides functions to use the libhimmelblau library
package himmelblau

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/canonical/authd/log"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

var (
	tpm         *boxedDynTPM
	tpmInitOnce sync.Once
	//nolint:errname // This is not a sentinel error.
	tpmInitErr error

	brokerClientApps   = make(map[brokerClientAppCacheKey]*brokerClientAppEntry)
	brokerClientAppsMu sync.Mutex

	authorityBaseURL   = "https://login.microsoftonline.com"
	authorityBaseURLMu sync.RWMutex

	deviceRegistrationMu sync.RWMutex
)

type brokerClientAppCacheKey struct {
	authority        string
	clientID         string
	transportKeyHash string
	certKeyHash      string
}

// brokerClientAppEntry is a cache slot for a broker client app. The once gate
// ensures initBroker runs only once per key while letting unrelated keys
// initialize concurrently (the global mutex is held only for map access, not
// across the cgo call, which performs TPM and network work).
type brokerClientAppEntry struct {
	once sync.Once
	app  *brokerClientApplication
	err  error
}

func ensureTPMInitialized() error {
	tpmInitOnce.Do(func() {
		filters := []string{"warn"}
		logLevel := log.GetLevel()
		if logLevel <= log.DebugLevel {
			log.Debug(context.Background(), "Setting libhimmelblau tracing level to DEBUG")
			filters = append(filters, "himmelblau=debug")
		} else if logLevel <= log.InfoLevel {
			filters = append(filters, "himmelblau=info")
		}

		if tpmInitErr = setTracingFilter(strings.Join(filters, ",")); tpmInitErr != nil {
			return
		}

		// An optional TPM Transmission Interface. If this parameter is empty, a soft TPM is initialized.
		var tctiName string
		tpm, tpmInitErr = initTPM(tctiName)
		if tpmInitErr != nil {
			return
		}
	})

	return tpmInitErr
}

func brokerClientAppFor(clientID, tenantID string, data *DeviceRegistrationData) (*brokerClientApplication, error) {
	if err := ensureTPMInitialized(); err != nil {
		return nil, err
	}

	authorityBaseURLMu.RLock()
	authority, err := url.JoinPath(authorityBaseURL, tenantID)
	authorityBaseURLMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("failed to construct authority URL: %v", err)
	}

	var transportKey []byte
	var certKey []byte
	if data != nil {
		transportKey = data.TransportKey
		certKey = data.CertKey
	}

	key := brokerClientAppCacheKey{
		authority:        authority,
		clientID:         clientID,
		transportKeyHash: hashCacheKeyBytes(transportKey),
		certKeyHash:      hashCacheKeyBytes(certKey),
	}

	brokerClientAppsMu.Lock()
	entry := brokerClientApps[key]
	if entry == nil {
		entry = &brokerClientAppEntry{}
		brokerClientApps[key] = entry
	}
	brokerClientAppsMu.Unlock()

	entry.once.Do(func() {
		entry.app, entry.err = initBroker(authority, clientID, transportKey, certKey)
	})
	if entry.err != nil {
		// Do not cache failures: drop the entry so a later call can retry.
		brokerClientAppsMu.Lock()
		if brokerClientApps[key] == entry {
			delete(brokerClientApps, key)
		}
		brokerClientAppsMu.Unlock()
		return nil, entry.err
	}

	return entry.app, nil
}

func hashCacheKeyBytes(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func tokenExtrasFromAccessToken(ctx context.Context, accessToken string) map[string]any {
	parsedToken, _, err := new(jwt.Parser).ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		log.Debugf(ctx, "Could not parse access token claims: %v", err)
		return nil
	}

	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		log.Debug(ctx, "Could not cast access token claims to jwt.MapClaims")
		return nil
	}

	extras := map[string]any{}
	if preferredUsername, ok := claims["preferred_username"].(string); ok && preferredUsername != "" {
		extras["preferred_username"] = preferredUsername
	} else if upn, ok := claims["upn"].(string); ok && upn != "" {
		extras["preferred_username"] = upn
	}
	if sub, ok := claims["sub"].(string); ok && sub != "" {
		extras["sub"] = sub
	}
	if name, ok := claims["name"].(string); ok && name != "" {
		extras["name"] = name
	}
	if scp, ok := claims["scp"].(string); ok && scp != "" {
		extras["scp"] = scp
		extras["scope"] = scp
	}

	if len(extras) == 0 {
		return nil
	}

	return extras
}

// RegisterDevice registers the device with Microsoft Entra ID and returns the
// device registration data required for subsequent access token acquisition via
// AcquireAccessTokenForGraphAPI.
//
// The returned cleanup function must be called after AcquireAccessTokenForGraphAPI
// or if that function will not be called, to release an internal mutex and allow
// future device registrations.
//
// RegisterDevice is thread-safe due to an internal mutex that serializes access
// to libhimmelblau, which modifies shared state during registration.
func RegisterDevice(
	ctx context.Context,
	token *oauth2.Token,
	tenantID string,
	domain string,
) (registrationData *DeviceRegistrationData, cleanup func(), err error) {
	deviceRegistrationMu.Lock()
	// libhimmelblau modifies BrokerClientApplication.cert_key during registration.
	// This key is reused in later calls, including acquire_token_by_refresh_token.
	// If cert_key changes because another device registration was done concurrently,
	// libhimmelblau returns "TPM error: Failed to load IdentityKey: Aes256GcmDecrypt".
	// The mutex also prevents concurrent modifications to TPM state.
	unlock := deviceRegistrationMu.Unlock

	// Ensure that the mutex is unlocked if an error occurs.
	// We can't rename `unlock` to `cleanup` because `return nil, nil, err` sets
	// the return value `cleanup` to `nil`, so calling `cleanup()` would panic.
	defer func() {
		if err != nil {
			unlock()
		}
	}()

	brokerClientApp, err := brokerClientAppFor("", tenantID, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	authValue, err := generateAuthValue()
	if err != nil {
		return nil, nil, err
	}

	loadableMachineKey, tpmCleanup, err := createTPMMachineKey(tpm, authValue)
	if err != nil {
		return nil, nil, err
	}
	defer tpmCleanup()

	attrs, err := initEnrollAttrs(domain, hostname(), OSVersion())
	if err != nil {
		return nil, nil, err
	}

	machineKey, tpmCleanup, err := loadTPMMachineKey(tpm, authValue, loadableMachineKey)
	if err != nil {
		return nil, nil, err
	}
	defer tpmCleanup()

	data, err := enrollDevice(brokerClientApp, token.RefreshToken, attrs, tpm, machineKey)
	if err != nil {
		return nil, nil, err
	}

	log.Infof(ctx, "Enrolled device with ID: %v", data.DeviceID)

	data.TPMMachineKey, err = serializeLoadableMachineKey(loadableMachineKey)
	if err != nil {
		return nil, nil, err
	}

	data.AuthValue = authValue

	return data, unlock, nil
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		log.Warningf(context.Background(), "Failed to get hostname: %v", err)
		return "unknown"
	}
	return name
}

// OSVersion gets the pretty name of the OS release from the system.
// Since we're running in a snap, this returns the version of the core base snap
// (which is not that helpful when it's shown as the device's OS in Entra, so
// might want to change this in the future, to somehow get the host's OS version).
var OSVersion = sync.OnceValue(func() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		log.Warningf(context.Background(), "Failed to read /etc/os-release: %v", err)
		return "unknown"
	}

	for _, line := range strings.Split(string(data), "\n") {
		if name, found := strings.CutPrefix(line, "PRETTY_NAME="); found && name != "" {
			return name
		}
	}

	log.Warningf(context.Background(), "PRETTY_NAME not found in /etc/os-release")
	return "unknown"
})

// AcquireAccessTokenForGraphAPI uses the refresh token from the provided
// OAuth 2.0 token with the required scopes to access the Microsoft Graph API.
func AcquireAccessTokenForGraphAPI(
	ctx context.Context,
	clientID string,
	tenantID string,
	token *oauth2.Token,
	data DeviceRegistrationData,
) (string, error) {
	// Pass an empty client ID to broker_init: there it only sets the *default*
	// on_behalf_of client ID, which we always override per-call below in
	// acquireTokenByRefreshToken. Passing the real client ID here would have no
	// effect on the resulting token and would only force a redundant broker_init
	// for a separate cache key (device registration initializes the broker app
	// with an empty client ID).
	brokerClientApp, err := brokerClientAppFor("", tenantID, &data)
	if err != nil {
		return "", fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	loadableMachineKey, cleanup, err := deserializeLoadableMachineKey(data.TPMMachineKey)
	if err != nil {
		return "", err
	}
	defer cleanup()

	machineKey, cleanup, err := loadTPMMachineKey(tpm, data.AuthValue, loadableMachineKey)
	if err != nil {
		return "", err
	}
	defer cleanup()

	userToken, cleanup, err := acquireTokenByRefreshToken(
		brokerClientApp,
		token.RefreshToken,
		[]string{"GroupMember.Read.All"},
		"",
		// Acquire the token on behalf of the user's OIDC app. This is what makes
		// the user's groups resolvable; without a client ID here (and without an
		// OIDC app registered in Entra) the group claims are unavailable. It is
		// passed per-call rather than via broker_init because the per-call value
		// takes precedence over the broker app's default on_behalf_of client ID.
		clientID,
		tpm,
		machineKey,
	)
	if err != nil {
		return "", err
	}
	defer cleanup()

	accessToken, err := accessTokenFromUserToken(userToken)
	if err != nil {
		return "", err
	}
	log.Info(ctx, "Acquired access token")

	return accessToken, nil
}

// InitiateMFAFlowWithPassword starts the password+MFA flow for a user.
// It submits the user's credentials to Entra ID and returns an MFAFlowState
// that can be used to complete the MFA challenge.
// When withDeviceScope is true, the MFA flow requests scopes required for device
// enrollment. When false, it uses standard scopes without enrollment resources.
func InitiateMFAFlowWithPassword(ctx context.Context, clientID, tenantID string, data *DeviceRegistrationData, username, password string, withDeviceScope bool) (*MFAFlowState, *MFAChallengeInfo, error) {
	brokerClientApp, err := brokerClientAppFor(clientID, tenantID, data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	log.Debugf(ctx, "Initiating MFA flow for user %q (withDeviceScope=%v)", username, withDeviceScope)
	var flow *MFAFlowState
	if withDeviceScope {
		flow, err = initiateMFAFlowForEnrollment(brokerClientApp, username, password)
	} else {
		flow, err = initiateMFAFlow(brokerClientApp, username, password)
	}
	if err != nil {
		return nil, nil, err
	}

	msg, err := mfaFlowMessage(flow)
	if err != nil {
		FreeMFAFlowState(flow)
		return nil, nil, err
	}

	method, err := mfaFlowMethod(flow)
	if err != nil {
		FreeMFAFlowState(flow)
		return nil, nil, err
	}

	challengeInfo := &MFAChallengeInfo{
		Message:           msg,
		Method:            method,
		PollingIntervalMs: mfaFlowPollingInterval(flow),
		MaxPollAttempts:   mfaFlowMaxPollAttempts(flow),
	}

	return flow, challengeInfo, nil
}

// AcquireTokenByMFAFlow completes the MFA challenge (poll or code submission).
// For poll-based MFA, pass empty authData and increment pollAttempt.
// For code-based MFA, pass the code as authData with pollAttempt=0.
// Returns an OAuth token containing the access and refresh tokens from the MFA result.
func AcquireTokenByMFAFlow(ctx context.Context, clientID, tenantID string, data *DeviceRegistrationData, username string, flow *MFAFlowState, authData string, pollAttempt int) (*oauth2.Token, error) {
	brokerClientApp, err := brokerClientAppFor(clientID, tenantID, data)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	log.Debugf(ctx, "Acquiring token by MFA flow for user %q (poll_attempt=%d)", username, pollAttempt)
	userToken, cleanup, err := acquireTokenByMFAFlow(brokerClientApp, username, flow, authData, pollAttempt)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	refreshToken, err := refreshTokenFromUserToken(userToken)
	if err != nil {
		return nil, fmt.Errorf("failed to extract refresh token from MFA result: %v", err)
	}

	accessToken, err := accessTokenFromUserToken(userToken)
	if err != nil {
		return nil, fmt.Errorf("failed to extract access token from MFA result: %v", err)
	}

	// The access token from the native MFA flow is issued for the Entra native API
	// and cannot be used with the standard OIDC UserInfo endpoint (different audience).
	// Include the user's SPN (preferred_username) and UUID (sub) as token extras so that
	// finishEntraAuth can recover user info without calling the UserInfo endpoint.
	extras := map[string]interface{}{}
	if spn, spnErr := spnFromUserToken(userToken); spnErr == nil && spn != "" {
		extras["preferred_username"] = spn
		log.Debugf(ctx, "MFA token SPN: %q", spn)
	} else if spnErr != nil {
		log.Debugf(ctx, "Could not get SPN from MFA token: %v", spnErr)
	}
	if sub, subErr := uuidFromUserToken(userToken); subErr == nil && sub != "" {
		extras["sub"] = sub
	} else if subErr != nil {
		log.Debugf(ctx, "Could not get UUID from MFA token: %v", subErr)
	}

	// The Entra password flow returns an access token rather than an OIDC
	// id_token, so recover the display name and any missing identity claims
	// (name, scp, plus a preferred_username/sub fallback) from the access
	// token JWT, which carries the "name" claim in every flow we use. The
	// SPN/UUID extras set above take priority over duplicates.
	if accessExtras := tokenExtrasFromAccessToken(ctx, accessToken); len(accessExtras) > 0 {
		for k, v := range accessExtras {
			if _, alreadySet := extras[k]; !alreadySet {
				extras[k] = v
			}
		}
	}

	t := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	}
	if len(extras) > 0 {
		return t.WithExtra(extras), nil
	}
	return t, nil
}
