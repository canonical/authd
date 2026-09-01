package himmelblau

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenAcquisitionErrorClassification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCodes []uint32
		want       error
		wantCode   string
	}{
		"Device_disabled":      {errorCodes: []uint32{deviceDisabledErrorCode}, want: ErrDeviceDisabled, wantCode: "AADSTS135011"},
		"Invalid_redirect_URI": {errorCodes: []uint32{invalidRedirectURIErrorCode}, want: ErrInvalidRedirectURI, wantCode: "AADSTS50011"},
		// AADSTS500113 is a separate documented code, but the same
		// reply-address misconfiguration family as AADSTS50011. It must be
		// classified explicitly rather than caught by prefix matching.
		"No_reply_address":             {errorCodes: []uint32{noReplyAddressErrorCode}, want: ErrInvalidRedirectURI, wantCode: "AADSTS500113"},
		"Missing_client_credentials":   {errorCodes: []uint32{missingClientCredentialsErrorCode}, want: ErrMissingClientCredentials, wantCode: "AADSTS7000218"},
		"Device_authentication_failed": {errorCodes: []uint32{deviceAuthenticationFailedErrorCode}, want: ErrDeviceAuthenticationFailed, wantCode: "AADSTS50155"},
		// AADSTS codes are exact numeric identifiers. An unknown code that
		// shares a prefix with a handled code must fall through to the catch-all.
		"Unrelated_code_sharing_50011_prefix_is_not_invalid_redirect": {errorCodes: []uint32{500114}},
		// AADSTS50155/DEVICE_AUTH_FAIL has no documented subcode family, and
		// misclassifying an unrelated code as a confirmed device-auth
		// failure is destructive (clears cached device registration data).
		// An undocumented/future code that merely starts with "50155" must
		// NOT match -- it should fall through to the non-destructive
		// catch-all, not to ErrDeviceAuthenticationFailed.
		"Unrelated_code_sharing_50155_prefix_is_not_device_authentication_failed": {errorCodes: []uint32{501559}},
		// ErrDeviceDisabled denies the login outright, and AADSTS135011 has
		// no documented subcode family. An undocumented/future code that
		// merely starts with "135011" must fall through to the catch-all
		// instead of denying a login based on a guessed classification.
		"Unrelated_code_sharing_135011_prefix_is_not_device_disabled":             {errorCodes: []uint32{1350119}},
		"Unrelated_code_sharing_7000218_prefix_is_not_missing_client_credentials": {errorCodes: []uint32{70002181}},
		// A genuinely unclassified/unknown AADSTS code (not one of the ones
		// above) must fall back to a plain error, never to one of the specific
		// sentinels.
		"Unexpected_token_acquisition": {errorCodes: []uint32{999999}},
		"Empty_error_codes_list":       {errorCodes: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tokenAcquisitionError(tc.errorCodes, "token acquisition failed")
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Contains(t, err.Error(), tc.wantCode)
				return
			}
			require.Error(t, err)
			for _, sentinel := range []error{ErrDeviceDisabled, ErrInvalidRedirectURI, ErrMissingClientCredentials, ErrDeviceAuthenticationFailed} {
				require.NotErrorIs(t, err, sentinel)
			}
		})
	}
}

func TestWithAADSTSErrorCodeDoesNotDuplicateCodeInProviderMessage(t *testing.T) {
	t.Parallel()

	err := withAADSTSErrorCode(
		ErrMissingClientCredentials,
		missingClientCredentialsErrorCode,
		"Token acquisition failed: invalid_client (AADSTS7000218: missing client credentials)",
	)

	require.Equal(t, 1, strings.Count(err.Error(), "AADSTS7000218"))
}
