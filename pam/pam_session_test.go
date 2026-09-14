package main

import (
	"testing"

	"github.com/canonical/authd/pam/internal/adapter"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

type testPamReturnError struct {
	status pam.Error
}

func (e testPamReturnError) Message() string   { return "message" }
func (e testPamReturnError) Status() pam.Error { return e.status }

func TestShouldSendPamMessage(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		style      pam.Style
		clientType adapter.PamClientType
		retStatus  adapter.PamReturnValue

		want bool
	}{
		"Does_not_send_native_success_info_messages_again": {
			style:      pam.TextInfo,
			clientType: adapter.Native,
			retStatus:  adapter.PamSuccess{},
			want:       false,
		},
		"Sends_gdm_success_info_messages_via_pam_conversation": {
			style:      pam.TextInfo,
			clientType: adapter.Gdm,
			retStatus:  adapter.PamSuccess{},
			want:       true,
		},
		"Sends_interactive_terminal_success_info_messages": {
			style:      pam.TextInfo,
			clientType: adapter.InteractiveTerminal,
			retStatus:  adapter.PamSuccess{},
			want:       true,
		},
		"Does_not_send_gdm_auth_error_messages": {
			style:      pam.ErrorMsg,
			clientType: adapter.Gdm,
			retStatus:  testPamReturnError{status: pam.ErrAuth},
			want:       false,
		},
		"Does_not_send_gdm_maxtries_error_messages": {
			style:      pam.ErrorMsg,
			clientType: adapter.Gdm,
			retStatus:  testPamReturnError{status: pam.ErrMaxtries},
			want:       false,
		},
		"Sends_gdm_system_error_messages": {
			style:      pam.ErrorMsg,
			clientType: adapter.Gdm,
			retStatus:  testPamReturnError{status: pam.ErrSystem},
			want:       true,
		},
		"Sends_native_ignore_info_messages": {
			style:      pam.TextInfo,
			clientType: adapter.Native,
			retStatus:  testPamReturnError{status: pam.ErrIgnore},
			want:       true,
		},
		"Sends_native_error_messages": {
			style:      pam.ErrorMsg,
			clientType: adapter.Native,
			retStatus:  testPamReturnError{status: pam.ErrAuth},
			want:       true,
		},
		"Sends_interactive_terminal_error_messages": {
			style:      pam.ErrorMsg,
			clientType: adapter.InteractiveTerminal,
			retStatus:  testPamReturnError{status: pam.ErrAuth},
			want:       true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want,
				shouldSendPamMessage(tc.style, tc.clientType, tc.retStatus))
		})
	}
}
