package himmelblau

import (
	"context"
	"fmt"
	"sync"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"golang.org/x/oauth2"
)

// EntraPasswordProvider is an optional interface that providers can implement
// to support the Entra ID password + MFA authentication flow.
type EntraPasswordProvider interface {
	// InitiateEntraPasswordAuth starts the Entra password/passwordless + MFA flow.
	// It submits credentials and returns an MFA challenge state.
	// clientID is the OIDC application client ID (on_behalf_of_client_id);
	// it is used to build the OIDC app inside the Rust layer so that the
	// resulting tokens can include Microsoft Graph API scopes.
	// When withDeviceScope is true, the MFA flow adds Intune enrollment
	// resources to the token request (needed for PRT-based token exchange).
	// When false, it uses only MS Graph scopes.
	// authOpts toggles optional flow behaviors (e.g. AuthOptionFido to let
	// Entra ID negotiate a FIDO/security-key challenge).
	InitiateEntraPasswordAuth(
		ctx context.Context,
		clientID string,
		issuerURL string,
		username, password string,
		deviceRegistrationData []byte,
		withDeviceScope bool,
		authOpts ...AuthOption,
	) (*MFAFlowState, *MFAChallengeInfo, error)

	// AcquireTokenByMFAFlow completes the MFA challenge.
	// clientID is the OIDC application client ID (on_behalf_of_client_id).
	// For poll-based MFA, authData is empty and pollAttempt increments.
	// For code-based MFA, authData is the user-entered code.
	// Returns an OAuth token built from the MFA result on success.
	AcquireTokenByMFAFlow(
		ctx context.Context,
		clientID string,
		issuerURL string,
		username string,
		flow *MFAFlowState,
		authData string,
		pollAttempt int,
		deviceRegistrationData []byte,
	) (*oauth2.Token, error)

	// RefreshEntraPasswordToken refreshes a cached Entra password/passwordless + MFA refresh
	// token to re-verify the account against Entra ID on a returning login, the
	// same way the device-auth flow's token refresh does. It is a plain OAuth2
	// refresh as a public client (no client_secret) for basic scopes only — never
	// Microsoft Graph — so it works regardless of register_device and never hits
	// the Broker-app↔Graph preauthorization wall.
	//
	// On success it returns the rotated token (the new refresh token must be
	// persisted). On an Entra rejection it returns an *oauth2.RetrieveError so the
	// broker can classify it with the same checks it uses for device-auth
	// (IsUserDisabledError → AADSTS50057, IsTokenExpiredError → AADSTS50173, etc.).
	RefreshEntraPasswordToken(
		ctx context.Context,
		issuerURL string,
		refreshToken string,
	) (*oauth2.Token, error)

	// VerifyAccessToken verifies the RS256 signature of the MFA-flow access token
	// against the tenant's published JWKS (handling the header-nonce rewrite that
	// Microsoft first-party tokens use) and that its tenant claim matches. It is
	// the defense-in-depth check that the token genuinely came from Microsoft, so
	// the identity read from its claims is not trusted on TLS alone. Returns nil
	// only when the token verifies.
	VerifyAccessToken(
		ctx context.Context,
		issuerURL string,
		accessToken string,
	) error

	// UserInfoFromAccessToken extracts user identity from a verified Entra access
	// token, mapping provider-specific claims (e.g. oid/upn) to the same info.User
	// shape that GetUserInfo returns for OIDC ID tokens / UserInfo responses.
	UserInfoFromAccessToken(accessToken string) (info.User, error)
}

// MFAFlowState is an opaque handle to an in-progress MFA flow.
// The actual continuation state is owned by the libhimmelblau-backed
// implementation, which also supplies the release callback used by
// FreeMFAFlowState.
type MFAFlowState struct {
	// mu serializes access to the underlying continuation state so that a
	// concurrent FreeMFAFlowState (e.g. from EndSession while a cancelled
	// poll goroutine is still running) cannot release it while it is in use
	// or release it twice.
	mu      sync.Mutex
	opaque  any
	release func()
}

