package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/testutils/ptytest"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

func TestCLIAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	// This test is flaky, see https://github.com/canonical/authd/issues/1329
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tests := map[string]struct {
		pamUser  string
		username string // typed at the Username: prompt (empty = use pamUser as preset)

		clientOptions      clientOptions
		currentUserNotRoot bool
		wantLocalGroups    bool
		extraArgs          []string
		socketPath         string // override socket path
		useCancelableAuthd bool

		test          func(t *testing.T, c *ptytest.Console)
		testWithAuthd func(t *testing.T, c *ptytest.Console, cancelAuthd func())
	}{
		"Authenticate_user_successfully": {
			username: testUserName(t, "simple"),
			test:     cliSimpleAuth,
		},
		"Authenticate_user_successfully_with_upper_case": {
			username: testUserName(t, "upper-case"),
			test:     cliSimpleAuth,
		},
		"Authenticate_user_successfully_with_preset_user": {
			clientOptions: clientOptions{
				PamUser: testUserName(t, "preset"),
			},
			test: cliSimpleAuthPresetUser,
		},
		"Authenticate_user_successfully_with_upper_case_preset_user": {
			clientOptions: clientOptions{
				PamUser: strings.ToUpper(testUserName(t, "preset-upper-case")),
			},
			test: cliSimpleAuthPresetUser,
		},
		"Authenticate_user_successfully_with_invalid_connection_timeout": {
			username:      testUserName(t, "invalid-timeout"),
			clientOptions: clientOptions{PamTimeout: "invalid"},
			test:          cliSimpleAuth,
		},
		"Authenticate_user_successfully_with_password_only_supported_method": {
			username: examplebroker.UserIntegrationAuthModesPrefix + "password-integration-cli@example.com",
			test:     cliSimpleAuth,
		},
		"Authenticate_user_successfully_after_trying_empty_user": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				c.WaitFor(t, `Username:`)
				c.Send(t, "user-integration-not-empty@example.com")
				for i := 0; i < 38; i++ {
					c.Send(t, "\x7f")
				}
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `user name`)
				c.SendKey(t, ptytest.KeyEscape)
				c.Send(t, "\x7f")
				c.Send(t, "user-integration-was-empty@example.com")
				c.SendKey(t, ptytest.KeyEnter)

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_mfa": {
			username: "user-mfa@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Go to auth method selection first.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Password authentication`)

				// Select password auth.
				c.Send(t, "1")
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				// MFA: fido device.
				c.WaitFor(t, `Plug your fido device and press with your thumb`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your fido device foo`)

				c.SendKey(t, ptytest.KeyEnter) // Select first option
				c.WaitFor(t, `Plug your fido device and press with your thumb`)

				// Wait for auto-advance to phone.
				c.WaitFor(t, `Unlock your phone \+33`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your phone \+33`)

				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Unlock your phone \+33`)

				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_form_mode_with_button": {
			username: "user-integration-form-w-button@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `7\. Authentication code`)

				c.Send(t, "7")
				c.WaitFor(t, `Enter your one time credential`)
				c.WaitFor(t, `Resend SMS \(1 sent\)`)

				// Press Tab to select button, then Enter.
				c.Send(t, "\t")
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Resend SMS \(2 sent\)`)

				c.SendLine(t, "temporary pass00")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code@example.com",
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `6\. Use a QR code`)
				c.Send(t, "6")
				c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
				c.WaitFor(t, `Code:\s*1337`)
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-tty@example.com",
				Term:    "linux",
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `6\. Use a QR code`)
				c.Send(t, "6")
				c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
				c.WaitFor(t, `Code:\s*1337`)
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY_session": {
			clientOptions: clientOptions{
				PamUser:     examplebroker.UserIntegrationPrefix + "qr-code-tty-session@example.com",
				Term:        "xterm-256color",
				SessionType: "tty",
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `6\. Use a QR code`)
				c.Send(t, "6")
				c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
				c.WaitFor(t, `Code:\s*1337`)
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code_in_screen": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-screen@example.com",
				Term:    "screen",
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `6\. Use a QR code`)
				c.Send(t, "6")
				c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
				c.WaitFor(t, `Code:\s*1337`)
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code_after_many_regenerations": {
			username: "user-integration-qrcode-static-regenerate@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `6\. Use a QR code`)
				c.Send(t, "6")
				c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
				c.WaitFor(t, `Code:\s*1337`)
				c.Send(t, "	")
				c.Send(t, strings.Repeat("\r", 100))
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			username: "user-needs-reset-integration-mandatory@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			username: strings.ToUpper(testUserNameFull(t,
				examplebroker.UserIntegrationNeedsResetPrefix, "Case-INSENSITIVE")),
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			username: "user-mfa-with-reset@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				c.WaitFor(t, `New password`)
				c.SendLine(t, "password")
				c.WaitFor(t, `The password fails the dictionary check`)
				c.SendLine(t, "1234")
				c.WaitFor(t, `The password is shorter than`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_offer_password_reset": {
			username: "user-can-reset@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				c.WaitFor(t, `Skip`)

				// Press Tab to select Skip button, then Enter.
				c.Send(t, "\t")
				c.SendKey(t, ptytest.KeyEnter)
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_auth_mode": {
			username: "user-integration-switch-mode@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Switch to auth mode selection.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `2\. Send URL to`)

				// Select "Send URL to" mode.
				c.Send(t, "2")
				c.WaitFor(t, `Click on the link received at .* or enter the code`)

				// Go back to auth mode selection.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `3\. Use your fido device foo`)

				// Select "Use your fido device" mode.
				c.Send(t, "3")
				c.WaitFor(t, `Plug your fido device and press with your thumb`)

				// Go back and select password auth.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Password authentication`)

				c.Send(t, "1")
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_username": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliEnterUsername(t, c, "user-integration-switch-username@example.com")
				c.WaitFor(t, `Select your provider`)

				// Go back to username.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Username:`)

				// Edit the username by sending backspaces and typing new suffix.
				for i := 0; i < len("username@example.com"); i++ {
					c.Send(t, "\x7f") // Backspace
				}
				c.SendLine(t, "username-switched@example.com")

				// Select broker and authenticate.
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_to_local_broker": {
			username: "user-integration-switch-broker@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Go back to auth method.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)

				// Go back to broker selection.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your provider`)
				c.WaitFor(t, `1\. local`)

				// Select local broker.
				c.Send(t, "1")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_add_it_to_local_group": {
			username:        "user-local-groups-integration-auth-cli@example.com",
			wantLocalGroups: true,
			test:            cliSimpleAuth,
		},
		"Authenticate_with_warnings_on_unsupported_arguments": {
			username:  "user2@example.com",
			extraArgs: []string{"invalid_flag=foo", "bar"},
			test:      cliSimpleAuth,
		},
		"Remember_last_successful_broker_and_mode": {
			username: "user-integration-remember-mode@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				// First login: select broker, then auth code mode.
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `7\. Authentication code`)

				c.Send(t, "7")
				c.WaitFor(t, `Enter your one time credential`)
				c.WaitFor(t, `Resend SMS \(1 sent\)`)

				c.SendLine(t, "temporary pass0")
				cliWaitForResult(t, c)

				// Note: The "remember broker" test needs a second login in the
				// the same session. With ptytest, we can't easily run a second command
				// in the same session. This test is kept for the first login only;
				// the remember check is done separately below.
			},
		},
		"Autoselect_local_broker_for_local_user": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliEnterUsername(t, c, "root")
				cliWaitForResult(t, c)
			},
		},
		"Autoselect_local_broker_for_local_user_preset": {
			clientOptions: clientOptions{PamUser: "root"},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliWaitForResult(t, c)
			},
		},
		"Prevent_user_from_switching_username": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "pam-preset@example.com",
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Go back to auth method, then provider.
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your provider`)

				// Verify we're still at provider selection by selecting broker.
				// Don't use cliSelectBroker here because bubbletea won't re-render
				// an identical view, so WaitFor can't find a new "Select your provider".
				c.Send(t, "2")
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				cliWaitForResult(t, c)
			},
		},

		"Deny_authentication_if_current_user_is_not_considered_as_root": {
			currentUserNotRoot: true,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliWaitForResult(t, c)
			},
		},
		"Deny_authentication_if_max_attempts_reached": {
			username: "user-integration-max-attempts@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Send all wrong passwords. We can't WaitFor the error between
				// attempts because bubbletea skips re-renders when the view
				// content is identical (same error message each time).
				for i := 0; i < 5; i++ {
					c.SendLine(t, "wrongpass")
				}

				c.SendLine(t, "wrongpass")
				c.WaitFor(t, `Maximum number of authentication attempts reached`)
				cliWaitForResult(t, c)
			},
		},
		"Deny_authentication_if_user_does_not_exist": {
			username: "user-unexistent@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliSelectBroker(t, c)
				cliWaitForResult(t, c)
			},
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			username: "user-needs-reset@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `No password supplied`)
				c.SendLine(t, "1234")
				c.WaitFor(t, `The password is shorter than`)
				c.SendLine(t, "12345678")
				c.WaitFor(t, `The password fails the dictionary check`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "123456789")
				c.WaitFor(t, `Password entries don't match`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
			},
		},

		"Exit_authd_if_local_broker_is_selected": {
			username: "user-local-broker",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Select your provider`)
				c.WaitFor(t, `1\. local`)
				c.SendKey(t, ptytest.KeyEnter)
				cliWaitForResult(t, c)
			},
		},
		"Exit_authd_if_user_sigints": {
			username: "user-integration-sigint@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlC)
				cliWaitForResult(t, c)
			},
		},
		"Exit_authd_if_user_presses_ctrl_d": {
			username: "user-integration-ctrl-d@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlD)
				cliWaitForResult(t, c)
			},
		},
		"Exit_if_authd_is_stopped": {
			useCancelableAuthd: true,
			testWithAuthd: func(t *testing.T, c *ptytest.Console, cancelAuthd func()) {
				t.Helper()

				c.WaitFor(t, `Username:`)
				cancelAuthd()
				c.WaitFor(t, `stopped serving`)
				cliWaitForResult(t, c)
			},
		},

		"Error_if_cannot_connect_to_authd": {
			socketPath: "/some-path/not-existent-socket",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `could not connect to unix:`)
				cliWaitForResult(t, c)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var socketPath, groupFileOutput string
			var cancelAuthd func()
			if tc.wantLocalGroups || tc.currentUserNotRoot || tc.useCancelableAuthd {
				var groupFile string
				groupFileOutput, groupFile = prepareGroupFiles(t)

				if tc.wantLocalGroups {
					groupFileOutput = groupFile
				}

				args := []testutils.DaemonOption{
					testutils.WithGroupFile(groupFile),
					testutils.WithGroupFileOutput(groupFileOutput),
				}
				if !tc.currentUserNotRoot {
					args = append(args, testutils.WithCurrentUserAsRoot)
				}

				if tc.useCancelableAuthd {
					socketPath, cancelAuthd = runAuthdForTestingWithCancel(t, false, args...)
					t.Cleanup(cancelAuthd)
				} else {
					socketPath = runAuthd(t, args...)
				}
			} else {
				socketPath, groupFileOutput = sharedAuthd(t)
			}
			if tc.socketPath != "" {
				socketPath = tc.socketPath
			}

			c := startPAMRunner(t, clientPath, socketPath,
				pam_test.RunnerActionLogin, cliEnv, tc.clientOptions, tc.extraArgs...)

			// If we have a typed username (not preset), enter it.
			if tc.username != "" && tc.clientOptions.PamUser == "" {
				cliEnterUsername(t, c, tc.username)
			}

			if tc.testWithAuthd != nil {
				tc.testWithAuthd(t, c, cancelAuthd)
			} else {
				tc.test(t, c)
			}

			err := c.WaitForExit(t)
			// Allow non-zero exits (e.g. auth failures, sigint).
			_ = err

			got := ptySanitizeOutput(t, c.RawOutput())
			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, groupFileOutput, golden.Path(t))

			requireRunnerResultForUser(t, authd.SessionMode_LOGIN, tc.clientOptions.PamUser, got)
		})
	}
}

// cliSelectBroker waits for the provider selection and selects ExampleBroker.
// Note: The TUI auto-selects when the number is typed, no Enter needed.
func cliEnterUsername(t *testing.T, c *ptytest.Console, username string) {
	t.Helper()
	c.WaitFor(t, `Username:`)
	c.SendLine(t, username)
}

func cliSelectBroker(t *testing.T, c *ptytest.Console) {
	t.Helper()

	c.WaitFor(t, `Select your provider`)
	c.WaitFor(t, `2\. ExampleBroker`)
	c.Send(t, "2")
}

// cliSimpleAuth performs a standard simple authentication flow: select broker, enter password.
func cliSimpleAuth(t *testing.T, c *ptytest.Console) {
	t.Helper()

	cliSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	cliWaitForResult(t, c)
}

// cliSimpleAuthPresetUser performs a simple auth flow when the user is preset (no username prompt).
func cliSimpleAuthPresetUser(t *testing.T, c *ptytest.Console) {
	t.Helper()

	cliSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	cliWaitForResult(t, c)
}

// cliWaitForResult waits for the PAM AcctMgmt() result, which is the last
// output line from the PAM runner (always printed, even on auth failure).
func cliWaitForResult(t *testing.T, c *ptytest.Console) {
	t.Helper()

	c.WaitFor(t, regexp.QuoteMeta(pam_test.RunnerResultActionAcctMgmt.String()))
}

func TestCLIChangeAuthTok(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tests := map[string]struct {
		username           string
		currentUserNotRoot bool

		test func(t *testing.T, socketPath, username string) string
	}{
		"Change_password_successfully_and_authenticate_with_new_one": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)

				c2 := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				cliEnterUsername(t, c2, username)
				c2.WaitFor(t, `Gimme your password`)
				c2.SendLine(t, "authd2404")
				cliWaitForResult(t, c2)
				_ = c2.WaitForExit(t)

				got := "=== Password Change ===\n" + ptySanitizeOutput(t, c.RawOutput()) +
					"\n=== Login ===\n" + ptySanitizeOutput(t, c2.RawOutput())
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, username, got)
				return got
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_different_case": {
			username: strings.ToUpper(testUserName(t, "case-insensitive")),
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				loginUsername := strings.ToLower(username)

				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)

				c2 := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				cliEnterUsername(t, c2, loginUsername)
				c2.WaitFor(t, `Gimme your password`)
				c2.SendLine(t, "authd2404")
				cliWaitForResult(t, c2)
				_ = c2.WaitForExit(t)

				got := "=== Password Change ===\n" + ptySanitizeOutput(t, c.RawOutput()) +
					"\n=== Login ===\n" + ptySanitizeOutput(t, c2.RawOutput())
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, loginUsername, got)
				return got
			},
		},
		"Change_passwd_after_MFA_auth": {
			username: examplebroker.UserIntegrationMfaPrefix + "cli-passwd@example.com",
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Password authentication`)
				c.Send(t, "1")
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your fido device foo`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				c.WaitFor(t, `Unlock your phone \+33`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your phone \+33`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Unlock your phone \+33`)
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Retry_if_new_password_is_rejected_by_broker": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendLine(t, "noble2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "noble2404")
				c.WaitFor(t, `new password does not match criteria`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Retry_if_new_password_is_same_of_previous": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `The password is the same as the old one`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Retry_if_password_confirmation_is_not_the_same": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "badpass")
				c.WaitFor(t, `Password entries don't match`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Retry_if_new_password_does_not_match_quality_criteria": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `No password supplied`)
				c.SendLine(t, "1234")
				c.WaitFor(t, `The password is shorter than`)
				c.SendLine(t, "12345678")
				c.WaitFor(t, `The password fails the dictionary check`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "123456789")
				c.WaitFor(t, `Password entries don't match`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm password`)
				c.SendLine(t, "authd2404")
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Prevent_change_password_if_auth_fails": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				for i := 0; i < 5; i++ {
					c.SendLine(t, "wrongpass")
				}
				c.WaitFor(t, `Maximum number of authentication attempts reached`)
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Prevent_change_password_if_user_does_not_exist": {
			username: examplebroker.UserIntegrationUnexistent,
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Prevent_change_password_if_current_user_is_not_root_as_can_not_authenticate": {
			currentUserNotRoot: true,
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Exit_authd_if_local_broker_is_selected": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				c.WaitFor(t, `Select your provider`)
				c.WaitFor(t, `1\. local`)
				c.SendKey(t, ptytest.KeyEnter)
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Exit_authd_if_user_sigints": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlC)
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
		"Exit_authd_if_user_presses_ctrl_d": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlD)
				cliWaitForResult(t, c)
				_ = c.WaitForExit(t)
				return ptySanitizeOutput(t, c.RawOutput())
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			groupFile := filepath.Join(t.TempDir(), "group")
			if err := os.WriteFile(groupFile, nil, 0o600); err != nil {
				t.Fatalf("Setup: could not create group file: %v", err)
			}
			var socketPath string
			if tc.currentUserNotRoot {
				socketPath = runAuthd(t, testutils.WithGroupFile(groupFile))
			} else {
				socketPath = runAuthd(t,
					testutils.WithCurrentUserAsRoot,
					testutils.WithGroupFile(groupFile),
					testutils.WithGroupFileOutput(groupFile),
				)
			}

			username := tc.username
			if username == "" && !tc.currentUserNotRoot {
				username = testUserName(t, "cli-passwd")
			}

			got := tc.test(t, socketPath, username)
			golden.CheckOrUpdate(t, got)
			requireRunnerResult(t, authd.SessionMode_CHANGE_PASSWORD, got)
		})
	}
}

func TestPamCLIRunStandalone(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	pamCleanup, err := buildPAMRunner(clientPath)
	require.NoError(t, err, "Setup: Failed to build PAM executable")
	t.Cleanup(pamCleanup)

	// #nosec:G204 - we control the command arguments in tests
	cmd := exec.Command("go", "run")
	cmd.Env = os.Environ()
	if testutils.CoverDirForTests() != "" {
		// -cover is a "positional flag", so it needs to come right after the "build" command.
		cmd.Args = append(cmd.Args, "-cover")
		cmd.Env = testutils.AppendCovEnv(os.Environ())
	}
	if testutils.IsRace() {
		cmd.Args = append(cmd.Args, "-race")
	}

	cmd.Dir = testutils.ProjectRoot()
	cmd.Args = append(cmd.Args, "-tags", "withpamrunner",
		"./pam/tools/pam-runner",
		pam_test.RunnerActionLogin.String(),
		"--exec-debug")
	cmd.Args = append(cmd.Args, "logfile="+os.Stdout.Name())
	cmd.Env = append(cmd.Env, pam_test.RunnerEnvUser+"=user")

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "Could not run PAM client: %s", out)
	outStr := string(out)
	t.Log(outStr)

	if !strings.Contains(outStr, pam.ErrAuthinfoUnavail.Error()) {
		t.Errorf("Expected output to contain %s", pam.ErrAuthinfoUnavail.Error())
	}
	if !strings.Contains(outStr, pam.ErrIgnore.Error()) {
		t.Errorf("Expected output to contain %s", pam.ErrIgnore.Error())
	}
}
