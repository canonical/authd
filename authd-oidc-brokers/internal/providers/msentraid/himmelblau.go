package msentraid

//go:generate ./generate-himmelblau.sh

/*
#cgo LDFLAGS: -L${SRCDIR} -lhimmelblau
// Add the current directory to the library search path if we're building for testing,
// because libhimmelblau is not installed in the standard search directories.
#cgo !release LDFLAGS: -Wl,-rpath,${SRCDIR}
#include "himmelblau.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"github.com/ubuntu/authd/log"
	"golang.org/x/oauth2"
)

const deviceDisabledErrorCode = 135011

var (
	tpm         *C.BoxedDynTpm
	tpmInitOnce sync.Once

	brokerClientApp         *C.BrokerClientApplication
	brokerClientAppInitOnce sync.Once

	// ErrDeviceDisabled is returned when the device is disabled in Microsoft Entra ID.
	ErrDeviceDisabled = fmt.Errorf("device is disabled in Microsoft Entra ID")

	authorityBaseURL = "https://login.microsoftonline.com"
)

// TokenAcquisitionError is returned when an error occurs while acquiring a token via libhimmelblau.
type TokenAcquisitionError struct {
	msg string
}

func (e TokenAcquisitionError) Error() string {
	return e.msg
}

func ensureTPMInitialized() (err error) {
	tpmInitOnce.Do(func() {
		filters := []string{"warn"}
		logLevel := log.GetLevel()
		if logLevel <= log.DebugLevel {
			log.Debug(context.Background(), "Setting libhimmelblau tracing level to DEBUG")
			filters = append(filters, "himmelblau=debug")
		} else if logLevel <= log.InfoLevel {
			filters = append(filters, "himmelblau=info")
		}

		msalErr := C.set_module_tracing_filter(C.CString(strings.Join(filters, ",")))
		if msalErr != nil {
			err = fmt.Errorf("failed to set libhimmelblau tracing level: %v", C.GoString(msalErr.msg))
			return
		}

		// An optional TPM Transmission Interface. If this parameter is NULL, a soft TPM is initialized.
		var tctiName *C.char
		msalErr = C.tpm_init(tctiName, &tpm)
		if msalErr != nil {
			err = fmt.Errorf("failed to initialize TPM: %v", C.GoString(msalErr.msg))
			return
		}
	})

	return err
}

func ensureBrokerClientAppInitialized(tenantID string, data *deviceRegistrationData) (err error) {
	if err = ensureTPMInitialized(); err != nil {
		return err
	}

	brokerClientAppInitOnce.Do(func() {
		var msalErr *C.MSAL_ERROR

		authority := C.CString(authorityBaseURL + "/" + tenantID)
		defer C.free(unsafe.Pointer(authority))

		cCertKey := (*C.LoadableMsDeviceEnrolmentKey)(nil)
		if data != nil && len(data.CertKey) > 0 {
			msalErr := C.deserialize_loadable_ms_device_enrolment_key(
				(*C.uint8_t)(unsafe.Pointer(&data.CertKey[0])),
				C.size_t(len(data.CertKey)),
				&cCertKey,
			)
			if msalErr != nil {
				err = fmt.Errorf("failed to deserialize device enrollment key: %v", C.GoString(msalErr.msg))
				return
			}
		} else {
			cCertKey = nil // No cert key provided, will be generated during enrollment.
		}

		var cTransportKey *C.LoadableMsOapxbcRsaKey
		if data != nil && len(data.TransportKey) > 0 {
			msalErr = C.deserialize_loadable_ms_oapxbc_rsa_key(
				(*C.uint8_t)(unsafe.Pointer(&data.TransportKey[0])),
				C.size_t(len(data.TransportKey)),
				&cTransportKey,
			)
			if msalErr != nil {
				err = fmt.Errorf("failed to deserialize transport key: %v", C.GoString(msalErr.msg))
				return
			}
		} else {
			cTransportKey = nil // No transport key provided, will be generated during enrollment.
		}

		msalErr = C.broker_init(
			authority,
			nil, /* client_id */
			cTransportKey,
			cCertKey,
			&brokerClientApp,
		)
		if msalErr != nil {
			err = fmt.Errorf("failed to initialize BrokerClientApplication: %v", C.GoString(msalErr.msg))
			return
		}
	})

	return err
}

type deviceRegistrationData struct {
	DeviceID      string `json:"device_id"`
	CertKey       []byte `json:"cert_key"`
	TransportKey  []byte `json:"transport_key"`
	AuthValue     string `json:"auth_value"`
	TPMMachineKey []byte `json:"tpm_machine_key"`
}

