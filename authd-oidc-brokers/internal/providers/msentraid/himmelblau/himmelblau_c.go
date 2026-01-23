//go:build withmsentraid

package himmelblau

//go:generate ./generate.sh

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
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/ubuntu/authd/log"
)

// Entra AADSTS error codes as defined in
// https://learn.microsoft.com/en-us/entra/identity-platform/reference-error-codes
const (
	// AADSTS135011 Device used during the authentication is disabled.
	deviceDisabledErrorCode = 135011
	// AADSTS50011 InvalidReplyTo - The reply address is missing, misconfigured,
	// or doesn't match reply addresses configured for the app. As a resolution
	// ensures to add this missing reply address to the Microsoft Entra
	// application or have someone with the permissions to manage your
	// application in Microsoft Entra IF do this for you. To learn more, see the
	// troubleshooting article for error AADSTS50011.
	invalidRedirectURIErrorCode = 50011
)

type boxedDynTPM C.BoxedDynTpm
type brokerClientApplication C.BrokerClientApplication

func setTracingFilter(filter string) error {
	if msalErr := C.set_module_tracing_filter(C.CString(filter)); msalErr != nil {
		return fmt.Errorf("failed to set libhimmelblau tracing filter: %v", C.GoString(msalErr.msg))
	}

	return nil
}

func initTPM(tctiName string) (tpm *boxedDynTPM, err error) {
	var cTctiName *C.char
	if tctiName != "" {
		cTctiName = C.CString(tctiName)
		defer C.free(unsafe.Pointer(cTctiName))
	}

	if msalErr := C.tpm_init(cTctiName, (**C.BoxedDynTpm)(unsafe.Pointer(&tpm))); msalErr != nil {
		return nil, fmt.Errorf("failed to initialize TPM: %v", C.GoString(msalErr.msg))
	}

	return tpm, nil
}

func initBroker(authority, clientID string, transportKeyBytes, certKeyBytes []byte) (broker *brokerClientApplication, err error) {
	cAuthority := C.CString(authority)
	defer C.free(unsafe.Pointer(cAuthority))

	var cClientID *C.char
	if clientID != "" {
		cClientID = C.CString(clientID)
		defer C.free(unsafe.Pointer(cClientID))
	}

	var cTransportKey *C.LoadableMsOapxbcRsaKey
	if len(transportKeyBytes) > 0 {
		msalErr := C.deserialize_loadable_ms_oapxbc_rsa_key(
			(*C.uint8_t)(unsafe.Pointer(&transportKeyBytes[0])),
			C.size_t(len(transportKeyBytes)),
			&cTransportKey,
		)
		if msalErr != nil {
			return nil, fmt.Errorf("failed to deserialize transport key: %v", C.GoString(msalErr.msg))
		}
		defer C.loadable_ms_oapxbc_rsa_key_free(cTransportKey)
	}

	var cCertKey *C.LoadableMsDeviceEnrolmentKey
	if len(certKeyBytes) > 0 {
		msalErr := C.deserialize_loadable_ms_device_enrolment_key(
			(*C.uint8_t)(unsafe.Pointer(&certKeyBytes[0])),
			C.size_t(len(certKeyBytes)),
			&cCertKey,
		)
		if msalErr != nil {
			return nil, fmt.Errorf("failed to deserialize cert key: %v", C.GoString(msalErr.msg))
		}
		defer C.loadable_ms_device_enrollment_key_free(cCertKey)
	}

	msalErr := C.broker_init(
		cAuthority,
		cClientID,
		cTransportKey,
		cCertKey,
		(**C.BrokerClientApplication)(unsafe.Pointer(&broker)),
	)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to initialize broker client: %v", C.GoString(msalErr.msg))
	}

	return broker, nil
}

