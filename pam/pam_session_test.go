package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/pam/internal/adapter"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

func TestSetAuthenticatedPamUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		release   string
		unixName  string // defaults to "unix-user"
		loginName string
		ssh       bool
		wantError bool
	}{
		"Reject_SSH_alias_on_24.04": {
			release: `ID=ubuntu
VERSION_ID="24.04"`,
			loginName: "provider-alias", ssh: true, wantError: true,
		},
		"Allow_Unix_name_on_24.04": {
			release: "ID=ubuntu\nVERSION_ID=24.04", loginName: "unix-user", ssh: true,
		},
		"Allow_case_variant_on_24.04": {
			release: "ID=ubuntu\nVERSION_ID=24.04", loginName: "UNIX-USER", ssh: true,
		},
		"Allow_non_SSH_alias_on_24.04": {
			release: "ID=ubuntu\nVERSION_ID=24.04", loginName: "provider-alias",
		},
		"Allow_SSH_alias_on_26.04": {
			release: "ID=ubuntu\nVERSION_ID=26.04", loginName: "provider-alias", ssh: true,
		},
		"Allow_SSH_alias_on_later_Ubuntu": {
			release: "ID=ubuntu\nVERSION_ID=26.10", loginName: "provider-alias", ssh: true,
		},
		"Leave_other_distributions_unchanged": {
			release: "ID=debian\nVERSION_ID=24.04", loginName: "provider-alias", ssh: true,
		},
		// authd lowercases names with ToLower, so the guard must too. ς and σ
		// are EqualFold-equal but ToLower-different, so they are different names.
		"Reject_SSH_final_sigma_alias_on_24.04": {
			release: "ID=ubuntu\nVERSION_ID=24.04", unixName: "σ", loginName: "ς", ssh: true, wantError: true,
		},
		// ToLower("İ") is "i", which authd resolves to the same account.
		"Allow_SSH_dotted_capital_I_on_24.04": {
			release: "ID=ubuntu\nVERSION_ID=24.04", unixName: "i", loginName: "İ", ssh: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			unixName := tc.unixName
			if unixName == "" {
				unixName = "unix-user"
			}
			path := filepath.Join(t.TempDir(), "os-release")
			require.NoError(t, os.WriteFile(path, []byte(tc.release), 0600))
			var message string
			tx := pam_test.NewModuleTransactionDummy(pam.ConversationFunc(func(style pam.Style, msg string) (string, error) {
				require.Equal(t, pam.ErrorMsg, style)
				message = msg
				return "", nil
			}))
			require.NoError(t, tx.SetItem(pam.User, tc.loginName))
			var originalName string
			if tc.ssh {
				var err error
				originalName, err = originalSSHLoginName(tx)
				require.NoError(t, err)
			}

			err := setAuthenticatedPamUser(tx, unixName, originalName, path)
			if tc.wantError {
				require.ErrorIs(t, err, pam.ErrAuth)
				require.Equal(t, fmt.Sprintf("Log in with your Unix username %q on Ubuntu 24.04.", unixName), message)
				currentName, err := tx.GetItem(pam.User)
				require.NoError(t, err)
				require.Equal(t, tc.loginName, currentName, "a denied alias must not rewrite PAM_USER")

				// Even if another module changes PAM_USER, retries must retain
				// the SSH connection's original name.
				require.NoError(t, tx.SetItem(pam.User, unixName))
				retryName, err := originalSSHLoginName(tx)
				require.NoError(t, err)
				require.Equal(t, tc.loginName, retryName)
				require.ErrorIs(t, setAuthenticatedPamUser(tx, unixName, retryName, path), pam.ErrAuth)
				return
			}
			require.NoError(t, err)
			currentName, err := tx.GetItem(pam.User)
			require.NoError(t, err)
			require.Equal(t, unixName, currentName)
			require.Empty(t, message)
		})
	}
}

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