func (d *deviceRegistrationData) IsValid() bool {
	return d.DeviceID != "" &&
		d.CertKey != nil &&
		d.TransportKey != nil &&
		d.AuthValue != "" &&
		d.TPMMachineKey != nil
}

// registerDevice registers the device with Microsoft Entra ID.
// It returns the device registration data as JSON.
func (p *Provider) registerDevice(ctx context.Context, token *oauth2.Token, tenantID, domain string) ([]byte, error) {
	if err := ensureBrokerClientAppInitialized(tenantID, nil); err != nil {
		return nil, fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	var cAuthValue *C.char
	msalErr := C.auth_value_generate(&cAuthValue)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to generate auth value: %v", C.GoString(msalErr.msg))
	}

	var loadableMachineKey *C.LoadableMachineKey
	msalErr = C.tpm_machine_key_create(tpm, cAuthValue, &loadableMachineKey)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to create loadable machine key: %v", C.GoString(msalErr.msg))
	}

	var attrs *C.EnrollAttrs
	cDomain := C.CString(domain)
	defer C.free(unsafe.Pointer(cDomain))
	cHostname := C.CString(hostname())
	defer C.free(unsafe.Pointer(cHostname))
	cOSVersion := C.CString(OSVersion())
	defer C.free(unsafe.Pointer(cOSVersion))

	msalErr = C.enroll_attrs_init(
		cDomain,
		cHostname,
		nil, /* device_type - default is "Linux" */
		0,   /* join_type - 0: Azure AD join */
		cOSVersion,
		&attrs,
	)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to initialize enroll attributes: %v", C.GoString(msalErr.msg))
	}

	var machineKey *C.MachineKey
	msalErr = C.tpm_machine_key_load(tpm, cAuthValue, loadableMachineKey, &machineKey)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to load TPM machine key: %v", C.GoString(msalErr.msg))
	}

	var cTransportKey *C.LoadableMsOapxbcRsaKey
	defer C.loadable_ms_oapxbc_rsa_key_free(cTransportKey)
	var cCertKey *C.LoadableMsDeviceEnrolmentKey
	defer C.loadable_ms_device_enrollment_key_free(cCertKey)
	var cDeviceID *C.char
	defer C.free(unsafe.Pointer(cDeviceID))
	cRefreshToken := C.CString(token.RefreshToken)
	defer C.free(unsafe.Pointer(cRefreshToken))

	msalErr = C.broker_enroll_device(
		brokerClientApp,
		cRefreshToken,
		attrs,
		tpm,
		machineKey,
		&cTransportKey,
		&cCertKey,
		&cDeviceID,
	)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to enroll device: %v", C.GoString(msalErr.msg))
	}

	deviceID := C.GoString(cDeviceID)
	log.Infof(ctx, "Enrolled device with ID: %v", deviceID)

	var certKey []byte
	var cSerializedCertKey *C.char
	var cSerializedCertKeyLen C.size_t
	defer C.free(unsafe.Pointer(cSerializedCertKey))
	msalErr = C.serialize_loadable_ms_device_enrolment_key(cCertKey, &cSerializedCertKey, &cSerializedCertKeyLen)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to serialize device enrollment key: %v", C.GoString(msalErr.msg))
	}
	if cSerializedCertKeyLen > 0 {
		certKey = C.GoBytes(unsafe.Pointer(cSerializedCertKey), C.int(cSerializedCertKeyLen))
	}

	var transportKey []byte
	var cSerializedTransportKey *C.char
	var cSerializedTransportKeyLen C.size_t
	defer C.free(unsafe.Pointer(cSerializedTransportKey))
	msalErr = C.serialize_loadable_ms_oapxbc_rsa_key(cTransportKey, &cSerializedTransportKey, &cSerializedTransportKeyLen)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to serialize transport key: %v", C.GoString(msalErr.msg))
	}
	if cSerializedTransportKeyLen > 0 {
		transportKey = C.GoBytes(unsafe.Pointer(cSerializedTransportKey), C.int(cSerializedTransportKeyLen))
	}

	var tpmMachineKey []byte
	var cSerializedTpmMachineKey *C.char
	var cSerializedTpmMachineKeyLen C.size_t
	defer C.free(unsafe.Pointer(cSerializedTpmMachineKey))
	msalErr = C.serialize_loadable_machine_key(loadableMachineKey, &cSerializedTpmMachineKey, &cSerializedTpmMachineKeyLen)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to serialize TPM machine key: %v", C.GoString(msalErr.msg))
	}
	if cSerializedTpmMachineKeyLen > 0 {
		tpmMachineKey = C.GoBytes(unsafe.Pointer(cSerializedTpmMachineKey), C.int(cSerializedTpmMachineKeyLen))
	}

	jsonData, err := json.Marshal(deviceRegistrationData{
		DeviceID:      deviceID,
		CertKey:       certKey,
		TransportKey:  transportKey,
		AuthValue:     C.GoString(cAuthValue),
		TPMMachineKey: tpmMachineKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal device registration data: %v", err)
	}

	return jsonData, nil
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
func OSVersion() string {
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
}

// acquireAccessTokenForGraphAPI uses the refresh token from the provided
// OAuth 2.0 token with the required scopes to access the Microsoft Graph API.
func acquireAccessTokenForGraphAPI(ctx context.Context, clientID, tenantID string, token *oauth2.Token, jsonData []byte) (string, error) {
	var data deviceRegistrationData
	err := json.Unmarshal(jsonData, &data)
	if err != nil {
		log.Noticef(ctx, "Device registration JSON data: %v", string(jsonData))
		return "", fmt.Errorf("failed to unmarshal device registration data: %v", err)
	}

	if err := ensureBrokerClientAppInitialized(tenantID, &data); err != nil {
		return "", fmt.Errorf("failed to initialize broker client application: %v", err)
	}

	var loadableMachineKey *C.LoadableMachineKey
	msalErr := C.deserialize_loadable_machine_key(
		(*C.uint8_t)(unsafe.Pointer(&data.TPMMachineKey[0])),
		C.size_t(len(data.TPMMachineKey)),
		&loadableMachineKey,
	)
	if msalErr != nil {
		return "", fmt.Errorf("failed to deserialize TPM machine key: %v", C.GoString(msalErr.msg))
	}

	var machineKey *C.MachineKey
	cAuthValue := C.CString(data.AuthValue)
	defer C.free(unsafe.Pointer(cAuthValue))
	msalErr = C.tpm_machine_key_load(tpm, cAuthValue, loadableMachineKey, &machineKey)
	if msalErr != nil {
		return "", fmt.Errorf("failed to load TPM machine key: %v", C.GoString(msalErr.msg))
	}

	var userToken *C.UserToken
	defer C.user_token_free(userToken)
	cRefreshToken := C.CString(token.RefreshToken)
	defer C.free(unsafe.Pointer(cRefreshToken))
	cGroupMemberReadAllScope := C.CString("GroupMember.Read.All")
	defer C.free(unsafe.Pointer(cGroupMemberReadAllScope))
	scopes := []*C.char{cGroupMemberReadAllScope}
	cClientID := C.CString(clientID)
	defer C.free(unsafe.Pointer(cClientID))
	msalErr = C.broker_acquire_token_by_refresh_token(
		brokerClientApp,
		cRefreshToken,
		&scopes[0],
		C.int(len(scopes)),
		nil, /* request_resource */
		// We could use `nil` here instead of the client ID if we also use `nil` as the client ID
		// in the `broker_init` call, which means that the user doesn't even have to register
		// an OIDC app in Entra. However, that has the effect that we can't fetch the groups
		// of the user.
		cClientID,
		tpm,
		machineKey,
		&userToken,
	)
	if msalErr != nil {
		var errorCodes []C.uint32_t
		if msalErr.acquire_token_error_codes != nil && msalErr.acquire_token_error_codes_len > 0 {
			errorCodes = unsafe.Slice(msalErr.acquire_token_error_codes, msalErr.acquire_token_error_codes_len)
		}
		if slices.Contains(errorCodes, deviceDisabledErrorCode) {
			log.Error(ctx, C.GoString(msalErr.msg))
			return "", ErrDeviceDisabled
		}
		// The token acquisition failed unexpectedly.
		// One possible reason is that the device was deleted by an administrator in Entra ID.
		// Unfortunately, Microsoft doesn't return a specific error code for that case,
		// it returns the generic error "AADSTS50155: Device authentication failed".
		return "", TokenAcquisitionError{msg: fmt.Sprintf("error acquiring access token using refresh token: %v", C.GoString(msalErr.msg))}
	}

	var accessToken *C.char
	defer C.free(unsafe.Pointer(accessToken))
	msalErr = C.user_token_access_token(userToken, &accessToken)
	if msalErr != nil {
		return "", fmt.Errorf("failed to get access token: %v", C.GoString(msalErr.msg))
	}

	log.Info(ctx, "Acquired access token")

	return C.GoString(accessToken), nil
}
