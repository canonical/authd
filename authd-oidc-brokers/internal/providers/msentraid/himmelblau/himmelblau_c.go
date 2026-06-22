//go:build withmsentraid

package himmelblau

//go:generate ./generate.sh

/*
// Define the feature macros that generate.sh enables when building the library
// (changepassword, on_behalf_of). cbindgen guards the corresponding enum
// variants and prototypes behind these macros, so cgo must define them to
// compile the header against the same ABI the shared library exposes. Omitting
// them drops the CHANGE_PASSWORD enum variant, which shifts every later
// MSAL_ERROR_CODE value down by one and misclassifies MFA error codes.
#cgo CFLAGS: -DCHANGEPASSWORD -DON_BEHALF_OF
#cgo LDFLAGS: -L${SRCDIR} -lhimmelblau
// Add the current directory to the library search path if we're building for testing,
// because libhimmelblau is not installed in the standard search directories.
#cgo !release LDFLAGS: -Wl,-rpath,${SRCDIR}
// libhimmelblau is built with the set_timeout feature, which makes cbindgen emit the
// timeout-aware, 6-argument broker_init under #if defined(SET_TIMEOUT) and the old
// 5-argument one under #if !defined(SET_TIMEOUT). Since initBroker calls the
// 6-argument variant, we must define SET_TIMEOUT so the header exposes it.
//
// The other features we enable (changepassword, on_behalf_of) don't need this:
// their guarded declarations are never referenced from cgo, so the header compiles
// fine whether or not their macros are defined.
#cgo CFLAGS: -DSET_TIMEOUT
#include "himmelblau.h"
*/
import "C"

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/canonical/authd/log"
)

// MSAL_ERROR_CODE values, derived from the cgo enum constants rather than
// hardcoded, so they always match the header the binding was compiled against.
//
// The enum values are NOT stable integers: several variants (e.g. CHANGE_PASSWORD)
// are gated behind cargo features, so the numeric value of later variants such as
// MFA_REQUIRED shifts depending on the feature set the library was built with
// (MFA_REQUIRED is 25 with the changepassword feature that generate.sh enables,
// not 24 — 24 is AUTH_CODE_RECEIVED). These are package vars (not a cgo import in
// the test) so the mapping can be unit-tested; test files cannot import cgo.
var (
	codeMFAPollContinue  = uint32(C.MFA_POLL_CONTINUE)
	codeMFARequired      = uint32(C.MFA_REQUIRED)
	codeAuthCodeReceived = uint32(C.AUTH_CODE_RECEIVED)
)

// mfaErrorCategory maps a libhimmelblau MSAL error code into an
// MFAErrorCategory so the broker can branch on outcomes without
// referencing the underlying numeric codes.
func mfaErrorCategory(code uint32) MFAErrorCategory {
	switch code {
	case codeMFAPollContinue:
		return MFAErrorPollContinue
	case codeMFARequired:
		return MFAErrorRequired
	}
	return MFAErrorOther
}

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
	// Do NOT free this C string: set_module_tracing_filter takes ownership of it
	// (the Rust side reclaims it via CString::from_raw and drops it), so freeing
	// it here would be a double free. The const char* in the header is misleading.
	if msalErr := C.set_module_tracing_filter(C.CString(filter)); msalErr != nil {
		return fmt.Errorf("failed to set libhimmelblau tracing filter: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to initialize TPM: %v", msalErrorMsg(msalErr))
	}

	return tpm, nil
}

// brokerHTTPTimeoutSecs extends libhimmelblau's 3-second default, which is too
// short for Entra device enrollment.
const brokerHTTPTimeoutSecs = 15

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
			return nil, fmt.Errorf("failed to deserialize transport key: %v", msalErrorMsg(msalErr))
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
			return nil, fmt.Errorf("failed to deserialize cert key: %v", msalErrorMsg(msalErr))
		}
		defer C.loadable_ms_device_enrollment_key_free(cCertKey)
	}

	cTimeoutSecs := C.uint64_t(brokerHTTPTimeoutSecs)

	msalErr := C.broker_init(
		cAuthority,
		cClientID,
		cTransportKey,
		cCertKey,
		&cTimeoutSecs,
		(**C.BrokerClientApplication)(unsafe.Pointer(&broker)),
	)
	if msalErr != nil {
		return nil, fmt.Errorf("failed to initialize broker client: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to initialize enroll attributes: %v", msalErrorMsg(msalErr))
	}

	// TODO: Do we not have to free the attrs?

	return attrs, nil
}