func initEnrollAttrs(domain, hostname, osVersion string) (attrs *C.EnrollAttrs, err error) {
	cDomain := C.CString(domain)
	defer C.free(unsafe.Pointer(cDomain))
	cHostname := C.CString(hostname)
	defer C.free(unsafe.Pointer(cHostname))
	cOSVersion := C.CString(osVersion)
	defer C.free(unsafe.Pointer(cOSVersion))

	msalErr := C.enroll_attrs_init(
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

	// TODO: Do we not have to free the attrs?

	return attrs, nil
}

func generateAuthValue() (authValue string, err error) {
	var cAuthValue *C.char
	if msalErr := C.auth_value_generate(&cAuthValue); msalErr != nil {
		return "", fmt.Errorf("failed to generate auth value: %v", C.GoString(msalErr.msg))
	}
	defer C.free(unsafe.Pointer(cAuthValue))

	return C.GoString(cAuthValue), nil
}

func createTPMMachineKey(tpm *boxedDynTPM, authValue string) (key *C.LoadableMachineKey, cleanup func(), err error) {
	cAuthValue := C.CString(authValue)
	defer C.free(unsafe.Pointer(cAuthValue))

	var loadableMachineKey *C.LoadableMachineKey
	msalErr := C.tpm_machine_key_create((*C.BoxedDynTpm)(unsafe.Pointer(tpm)), cAuthValue, &loadableMachineKey)
	if msalErr != nil {
		return nil, nil, fmt.Errorf("failed to create loadable machine key: %v", C.GoString(msalErr.msg))
	}

	cleanup = func() { C.loadable_machine_key_free(loadableMachineKey) }

	return loadableMachineKey, cleanup, nil
}

func loadTPMMachineKey(tpm *boxedDynTPM, authValue string, loadableMachineKey *C.LoadableMachineKey) (key *C.MachineKey, cleanup func(), err error) {
	cAuthValue := C.CString(authValue)
	defer C.free(unsafe.Pointer(cAuthValue))

	if msalErr := C.tpm_machine_key_load((*C.BoxedDynTpm)(unsafe.Pointer(tpm)), cAuthValue, loadableMachineKey, &key); msalErr != nil {
		return nil, nil, fmt.Errorf("failed to load TPM machine key: %v", C.GoString(msalErr.msg))
	}

	cleanup = func() { C.machine_key_free(key) }

	return key, cleanup, nil
}

func enrollDevice(broker *brokerClientApplication, refreshToken string, attrs *C.EnrollAttrs, tpm *boxedDynTPM, machineKey *C.MachineKey) (data *DeviceRegistrationData, err error) {
	cRefreshToken := C.CString(refreshToken)
	defer C.free(unsafe.Pointer(cRefreshToken))

	var cTransportKey *C.LoadableMsOapxbcRsaKey
	var cCertKey *C.LoadableMsDeviceEnrolmentKey
	var cDeviceID *C.char

	msalErr := C.broker_enroll_device(
		(*C.BrokerClientApplication)(unsafe.Pointer(broker)),
		cRefreshToken,
		attrs,
		(*C.BoxedDynTpm)(unsafe.Pointer(tpm)),
		machineKey,
		&cTransportKey,
		&cCertKey,
		&cDeviceID,
	)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to enroll device: %v", C.GoString(msalErr.msg))
	}
	defer C.loadable_ms_oapxbc_rsa_key_free(cTransportKey)
	defer C.loadable_ms_device_enrollment_key_free(cCertKey)
	defer C.free(unsafe.Pointer(cDeviceID))

	deviceID := C.GoString(cDeviceID)

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

	return &DeviceRegistrationData{
		DeviceID:     deviceID,
		CertKey:      certKey,
		TransportKey: transportKey,
	}, nil
}

func serializeLoadableMachineKey(loadableMachineKey *C.LoadableMachineKey) (key []byte, err error) {
	var cSerializedKey *C.char
	var cSerializedKeyLen C.size_t
	defer C.free(unsafe.Pointer(cSerializedKey))
	msalErr := C.serialize_loadable_machine_key(loadableMachineKey, &cSerializedKey, &cSerializedKeyLen)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to serialize loadable machine key: %v", C.GoString(msalErr.msg))
	}
	if cSerializedKeyLen > 0 {
		key = C.GoBytes(unsafe.Pointer(cSerializedKey), C.int(cSerializedKeyLen))
	}

	return key, nil
}