// FreeMFAFlowState releases resources associated with the MFA flow state.
// It is safe to call with a nil state, to call repeatedly, and to call
// concurrently with an in-flight use of the flow (it blocks until the use
// completes).
func FreeMFAFlowState(flow *MFAFlowState) {
	if flow == nil {
		return
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.release != nil {
		flow.release()
	}
	flow.release = nil
	flow.opaque = nil
}

// AuthOption toggles optional behaviors of the MFA flow initiation, mirroring
// libhimmelblau's AuthOption without exposing its C enum values.
type AuthOption int

const (
	// AuthOptionNoDAGFallback suppresses the silent Device Authorization Grant
	// fallback in libhimmelblau. The broker surfaces MFA challenges through
	// dedicated auth modes and never wants the DAG fallback, so this is always
	// passed by InitiateMFAFlowWithPassword.
	AuthOptionNoDAGFallback AuthOption = iota

	// AuthOptionFido advertises that the caller can perform a FIDO/WebAuthn
	// assertion. Without it, Entra ID may still select a FIDO method for the
	// user, but libhimmelblau does not fetch the WebAuthn challenge, so the
	// flow cannot be completed locally.
	AuthOptionFido

	// AuthOptionPasswordless asks libhimmelblau to attempt passwordless factors
	// (Authenticator number-matching, TAP, FIDO/security key, ...) as primary
	// authentication. It is the intent switch, orthogonal to the password
	// argument: InitiateMFAFlow sets it whenever no password is supplied.
	AuthOptionPasswordless
)

// MFAChallengeInfo describes the MFA challenge that must be presented to the user.
type MFAChallengeInfo struct {
	Message           string
	Method            string
	PollingIntervalMs int
	MaxPollAttempts   int

	// FidoChallenge is the WebAuthn challenge to sign when a FIDO method was
	// negotiated. Empty for non-FIDO challenges.
	FidoChallenge string
	// FidoAllowList contains the credential IDs (base64-encoded) that Entra ID
	// accepts for the FIDO assertion. Empty for non-FIDO challenges.
	FidoAllowList []string
}

// MFAErrorCategory classifies an MFA error so the broker can route
// it without depending on libhimmelblau-specific numeric codes.
type MFAErrorCategory int

const (
	// MFAErrorOther is the default category and means the error has no
	// specific routing semantics.
	MFAErrorOther MFAErrorCategory = iota
	// MFAErrorPollContinue means the MFA poll loop should keep polling.
	MFAErrorPollContinue
	// MFAErrorDenied means the user actively rejected the MFA challenge
	// (e.g. tapped "Deny" on a push notification).
	MFAErrorDenied
	// MFAErrorRequired means MFA is required to complete authentication.
	MFAErrorRequired
	// MFAErrorRetryableCode means a submitted one-time code was incorrect or
	// expired while the MFA flow itself remains valid, so the user can simply
	// re-enter the code without restarting the flow. See newMFAError for how
	// this is detected.
	MFAErrorRetryableCode
	// MFAErrorPasswordRequired means a passwordless flow found no usable
	// passwordless method for the account, so authentication needs the
	// password flow instead.
	MFAErrorPasswordRequired
)

// MFAError represents an error from initiating or continuing an MFA flow.
//
// Category is set so that consumers can branch on well-known outcomes without
// referencing libhimmelblau-specific error codes. AADSTS, when non-zero,
// carries the Entra ID AADSTS error code.
type MFAError struct {
	Category MFAErrorCategory
	AADSTS   int
	Message  string
}

// Error returns the formatted error message.
func (e *MFAError) Error() string {
	if e.AADSTS != 0 {
		return fmt.Sprintf("AADSTS%d: %s", e.AADSTS, e.Message)
	}
	return e.Message
}

// IsMFAPollContinue returns true if the error indicates the MFA poll should continue.
func (e *MFAError) IsMFAPollContinue() bool {
	return e.Category == MFAErrorPollContinue
}

// IsMFADenied returns true if the error indicates the MFA request was actively
// rejected (e.g., user denied the push notification).
func (e *MFAError) IsMFADenied() bool {
	return e.Category == MFAErrorDenied
}

// IsMFARequired returns true if the error indicates MFA is required.
func (e *MFAError) IsMFARequired() bool {
	return e.Category == MFAErrorRequired
}

// IsMFARetryableCode returns true if the error indicates a submitted one-time
// code was incorrect or expired while the MFA flow remains valid, so the user
// can retry the code without restarting the flow.
func (e *MFAError) IsMFARetryableCode() bool {
	return e.Category == MFAErrorRetryableCode
}

// IsMFAPasswordRequired returns true if the error indicates that a
// passwordless flow found no usable passwordless method for the account, so
// authentication needs the password flow instead.
func (e *MFAError) IsMFAPasswordRequired() bool {
	return e.Category == MFAErrorPasswordRequired
}
