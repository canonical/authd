package himmelblau

import (
	"fmt"
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
		// libhimmelblau can report a list of codes. The classification must keep
		// scanning past codes it does not know, otherwise a documented signal
		// behind an unrecognized code (for example the device-authentication
		// failure that starts device recovery) is lost.
		"Known_code_after_unknown_code": {
			errorCodes: []uint32{999999, deviceAuthenticationFailedErrorCode},
			want:       ErrDeviceAuthenticationFailed,
			wantCode:   "AADSTS50155",
		},
		"Missing_client_credentials_after_unknown_code": {
			errorCodes: []uint32{999999, missingClientCredentialsErrorCode},
			want:       ErrMissingClientCredentials,
			wantCode:   "AADSTS7000218",
		},
		// Two recognized codes: the one libhimmelblau reports first classifies
		// the failure, and both stay visible in the message. Nothing ranks the
		// codes by severity, so the reported order decides which branch runs --
		// the reverse case below pins the other order.
		"First_classified_code_wins": {
			errorCodes: []uint32{missingClientCredentialsErrorCode, deviceAuthenticationFailedErrorCode},
			want:       ErrMissingClientCredentials,
			wantCode:   "AADSTS7000218",
		},
		"First_classified_code_wins_reversed": {
			errorCodes: []uint32{deviceAuthenticationFailedErrorCode, missingClientCredentialsErrorCode},
			want:       ErrDeviceAuthenticationFailed,
			wantCode:   "AADSTS50155",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tokenAcquisitionError(tc.errorCodes, "token acquisition failed")
			require.Contains(t, err.Error(), "token acquisition failed",
				"the provider message must survive into the returned error")
			// Every reported code must stay visible, also in a classified
			// failure: libhimmelblau can report codes only through its list, so
			// a code missing from the error is missing from the log too.
			for _, code := range tc.errorCodes {
				require.Contains(t, err.Error(), fmt.Sprintf("%d", code),
					"every reported AADSTS code must stay visible in the error message")
			}
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

func TestTokenAcquisitionErrorDoesNotRepeatCodeFromProviderMessage(t *testing.T) {
	t.Parallel()

	err := tokenAcquisitionError(
		[]uint32{missingClientCredentialsErrorCode},
		"Token acquisition failed: invalid_client (AADSTS7000218: missing client credentials)",
	)

	require.ErrorIs(t, err, ErrMissingClientCredentials)
	require.Equal(t, 1, strings.Count(err.Error(), "AADSTS7000218"),
		"the AADSTS code that the provider message already names must not be annotated again")
}

func TestTokenAcquisitionErrorAddsCodeThatIsOnlyAPrefixOfTheProviderCode(t *testing.T) {
	t.Parallel()

	err := tokenAcquisitionError(
		[]uint32{invalidRedirectURIErrorCode},
		"Invalid reply address: AADSTS500113 (no reply address registered)",
	)

	require.ErrorIs(t, err, ErrInvalidRedirectURI)
	require.Contains(t, err.Error(), "AADSTS50011)",
		"a longer code in the provider message must not hide the classified code")
	require.Contains(t, err.Error(), "AADSTS500113",
		"the provider message must stay intact")
}

func TestContainsAADSTSCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		message string
		code    string
		want    bool
	}{
		"Code_stands_alone":               {message: "failed (AADSTS7000218)", code: "AADSTS7000218", want: true},
		"Code_followed_by_text":           {message: "AADSTS50011: invalid redirect", code: "AADSTS50011", want: true},
		"Longer_code_does_not_match":      {message: "failed (AADSTS500113)", code: "AADSTS50011", want: false},
		"Longer_match_before_exact_match": {message: "AADSTS500113, also AADSTS50011", code: "AADSTS50011", want: true},
		"Code_absent":                     {message: "no AADSTS code here", code: "AADSTS50011", want: false},
		"Code_longer_than_message":        {message: "AADSTS1", code: "AADSTS50011", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, containsAADSTSCode(tc.message, tc.code))
		})
	}
}
