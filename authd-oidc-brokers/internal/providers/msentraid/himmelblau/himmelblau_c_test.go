//go:build withmsentraid

package himmelblau

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeserializeLoadableMachineKeyRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	// An empty key must return an error rather than panicking on &key[0].
	_, cleanup, err := deserializeLoadableMachineKey(nil)
	require.Error(t, err, "deserializeLoadableMachineKey should reject an empty key")
	require.Nil(t, cleanup, "no cleanup should be returned on error")

	_, cleanup, err = deserializeLoadableMachineKey([]byte{})
	require.Error(t, err, "deserializeLoadableMachineKey should reject a zero-length key")
	require.Nil(t, cleanup, "no cleanup should be returned on error")
}

// TestMFAErrorCategoryMapping guards against the enum-drift bug where the MFA
// error codes were hardcoded as Go integer literals (mfaRequiredCode = 24). The
// MSAL_ERROR_CODE enum gates some variants (e.g. CHANGE_PASSWORD) behind cargo
// features, so the numeric value of later variants such as MFA_REQUIRED depends on
// the build. The codes are now derived from the cgo enum constants, and this test
// pins both the mapping and the documented layout.
func TestMFAErrorCategoryMapping(t *testing.T) {
	t.Parallel()

	require.Equal(t, MFAErrorPollContinue, mfaErrorCategory(codeMFAPollContinue),
		"MFA_POLL_CONTINUE must map to MFAErrorPollContinue")
	require.Equal(t, MFAErrorRequired, mfaErrorCategory(codeMFARequired),
		"MFA_REQUIRED must map to MFAErrorRequired")

	// The original bug hardcoded mfaRequiredCode=24, which is actually
	// AUTH_CODE_RECEIVED once the changepassword feature shifts the enum. That
	// misclassified AUTH_CODE_RECEIVED as MFAErrorRequired and let the real
	// MFA_REQUIRED (25) fall through to MFAErrorOther, breaking the
	// "MFA required -> redirect to Device Authentication" fallback. Pin both
	// directions so the literal bug cannot return.
	require.Equal(t, MFAErrorOther, mfaErrorCategory(codeAuthCodeReceived),
		"AUTH_CODE_RECEIVED must NOT be classified as MFAErrorRequired")
	require.NotEqual(t, codeAuthCodeReceived, codeMFARequired,
		"AUTH_CODE_RECEIVED and MFA_REQUIRED must be distinct codes")

	// Documented enum layout with the changepassword feature enabled (generate.sh).
	// A failure here means the compiled C enum shifted, so the code->category
	// mapping (and any other code that depends on these values) must be re-verified.
	require.Equal(t, uint32(14), codeMFAPollContinue, "MFA_POLL_CONTINUE is expected to be 14")
	require.Equal(t, uint32(24), codeAuthCodeReceived, "AUTH_CODE_RECEIVED is expected to be 24")
	require.Equal(t, uint32(25), codeMFARequired, "MFA_REQUIRED is expected to be 25 (changepassword enabled)")
}
