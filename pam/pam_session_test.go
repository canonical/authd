package main

import (
	"testing"

	"github.com/canonical/authd/pam/internal/adapter"
	"github.com/stretchr/testify/require"
)

func TestShouldSendAuthMessage(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		clientType adapter.PamClientType
		msg        string
		isSuccess  bool

		want bool
	}{
		"Does_not_send_native_success_messages_again": {
			clientType: adapter.Native,
			msg:        "cached",
			isSuccess:  true,
			want:       false,
		},
		"Sends_gdm_success_messages_via_pam_conversation": {
			clientType: adapter.Gdm,
			msg:        "cached",
			isSuccess:  true,
			want:       true,
		},
		"Sends_interactive_terminal_success_messages": {
			clientType: adapter.InteractiveTerminal,
			msg:        "cached",
			isSuccess:  true,
			want:       true,
		},
		"Sends_error_messages": {
			clientType: adapter.Native,
			msg:        "denied",
			isSuccess:  false,
			want:       true,
		},
		"Ignores_empty_messages": {
			clientType: adapter.Native,
			msg:        "",
			isSuccess:  true,
			want:       false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, shouldSendAuthMessage(tc.clientType, tc.msg, tc.isSuccess))
		})
	}
}