func deserializeLoadableMachineKey(key []byte) (loadableMachineKey *C.LoadableMachineKey, cleanup func(), err error) {
	msalErr := C.deserialize_loadable_machine_key(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		C.size_t(len(key)),
		&loadableMachineKey,
	)
	if msalErr != nil {
		return nil, nil, fmt.Errorf("failed to deserialize loadable machine key: %v", C.GoString(msalErr.msg))
	}

	cleanup = func() { C.loadable_machine_key_free(loadableMachineKey) }

	return loadableMachineKey, cleanup, nil
}

func acquireTokenByRefreshToken(broker *brokerClientApplication, refreshToken string, scopes []string, requestResource string, clientID string, tpm *boxedDynTPM, machineKey *C.MachineKey) (token *C.UserToken, cleanup func(), err error) {
	cRefreshToken := C.CString(refreshToken)
	defer C.free(unsafe.Pointer(cRefreshToken))

	var cScopes []*C.char
	for _, scope := range scopes {
		cScope := C.CString(scope)
		cScopes = append(cScopes, cScope)
		defer C.free(unsafe.Pointer(cScope))
	}

	var cRequestResource *C.char
	if requestResource != "" {
		cRequestResource = C.CString(requestResource)
		defer C.free(unsafe.Pointer(cRequestResource))
	}

	var cClientID *C.char
	if clientID != "" {
		cClientID = C.CString(clientID)
		defer C.free(unsafe.Pointer(cClientID))
	}

	var userToken *C.UserToken

	msalErr := C.broker_acquire_token_by_refresh_token(
		(*C.BrokerClientApplication)(unsafe.Pointer(broker)),
		cRefreshToken,
		&cScopes[0],
		C.int(len(scopes)),
		cRequestResource,
		// We could use `nil` here instead of the client ID if we also use `nil` as the client ID
		// in the `broker_init` call, which means that the user doesn't even have to register
		// an OIDC app in Entra. However, that has the effect that we can't fetch the groups
		// of the user.
		cClientID,
		(*C.BoxedDynTpm)(unsafe.Pointer(tpm)),
		machineKey,
		&userToken,
	)
	if msalErr != nil {
		// Error codes can be returned by libhimmelblau as a single code in the aadsts_code field or
		// as a list of error codes in the acquire_token_error_codes field.
		errorCodes := []C.uint32_t{msalErr.aadsts_code}
		if msalErr.acquire_token_error_codes != nil && msalErr.acquire_token_error_codes_len > 0 {
			errorCodes = unsafe.Slice(msalErr.acquire_token_error_codes, msalErr.acquire_token_error_codes_len)
		}

		for _, errorCode := range errorCodes {
			errorCodeStr := strconv.Itoa(int(errorCode))
			switch {
			// AADSTS error codes can have additional digits or subcodes appended
			// (e.g. AADSTS500113 as a variation of AADSTS50011).
			// Checking the prefix ensures we catch all variations of the base error code.
			case strings.HasPrefix(errorCodeStr, strconv.Itoa(deviceDisabledErrorCode)):
				log.Error(context.Background(), C.GoString(msalErr.msg))
				return nil, nil, ErrDeviceDisabled
			case strings.HasPrefix(errorCodeStr, strconv.Itoa(invalidRedirectURIErrorCode)):
				log.Errorf(context.Background(), "Token acquisition failed: %v", C.GoString(msalErr.msg))
				return nil, nil, ErrInvalidRedirectURI
			}
		}

		// The token acquisition failed unexpectedly.
		// One possible reason is that the device was deleted by an administrator in Entra ID.
		// Unfortunately, Microsoft doesn't return a specific error code for that case,
		// it returns the generic error "AADSTS50155: Device authentication failed".
		return nil, nil, TokenAcquisitionError{msg: fmt.Sprintf("error acquiring access token using refresh token: %v", C.GoString(msalErr.msg))}
	}

	cleanup = func() { C.user_token_free(userToken) }

	return userToken, cleanup, nil
}

func accessTokenFromUserToken(userToken *C.UserToken) (accessToken string, err error) {
	var cAccessToken *C.char
	msalErr := C.user_token_access_token(userToken, &cAccessToken)
	if msalErr != nil {
		return "", fmt.Errorf("failed to get access token: %v", C.GoString(msalErr.msg))
	}
	defer C.free(unsafe.Pointer(cAccessToken))

	return C.GoString(cAccessToken), nil
}
