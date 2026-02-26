//go:build withmsentraid

// Package himmelblau provides functions to use the libhimmelblau library
package himmelblau

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/ubuntu/authd/log"
	"golang.org/x/oauth2"
)

var (
	tpm         *boxedDynTPM
	tpmInitOnce sync.Once
	//nolint:errname // This is not a sentinel error.
	tpmInitErr error

	brokerClientApp         *brokerClientApplication
	brokerClientAppInitOnce sync.Once
	//nolint:errname // This is not a sentinel error.
	brokerClientAppInitErr error

	authorityBaseURL   = "https://login.microsoftonline.com"
	authorityBaseURLMu sync.RWMutex

	deviceRegistrationMu sync.RWMutex
)

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

func ensureBrokerClientAppInitialized(tenantID string, data *DeviceRegistrationData) error {
	if err := ensureTPMInitialized(); err != nil {
		return err
	}

	brokerClientAppInitOnce.Do(func() {
		authorityBaseURLMu.RLock()
		authority, err := url.JoinPath(authorityBaseURL, tenantID)
		authorityBaseURLMu.RUnlock()
		if err != nil {
			brokerClientAppInitErr = fmt.Errorf("failed to construct authority URL: %v", err)
			return
		}
		var transportKey []byte
		var certKey []byte
		if data != nil {
			transportKey = data.TransportKey
			certKey = data.CertKey
		}

		brokerClientApp, brokerClientAppInitErr = initBroker(authority, "", transportKey, certKey)
		if brokerClientAppInitErr != nil {
			return
		}
	})

	return brokerClientAppInitErr
}

// DeviceRegistrationData contains the data returned by RegisterDevice
// which is needed to acquire an access token later.
type DeviceRegistrationData struct {
	DeviceID      string `json:"device_id"`
	CertKey       []byte `json:"cert_key"`
	TransportKey  []byte `json:"transport_key"`
	AuthValue     string `json:"auth_value"`
	TPMMachineKey []byte `json:"tpm_machine_key"`
}

// IsValid checks whether all fields of the DeviceRegistrationData are set.
func (d *DeviceRegistrationData) IsValid() bool {
	return d.DeviceID != "" &&
		d.CertKey != nil &&
		d.TransportKey != nil &&
		d.AuthValue != "" &&
		d.TPMMachineKey != nil
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

	if err := ensureBrokerClientAppInitialized(tenantID, nil); err != nil {
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
	if err := ensureBrokerClientAppInitialized(tenantID, &data); err != nil {
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
		// We could use `nil` here instead of the client ID if we also use `nil` as the client ID
		// in the `broker_init` call, which means that the user doesn't even have to register
		// an OIDC app in Entra. However, that has the effect that we can't fetch the groups
		// of the user.
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
