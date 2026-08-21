package himmelblau

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenAcquisitionErrorClassification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCodes []uint32
		want       error
	}{
		"Device_disabled":              {errorCodes: []uint32{deviceDisabledErrorCode}, want: ErrDeviceDisabled},
		"Invalid_redirect_URI":         {errorCodes: []uint32{invalidRedirectURIErrorCode}, want: ErrInvalidRedirectURI},
		"Missing_client_credentials":   {errorCodes: []uint32{missingClientCredentialsErrorCode}, want: ErrMissingClientCredentials},
		"Device_authentication_failed": {errorCodes: []uint32{deviceAuthenticationFailedErrorCode}, want: ErrDeviceAuthenticationFailed},
		// Microsoft appends extra digits/subcodes to some base AADSTS codes
		// (e.g. AADSTS500113 as a subcode of AADSTS50011, per the comment on
		// tokenAcquisitionError). Prefix matching must still classify this
		// documented variation correctly.
		"Invalid_redirect_URI_subcode": {errorCodes: []uint32{500113}, want: ErrInvalidRedirectURI},
		// AADSTS50155/DEVICE_AUTH_FAIL has no documented subcode family, and
		// misclassifying an unrelated code as a confirmed device-auth
		// failure is destructive (clears cached device registration data).
		// An undocumented/future code that merely starts with "50155" must
		// NOT match -- it should fall through to the non-destructive
		// catch-all, not to ErrDeviceAuthenticationFailed.
		"Unrelated_code_sharing_50155_prefix_is_not_device_authentication_failed": {errorCodes: []uint32{501559}},
		// A genuinely unclassified/unknown AADSTS code (not one of the ones
		// above) must fall back to the catch-all TokenAcquisitionError, never
		// to one of the specific sentinels.
		"Unexpected_token_acquisition": {errorCodes: []uint32{999999}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tokenAcquisitionError(tc.errorCodes, "token acquisition failed")
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}

			var tokenErr TokenAcquisitionError
			require.ErrorAs(t, err, &tokenErr)
		})
	}
}