func generateAuthValue() (authValue string, err error) {
	var cAuthValue *C.char
	if msalErr := C.auth_value_generate(&cAuthValue); msalErr != nil {
		return "", fmt.Errorf("failed to generate auth value: %v", msalErrorMsg(msalErr))
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
		return nil, nil, fmt.Errorf("failed to create loadable machine key: %v", msalErrorMsg(msalErr))
	}

	cleanup = func() { C.loadable_machine_key_free(loadableMachineKey) }

	return loadableMachineKey, cleanup, nil
}

func loadTPMMachineKey(tpm *boxedDynTPM, authValue string, loadableMachineKey *C.LoadableMachineKey) (key *C.MachineKey, cleanup func(), err error) {
	cAuthValue := C.CString(authValue)
	defer C.free(unsafe.Pointer(cAuthValue))

	if msalErr := C.tpm_machine_key_load((*C.BoxedDynTpm)(unsafe.Pointer(tpm)), cAuthValue, loadableMachineKey, &key); msalErr != nil {
		return nil, nil, fmt.Errorf("failed to load TPM machine key: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to enroll device: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to serialize device enrollment key: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to serialize transport key: %v", msalErrorMsg(msalErr))
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
		return nil, fmt.Errorf("failed to serialize loadable machine key: %v", msalErrorMsg(msalErr))
	}
	if cSerializedKeyLen > 0 {
		key = C.GoBytes(unsafe.Pointer(cSerializedKey), C.int(cSerializedKeyLen))
	}

	return key, nil
}

func deserializeLoadableMachineKey(key []byte) (loadableMachineKey *C.LoadableMachineKey, cleanup func(), err error) {
	// The C call below indexes &key[0], so an empty key would panic.
	if len(key) == 0 {
		return nil, nil, fmt.Errorf("no machine key provided to deserialize")
	}

	msalErr := C.deserialize_loadable_machine_key(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		C.size_t(len(key)),
		&loadableMachineKey,
	)
	if msalErr != nil {
		return nil, nil, fmt.Errorf("failed to deserialize loadable machine key: %v", msalErrorMsg(msalErr))
	}

	cleanup = func() { C.loadable_machine_key_free(loadableMachineKey) }

	return loadableMachineKey, cleanup, nil
}

func acquireTokenByRefreshToken(broker *brokerClientApplication, refreshToken string, scopes []string, requestResource string, clientID string, tpm *boxedDynTPM, machineKey *C.MachineKey) (token *C.UserToken, cleanup func(), err error) {
	// The C call below indexes &cScopes[0], so an empty scope list would panic.
	if len(scopes) == 0 {
		return nil, nil, fmt.Errorf("no scopes provided for token acquisition")
	}

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
		// on_behalf_of client ID. Passing it per-call (rather than only via
		// broker_init) is what lets us resolve the user's groups: it requests the
		// token on behalf of the caller's OIDC app. The per-call value takes
		// precedence over the broker app's default on_behalf_of client ID.
		cClientID,
		(*C.BoxedDynTpm)(unsafe.Pointer(tpm)),
		machineKey,
		&userToken,
	)
	if msalErr != nil {
		defer C.error_free(msalErr)
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
		return "", fmt.Errorf("failed to get access token: %v", msalErrorMsg(msalErr))
	}
	defer C.free(unsafe.Pointer(cAccessToken))

	return C.GoString(cAccessToken), nil
}

func refreshTokenFromUserToken(userToken *C.UserToken) (refreshToken string, err error) {
	var cRefreshToken *C.char
	msalErr := C.user_token_refresh_token(userToken, &cRefreshToken)
	if msalErr != nil {
		return "", fmt.Errorf("failed to get refresh token: %v", msalErrorMsg(msalErr))
	}
	defer C.free(unsafe.Pointer(cRefreshToken))

	return C.GoString(cRefreshToken), nil
}

func initiateMFAFlow(broker *brokerClientApplication, username, password string) (*MFAFlowState, error) {
	cUsername := C.CString(username)
	defer C.free(unsafe.Pointer(cUsername))

	cPassword := C.CString(password)
	defer C.free(unsafe.Pointer(cPassword))

	var flow *C.MFAAuthContinue
	msalErr := C.broker_initiate_acquire_token_by_mfa_flow(
		(*C.BrokerClientApplication)(unsafe.Pointer(broker)),
		cUsername,
		cPassword,
		&flow,
	)
	if msalErr != nil {
		return nil, newMFAError(msalErr)
	}
	return newMFAFlowState(flow), nil
}

// newMFAFlowState wraps a C MFAAuthContinue pointer in the shared MFAFlowState type.
func newMFAFlowState(flow *C.MFAAuthContinue) *MFAFlowState {
	return &MFAFlowState{
		opaque:  flow,
		release: func() { C.mfa_auth_continue_free(flow) },
	}
}

// cFlow extracts the C MFAAuthContinue pointer from an MFAFlowState.
func cFlow(state *MFAFlowState) *C.MFAAuthContinue {
	if state == nil {
		return nil
	}
	flow, ok := state.opaque.(*C.MFAAuthContinue)
	if !ok {
		return nil
	}
	return flow
}

// msalErrorMsg extracts the message from a C MSAL_ERROR and frees it.
// Use it on one-shot error-reporting paths to avoid leaking the error struct.
func msalErrorMsg(msalErr *C.MSAL_ERROR) string {
	defer C.error_free(msalErr)
	return C.GoString(msalErr.msg)
}

// newMFAError builds an MFAError from an msalErr and frees it.
func newMFAError(msalErr *C.MSAL_ERROR) *MFAError {
	defer C.error_free(msalErr)
	msg := C.GoString(msalErr.msg)
	category := mfaErrorCategory(msalErr.code)
	// libhimmelblau surfaces user-denied MFA (authorization_state==1) as a
	// GENERAL_FAILURE with the message "Authorization denied" rather than a
	// dedicated C error code. Promote that to MFAErrorDenied so the broker's
	// denial-specific branch is reachable.
	if category == MFAErrorOther && strings.EqualFold(msg, "authorization denied") {
		category = MFAErrorDenied
	}
	// libhimmelblau's code-submission branch of acquire_token_by_mfa_flow
	// discards the server's "retry" flag and AADSTS error code for an incorrect
	// or expired one-time code, returning a generic GeneralFailure with the
	// message "AuthResponse indicates failure: ...". Its polling branch, by
	// contrast, surfaces a structured MFA_POLL_CONTINUE. Promote the code-path
	// failure to MFAErrorRetryableCode so consumers can re-prompt for the code
	// without depending on libhimmelblau's error text themselves.
	//
	// The robust fix lives upstream: make the EndAuth code-submission branch
	// honor auth_response.retry and return MFA_POLL_CONTINUE (mirroring the
	// polling branch), after which this promotion would become unnecessary. That
	// change was deliberately NOT made because acquire_token_by_mfa_flow is a
	// PUBLIC API shared with other consumers (e.g. himmelblau-idm) that do not
	// expect MFA_POLL_CONTINUE on the code path. The matched text is unique to
	// that branch (the poll branch uses "did not indicate success") and the
	// libhimmelblau submodule is pinned, so this match is safe — keep it in sync
	// if the submodule is bumped. See third_party/libhimmelblau/src/auth.rs.
	if category == MFAErrorOther && strings.Contains(msg, "AuthResponse indicates failure") {
		category = MFAErrorRetryableCode
	}
	return &MFAError{
		Category: category,
		AADSTS:   int(msalErr.aadsts_code),
		Message:  msg,
	}
}

func initiateMFAFlowForEnrollment(broker *brokerClientApplication, username, password string) (*MFAFlowState, error) {
	cUsername := C.CString(username)
	defer C.free(unsafe.Pointer(cUsername))

	cPassword := C.CString(password)
	defer C.free(unsafe.Pointer(cPassword))

	var flow *C.MFAAuthContinue
	msalErr := C.broker_initiate_acquire_token_by_mfa_flow_for_device_enrollment(
		(*C.BrokerClientApplication)(unsafe.Pointer(broker)),
		cUsername,
		cPassword,
		&flow,
	)
	if msalErr != nil {
		return nil, newMFAError(msalErr)
	}

	return newMFAFlowState(flow), nil
}

func acquireTokenByMFAFlow(broker *brokerClientApplication, username string, flow *MFAFlowState, authData string, pollAttempt int) (token *C.UserToken, cleanup func(), err error) {
	if flow == nil {
		return nil, nil, fmt.Errorf("missing MFA flow state")
	}
	// Hold the flow lock for the duration of the C call so that a concurrent
	// FreeMFAFlowState (e.g. from EndSession after a cancelled poll) cannot
	// free the MFAAuthContinue while it is in use.
	flow.mu.Lock()
	defer flow.mu.Unlock()
	cf := cFlow(flow)
	if cf == nil {
		return nil, nil, fmt.Errorf("MFA flow state has been released")
	}

	cUsername := C.CString(username)
	defer C.free(unsafe.Pointer(cUsername))

	var cAuthData *C.char
	if authData != "" {
		cAuthData = C.CString(authData)
		defer C.free(unsafe.Pointer(cAuthData))
	}

	var userToken *C.UserToken
	msalErr := C.broker_acquire_token_by_mfa_flow(
		(*C.BrokerClientApplication)(unsafe.Pointer(broker)),
		cUsername,
		cAuthData,
		C.int(pollAttempt),
		cf,
		&userToken,
	)
	if msalErr != nil {
		return nil, nil, newMFAError(msalErr)
	}

	cleanup = func() { C.user_token_free(userToken) }
	return userToken, cleanup, nil
}

// The mfaFlow* accessors read the continuation state, so they take flow.mu to
// honour MFAFlowState's locking contract (a concurrent FreeMFAFlowState must not
// release the state mid-read). They are currently only called at flow creation,
// before the flow is shared, but locking keeps them safe if that ever changes.
func mfaFlowMessage(flow *MFAFlowState) (string, error) {
	if flow == nil {
		return "", fmt.Errorf("missing MFA flow state")
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	c := cFlow(flow)
	if c == nil {
		return "", fmt.Errorf("missing MFA flow state")
	}
	var cMsg *C.char
	msalErr := C.mfa_auth_continue_msg(c, &cMsg)
	if msalErr != nil {
		return "", fmt.Errorf("failed to get MFA continue message: %v", msalErrorMsg(msalErr))
	}
	defer C.free(unsafe.Pointer(cMsg))
	return C.GoString(cMsg), nil
}

func mfaFlowMethod(flow *MFAFlowState) (string, error) {
	if flow == nil {
		return "", fmt.Errorf("missing MFA flow state")
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	c := cFlow(flow)
	if c == nil {
		return "", fmt.Errorf("missing MFA flow state")
	}
	var cMethod *C.char
	msalErr := C.mfa_auth_continue_mfa_method(c, &cMethod)
	if msalErr != nil {
		return "", fmt.Errorf("failed to get MFA method: %v", msalErrorMsg(msalErr))
	}
	defer C.free(unsafe.Pointer(cMethod))
	return C.GoString(cMethod), nil
}

func mfaFlowPollingInterval(flow *MFAFlowState) int {
	if flow == nil {
		return -1
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	c := cFlow(flow)
	if c == nil {
		return -1
	}
	return int(C.mfa_auth_continue_polling_interval(c))
}

func mfaFlowMaxPollAttempts(flow *MFAFlowState) int {
	if flow == nil {
		return -1
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	c := cFlow(flow)
	if c == nil {
		return -1
	}
	return int(C.mfa_auth_continue_max_poll_attempts(c))
}
