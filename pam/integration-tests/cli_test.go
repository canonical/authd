package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/testutils/ptytest"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	"github.com/canonical/authd/pam/internal/adapter"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

func TestCLIAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tests := map[string]struct {
		pamUser  string
		username string // typed at the Username: prompt (empty = use pamUser as preset)

		clientOptions      clientOptions
		currentUserNotRoot bool
		wantLocalGroups    bool
		expectedExitCode   int
		extraArgs          []string
		socketPath         string // override socket path
		useCancelableAuthd bool
		skipRunnerCheck    bool // skip the final runner-result assertion (use for tests that kill the runner)

		test func(t *testing.T, c *ptytest.Console)
		// testWithSignals is like test but receives a signalFn that creates a broker
		// completion signal for the given username, allowing tests to control when
		// wait-based authentication (FIDO, QR code, phone ack) completes.
		testWithSignals func(t *testing.T, c *ptytest.Console, signalFn func(username string))
		testWithAuthd   func(t *testing.T, c *ptytest.Console, cancelAuthd func())
		// testRun allows a test to control the entire test run (useful for
		// multi-session tests). When set, it handles all sessions and returns
		// the combined golden output. The test runner skips the default single-
		// session flow.
		testRun func(t *testing.T, socketPath string) string
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

				initialUsername := "user-integration-not-empty@example.com"
				finalUsername := "user-integration-was-empty@example.com"

				c.WaitFor(t, `Username:`)
				c.Send(t, initialUsername)
				c.WaitFor(t, `not-empty`)
				for i := 0; i < len(initialUsername); i++ {
					c.SendKey(t, ptytest.KeyBackspace)
				}
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `user name`)
				c.SendKey(t, ptytest.KeyEscape)
				c.SendKey(t, ptytest.KeyBackspace)
				c.Send(t, finalUsername)
				c.WaitFor(t, `was-empty`)
				c.SendKey(t, ptytest.KeyEnter)

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_mfa": {
			username: "user-mfa@example.com",
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
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
				cliSendPassword(t, c, "goodpass")

				// MFA: fido device.
				c.WaitFor(t, `Plug your fido device and press with your thumb`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your fido device foo`)

				c.SendKey(t, ptytest.KeyEnter) // Select first option
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				signalFn("user-mfa@example.com")

				// Auto-advances to phone after FIDO completes.
				c.WaitFor(t, `Unlock your phone \+33`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your phone \+33`)

				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Unlock your phone \+33`)
				signalFn("user-mfa@example.com")

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

				cliSendText(t, c, "temporary pass00")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_with_qr_code": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code@example.com",
			},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
				t.Helper()
				cliAuthenticateWithQRCode(t, c, signalFn, examplebroker.UserIntegrationPrefix+"qr-code@example.com")
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-tty@example.com",
				Term:    "linux",
			},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
				t.Helper()
				cliAuthenticateWithQRCode(t, c, signalFn, examplebroker.UserIntegrationPrefix+"qr-code-tty@example.com")
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY_session": {
			clientOptions: clientOptions{
				PamUser:     examplebroker.UserIntegrationPrefix + "qr-code-tty-session@example.com",
				Term:        "xterm-256color",
				SessionType: "tty",
			},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
				t.Helper()
				cliAuthenticateWithQRCode(t, c, signalFn, examplebroker.UserIntegrationPrefix+"qr-code-tty-session@example.com")
			},
		},
		"Authenticate_user_with_qr_code_in_screen": {
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-screen@example.com",
				Term:    "screen",
			},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
				t.Helper()
				cliAuthenticateWithQRCode(t, c, signalFn, examplebroker.UserIntegrationPrefix+"qr-code-screen@example.com")
			},
		},
		"Authenticate_user_with_qr_code_after_many_regenerations": {
			username: "user-integration-qrcode-static-regenerate@example.com",
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
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
				signalFn("user-integration-qrcode-static-regenerate@example.com")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			username: "user-needs-reset-integration-mandatory@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			testRun: func(t *testing.T, socketPath string) string {
				t.Helper()

				// Three case variants of the same user (matched case-insensitively by authd).
				baseUsername := testUserNameFull(t,
					examplebroker.UserIntegrationNeedsResetPrefix, "Case-INSENSITIVE")
				lowerUsername := strings.ToLower(baseUsername)
				upperUsername := strings.ToUpper(baseUsername)

				// cliStartLogin starts the PAM runner, enters the username, and
				// returns the console (before broker selection).
				cliStartLogin := func(username string) *ptytest.Console {
					c := startCLIPAMRunner(t, clientPath, socketPath,
						pam_test.RunnerActionLogin, cliEnv, clientOptions{})
					c.WaitFor(t, `Username:`)
					c.Send(t, username)
					c.WaitFor(t, regexp.QuoteMeta(username))
					c.SendKey(t, ptytest.KeyEnter)
					return c
				}

				// First login: lowercase username, broker selection, then
				// mandatory password reset.
				c1 := cliStartLogin(lowerUsername)
				cliSelectBroker(t, c1)
				c1.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c1, "goodpass")
				c1.WaitFor(t, `Password reset`)
				c1.WaitFor(t, `New password`)
				cliSendPassword(t, c1, "authd2404")
				c1.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c1, "authd2404")
				cliWaitForResult(t, c1)
				c1.RequireSuccessfulExit(t)

				// Second login: UPPERCASE username. Broker is remembered, so no
				// broker selection step.
				c2 := cliStartLogin(upperUsername)
				c2.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c2, "authd2404")
				cliWaitForResult(t, c2)
				c2.RequireSuccessfulExit(t)

				// Third login: mixed-case username (as returned by testUserNameFull).
				c3 := cliStartLogin(baseUsername)
				c3.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c3, "authd2404")
				cliWaitForResult(t, c3)
				c3.RequireSuccessfulExit(t)

				return "=== Login (lowercase, password reset) ===\n" + ptySanitizeSnapshots(t, c1) +
					"\n=== Login (UPPERCASE) ===\n" + ptySanitizeSnapshots(t, c2) +
					"\n=== Login (Mixed Case) ===\n" + ptySanitizeSnapshots(t, c3)
			},
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			username: "user-mfa-with-reset@example.com",
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(string)) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				signalFn("user-mfa-with-reset@example.com")
				// After FIDO auto-completes, capture the "1 step(s) missing" state.
				c.WaitFor(t, `Password reset, 1 step`)
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "password")
				c.WaitFor(t, `The password fails the dictionary check`)
				cliSendPassword(t, c, "1234")
				c.WaitFor(t, `The password is shorter than`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_and_offer_password_reset": {
			username: "user-can-reset@example.com",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")

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
				cliSendPassword(t, c, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_username": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				initialUsername := "user-integration-switch-username@example.com"
				updatedUsernameSuffix := "username-switched@example.com"
				clearedSuffix := "switch-username@example.com"

				// Type initial username and capture it before submitting.
				c.WaitFor(t, `Username:`)
				c.Send(t, initialUsername)
				c.WaitFor(t, `user-integration-switch-username@example`)
				c.SendKey(t, ptytest.KeyEnter)

				// Capture initial provider-selection (shows user flow before going back).
				c.WaitFor(t, `Select your provider`)

				// Go back to username.
				c.SendKey(t, ptytest.KeyEscape)
				// Discard pre-edit username snapshot (duplicate of initial username above).
				c.WaitFor(t, `Username:`)
				c.DiscardLastSnapshot()

				// Edit the username: remove "switch-username@example.com" and retype suffix.
				for i := 0; i < len(clearedSuffix); i++ {
					c.SendKey(t, ptytest.KeyBackspace)
				}
				c.Send(t, updatedUsernameSuffix)
				// Capture the final username before submitting.
				c.WaitFor(t, `user-integration-username-switched`)
				c.SendKey(t, ptytest.KeyEnter)

				// Select broker and authenticate.
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				cliWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_to_local_broker": {
			username:         "user-integration-switch-broker@example.com",
			expectedExitCode: 0,
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
			testRun: func(t *testing.T, socketPath string) string {
				t.Helper()

				username := "user-integration-remember-mode@example.com"

				// First login: select broker, then auth code mode.
				c := startCLIPAMRunner(t, clientPath, socketPath,
					pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				c.WaitFor(t, `Username:`)
				c.Send(t, username)
				c.WaitFor(t, regexp.QuoteMeta(username))
				c.SendKey(t, ptytest.KeyEnter)

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `7\. Authentication code`)

				c.Send(t, "7")
				c.WaitFor(t, `Enter your one time credential`)
				c.WaitFor(t, `Resend SMS \(1 sent\)`)

				cliSendText(t, c, "temporary pass0")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)

				// Second login: broker and mode should be remembered.
				c2 := startCLIPAMRunner(t, clientPath, socketPath,
					pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				c2.WaitFor(t, `Username:`)
				c2.Send(t, username)
				c2.WaitFor(t, regexp.QuoteMeta(username))
				c2.SendKey(t, ptytest.KeyEnter)

				// Should go directly to auth code (no broker/auth method selection).
				c2.WaitFor(t, `Enter your one time credential`)
				c2.WaitFor(t, `Resend SMS \(1 sent\)`)

				cliSendText(t, c2, "temporary pass0")
				cliWaitForResult(t, c2)
				c2.RequireSuccessfulExit(t)

				return "=== First Login ===\n" + ptySanitizeSnapshots(t, c) +
					"\n=== Second Login (broker/mode remembered) ===\n" + ptySanitizeSnapshots(t, c2)
			},
		},
		"Autoselect_local_broker_for_local_user": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Username:`)
				c.Send(t, "root")
				c.WaitFor(t, `Username: root`)
				c.SendKey(t, ptytest.KeyEnter)
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
				cliSendPassword(t, c, "goodpass")
				cliWaitForResult(t, c)
			},
		},

		"Deny_authentication_if_current_user_is_not_considered_as_root": {
			currentUserNotRoot: true,
			expectedExitCode:   0,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliWaitForResult(t, c)
			},
		},
		"Deny_authentication_if_max_attempts_reached": {
			username:         "user-integration-max-attempts@example.com",
			expectedExitCode: 0,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Send the wrong passwords. We use a different wrong password on each attempt
				// because bubbletea skips re-renders when the view content is identical.
				// By using a different password, we ensure that the
				// "Invalid password '<password>'" message is different, so that bubbletea
				// re-renders and we can WaitFor the new error message on each attempt.
				for i := 0; i < 4; i++ {
					c.SendLine(t, "wrongpass"+strconv.Itoa(i+1))
					c.WaitFor(t, `invalid password`)
					sanitizeTrailingPasswordEchoSnapshot(c)
				}

				c.SendLine(t, "wrongpass-final")
				c.WaitFor(t, `Maximum number of authentication attempts reached`)
				// The snapshot at this point is flaky: sometimes the PAM result
				// has already been rendered alongside the error message, sometimes
				// not. Discard it; cliWaitForResult captures the stable final state.
				c.DiscardLastSnapshot()
				cliWaitForResult(t, c)
			},
		},
		"Deny_authentication_if_user_does_not_exist": {
			username:         "user-unexistent@example.com",
			expectedExitCode: 0,
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
				cliSendPassword(t, c, "goodpass")

				c.WaitFor(t, `Password reset`)
				c.WaitFor(t, `New password`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `No password supplied`)
				cliSendPassword(t, c, "1234")
				c.WaitFor(t, `The password is shorter than`)
				cliSendPassword(t, c, "12345678")
				c.WaitFor(t, `The password fails the dictionary check`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "123456789")
				c.WaitFor(t, `Password entries don't match`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
			},
		},

		"Exit_authd_if_local_broker_is_selected": {
			username:         "user-local-broker",
			expectedExitCode: 0,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Select your provider`)
				c.WaitFor(t, `1\. local`)
				c.SendKey(t, ptytest.KeyEnter)
				cliWaitForResult(t, c)
			},
		},
		"Exit_authd_if_user_sigints": {
			username:         "user-integration-sigint@example.com",
			expectedExitCode: 0,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlC)
				cliWaitForResult(t, c)
			},
		},
		"Exit_authd_if_user_presses_ctrl_d": {
			username:         "user-integration-ctrl-d@example.com",
			expectedExitCode: 0,
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
			expectedExitCode:   0,
			testWithAuthd: func(t *testing.T, c *ptytest.Console, cancelAuthd func()) {
				t.Helper()

				c.WaitFor(t, `Username:`)
				cancelAuthd()
				c.WaitFor(t, `stopped serving`)
				// Discard the timing-sensitive "stopped serving" snapshot: it may or
				// may not include the PAM result depending on scheduling. The final
				// snapshot from cliWaitForResult always includes the full output.
				c.DiscardLastSnapshot()
				cliWaitForResult(t, c)
			},
		},
		//nolint:dupl // This is not a duplicate test
		"Exit_the_pam_client_if_parent_pam_application_is_stopped": {
			skipRunnerCheck:  true,
			expectedExitCode: 128 + int(syscall.SIGTERM),
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				c.WaitFor(t, `Username:`)

				parentPID := c.Pid()
				helperPID := findPAMExecChildPID(t, parentPID)
				t.Logf("Found %s helper child pid %d under PAM runner pid %d",
					pamExecChildName, helperPID, parentPID)

				runnerLogPath, ok := c.Env(pam_test.RunnerEnvLogFile)
				require.True(t, ok, "missing %s in pty runner environment", pam_test.RunnerEnvLogFile)
				t.Logf("Found %s logfile path: %s", pamExecChildName, runnerLogPath)

				// Kill the parent PAM application. This tears down the
				// private D-Bus server that the PAM module was hosting for
				// the helper, which is the condition the helper is supposed
				// to detect.
				c.Signal(t, syscall.SIGTERM)

				// The helper must terminate on its own once it sees the
				// disconnect.
				require.Eventually(t, func() bool {
					return syscall.Kill(helperPID, 0) == syscall.ESRCH
				}, sleepDuration(1*time.Second), 50*time.Millisecond,
					"authd-pam helper child (pid %d) was not terminated after parent was killed",
					helperPID)

				content, err := os.ReadFile(runnerLogPath)
				require.NoError(t, err, "failed to read PAM runner log file")
				require.Contains(t, string(content), "D-Bus Connection closed")
			},
		},

		"Error_if_cannot_connect_to_authd": {
			socketPath:       "/some-path/not-existent-socket",
			expectedExitCode: 0,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				// Discard the intermediate snapshot: the PAM error appears first but
				// Authenticate/AcctMgmt details may not be rendered yet, making the
				// snapshot non-deterministic. Only keep the final complete result.
				c.WaitFor(t, `could not connect to unix:`)
				c.DiscardLastSnapshot()
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

			var consoleOutput string
			if tc.testRun != nil {
				consoleOutput = tc.testRun(t, socketPath)
			} else {
				if tc.clientOptions.ClientType == nil {
					tc.clientOptions.ClientType = ptrValue(adapter.InteractiveTerminal)
				}

				c := startCLIPAMRunner(t, clientPath, socketPath,
					pam_test.RunnerActionLogin, cliEnv, tc.clientOptions, tc.extraArgs...)

				// If we have a typed username (not preset), enter it.
				if tc.username != "" && tc.clientOptions.PamUser == "" {
					c.WaitFor(t, `Username:`)
					// Type the username without Enter first, wait for the echo so
					// the snapshot captures "Username: <name>" on screen, then submit.
					c.Send(t, tc.username)
					c.WaitFor(t, regexp.QuoteMeta(tc.username))
					c.SendKey(t, ptytest.KeyEnter)
				}

				if tc.testWithSignals != nil {
					signalFn := func(username string) {
						testutils.CreateBrokerCompletionSignal(t, socketPath, username)
					}
					tc.testWithSignals(t, c, signalFn)
				} else if tc.testWithAuthd != nil {
					tc.testWithAuthd(t, c, cancelAuthd)
				} else if tc.test != nil {
					tc.test(t, c)
				}

				c.RequireExitCode(t, tc.expectedExitCode)

				consoleOutput = ptySanitizeSnapshots(t, c)
			}

			golden.CheckOrUpdate(t, consoleOutput)
			localgroupstestutils.RequireGroupFile(t, groupFileOutput, golden.Path(t))
			if !tc.skipRunnerCheck {
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, tc.clientOptions.PamUser, consoleOutput)
			}
		})
	}
}

// TestCLIAuthenticateRedirectedIO reproduces the cases in which the I/O streams
// of the PAM client are redirected or closed, ensuring that the CLI still
// prompts on the terminal (PAM_TTY) and the authentication flow works end-to-end.
func TestCLIAuthenticateRedirectedIO(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	tests := map[string]struct {
		redirections string
		detach       bool
	}{
		// `sudo ls > /dev/null` and the sshuttle stdout-to-socket path: stdout
		// is not the terminal, the prompt must still appear on the TTY.
		"Redirected_stdout": {redirections: ">/dev/null"},

		// stdout fully closed (>&-): the CLI must use the TTY for output.
		"Closed_stdout": {redirections: ">&-"},

		// stdin is /dev/null, input comes from the TTY.
		"Stdin_from_devnull": {redirections: "</dev/null"},

		// stdin is closed, input comes from the TTY.
		"Closed_stdin": {redirections: "<&-"},

		// Both stdin and stdout detached, only the TTY remains usable.
		"Closed_stdin_and_redirected_stdout": {redirections: "</dev/null >/dev/null"},

		// Stdin is closed and stdout *and* stderr are redirected to a non-terminal.
		// None of fd 0/1/2 is a terminal anymore.
		"All_streams_detached": {redirections: "<&- 1>/dev/null 2>/dev/null"},

		// No controlling terminal (setsid) and stdin detached: /dev/tt
		// input fallback is unavailable, so input must come from the
		// explicit PAM_TTY. This mirrors PAM clients without a controlling
		// terminal that still get a PAM_TTY.
		"Detached_session_input_from_pam_tty": {redirections: "</dev/null", detach: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			user := testUserName(t, "cli-redirected-io")

			runner := newRedirectedIORunner(t)
			c := runner.startRedirectedIORunner(t,
				redirectedIOScenario{
					ioRedirections:       tc.redirections,
					detachControllingTTY: tc.detach,
				},
				clientOptions{PamUser: user})

			cliSimpleAuth(t, c)
			c.RequireSuccessfulExit(t)

			consoleOutput := ptySanitizeSnapshots(t, c)
			golden.CheckOrUpdate(t, consoleOutput)
			runner.requireSuccess(t, authd.SessionMode_LOGIN, user)
		})
	}
}

// cliSelectBroker waits for the provider selection and selects ExampleBroker.
// Note: The TUI auto-selects when the number is typed, no Enter needed.
func cliEnterUsername(t *testing.T, c *ptytest.Console, username string) {
	t.Helper()
	c.WaitFor(t, `Username:`)
	c.Send(t, username)
	c.WaitFor(t, regexp.QuoteMeta(username))
	c.SendKey(t, ptytest.KeyEnter)
}

func cliSelectBroker(t *testing.T, c *ptytest.Console) {
	t.Helper()

	c.WaitFor(t, `Select your provider`)
	c.WaitFor(t, `2\. ExampleBroker`)
	c.Send(t, "2")
}

// cliSendPassword types a password into the current password prompt and submits it.
// cliSendPassword types password into the CLI password field, waits for the
// asterisks to be rendered (capturing an intermediate snapshot showing the
// masked input), then submits with Enter. Only use for single attempts — in
// retry loops the auth.Retry response may race with the typed characters
// inside bubbletea's event batch, causing the field to clear before any
// asterisks are rendered. Use c.SendLine for repeated wrong passwords.
func cliSendPassword(t *testing.T, c *ptytest.Console, password string) {
	t.Helper()

	c.Send(t, password)
	c.WaitFor(t, `\*+`)
	c.SendKey(t, ptytest.KeyEnter)
}

// cliSendText types text into a plain-text prompt (entries.Chars) and captures
// a snapshot showing the typed text before submitting.
func cliSendText(t *testing.T, c *ptytest.Console, text string) {
	t.Helper()

	c.Send(t, text)
	c.WaitFor(t, regexp.QuoteMeta(text))
	c.SendKey(t, ptytest.KeyEnter)
}

// cliSimpleAuth performs a standard simple authentication flow: select broker, enter password.
func cliSimpleAuth(t *testing.T, c *ptytest.Console) {
	t.Helper()

	cliSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	cliSendPassword(t, c, "goodpass")
	cliWaitForResult(t, c)
}

// cliSimpleAuthPresetUser performs a simple auth flow when the user is preset (no username prompt).
func cliSimpleAuthPresetUser(t *testing.T, c *ptytest.Console) {
	t.Helper()

	cliSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	cliSendPassword(t, c, "goodpass")
	cliWaitForResult(t, c)
}

// cliChangePasswordWithRetry performs the common "change password with one
// rejected attempt then success" flow. It sends firstNew/firstConfirm (which
// cause errMsg to appear), then retries with secondNew (confirmed identically).
func cliChangePasswordWithRetry(t *testing.T, c *ptytest.Console, firstNew, firstConfirm, errMsg, secondNew string) {
	t.Helper()

	c.WaitFor(t, `New password`)
	cliSendPassword(t, c, firstNew)
	c.WaitFor(t, `Confirm password`)
	cliSendPassword(t, c, firstConfirm)
	c.WaitFor(t, errMsg)
	cliSendPassword(t, c, secondNew)
	c.WaitFor(t, `Confirm password`)
	cliSendPassword(t, c, secondNew)
}

// cliWaitForResult waits for the complete PAM AcctMgmt() result block.
func cliWaitForResult(t *testing.T, c *ptytest.Console) {
	t.Helper()

	waitForRunnerResult(t, c, pam_test.RunnerResultActionAcctMgmt)
}

func cliAuthenticateWithQRCode(t *testing.T, c *ptytest.Console, signalFn func(string), username string) {
	t.Helper()

	cliSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendKey(t, ptytest.KeyEscape)
	c.WaitFor(t, `Select your authentication method`)
	c.WaitFor(t, `6\. Use a QR code`)
	c.Send(t, "6")
	c.WaitFor(t, `Scan the qrcode or enter the code in the login page`)
	// The QR view (label, QR matrix, URL, Code) is emitted as a single render,
	// but in a TTY it exceeds the PTY read buffer and arrives in several reads.
	// A WaitFor poll can therefore land between the chunk carrying the "Scan the
	// qrcode" label (top of the view) and the one carrying "Code:" (bottom),
	// snapshotting a half-drawn QR matrix. Discard that intermediate frame; the
	// "Code:" frame below is a complete superset that captures the same screen.
	c.DiscardLastSnapshot()
	c.WaitFor(t, `Code:\s*1337`)
	// The Regenerate button has a 500ms reselectionWaitTime guard to
	// prevent accidental double-clicks right after mode selection.
	// Wait past it before pressing Enter to trigger code regeneration.
	time.Sleep(testutils.MultipliedSleepDuration(550 * time.Millisecond))
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `Code:\s*1338`)
	time.Sleep(testutils.MultipliedSleepDuration(550 * time.Millisecond))
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `Code:\s*1339`)
	time.Sleep(testutils.MultipliedSleepDuration(550 * time.Millisecond))
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `Code:\s*1340`)
	time.Sleep(testutils.MultipliedSleepDuration(550 * time.Millisecond))
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `Code:\s*1341`)
	signalFn(username)
	cliWaitForResult(t, c)
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
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)

				c2 := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				cliEnterUsername(t, c2, username)
				c2.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c2, "authd2404")
				cliWaitForResult(t, c2)
				c2.RequireSuccessfulExit(t)

				got := "=== Password Change ===\n" + ptySanitizeSnapshots(t, c) +
					"\n=== Login ===\n" + ptySanitizeSnapshots(t, c2)
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, username, got)
				return got
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_different_case": {
			username: strings.ToUpper(testUserName(t, "case-insensitive")),
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				loginUsername := strings.ToLower(username)

				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)

				c2 := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionLogin, cliEnv, clientOptions{})
				cliEnterUsername(t, c2, loginUsername)
				c2.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c2, "authd2404")
				cliWaitForResult(t, c2)
				c2.RequireSuccessfulExit(t)

				got := "=== Password Change ===\n" + ptySanitizeSnapshots(t, c) +
					"\n=== Login ===\n" + ptySanitizeSnapshots(t, c2)
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, loginUsername, got)
				return got
			},
		},
		"Change_passwd_after_MFA_auth": {
			username: examplebroker.UserIntegrationMfaPrefix + "cli-passwd@example.com",
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Password authentication`)
				c.Send(t, "1")
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your fido device foo`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Plug your fido device and press with your thumb`)
				testutils.CreateBrokerCompletionSignal(t, socketPath, username)
				c.WaitFor(t, `Unlock your phone \+33`)
				c.SendKey(t, ptytest.KeyEscape)
				c.WaitFor(t, `Select your authentication method`)
				c.WaitFor(t, `1\. Use your phone \+33`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `Unlock your phone \+33`)
				testutils.CreateBrokerCompletionSignal(t, socketPath, username)
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Retry_if_new_password_is_rejected_by_broker": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c1 := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c1, username)
				cliSelectBroker(t, c1)
				c1.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c1, "goodpass")
				cliChangePasswordWithRetry(t, c1, "noble2404", "noble2404",
					`new password does not match criteria`, "authd2404")
				cliWaitForResult(t, c1)
				c1.RequireSuccessfulExit(t)

				// Repeat the flow to verify that after a rejection, the user can still change the password successfully.
				c2 := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				c2.WaitFor(t, `Username:`)
				c2.Send(t, username)
				c2.WaitFor(t, regexp.QuoteMeta(username))
				c2.SendKey(t, ptytest.KeyEnter)
				c2.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c2, "authd2404")
				cliChangePasswordWithRetry(t, c2, "noble2404", "noble2404",
					`new password does not match criteria`, "goodpass")
				cliWaitForResult(t, c2)
				c2.RequireSuccessfulExit(t)

				return ptySanitizeSnapshots(t, c1) + ptySanitizeSnapshots(t, c2)
			},
		},
		"Retry_if_new_password_is_same_of_previous": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `New password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `The password is the same as the old one`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Retry_if_password_confirmation_is_not_the_same": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				cliChangePasswordWithRetry(t, c, "authd2404", "badpass",
					`Password entries don't match`, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Retry_if_new_password_does_not_match_quality_criteria": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				cliSendPassword(t, c, "goodpass")
				c.WaitFor(t, `New password`)
				c.SendKey(t, ptytest.KeyEnter)
				c.WaitFor(t, `No password supplied`)
				cliSendPassword(t, c, "1234")
				c.WaitFor(t, `The password is shorter than`)
				cliSendPassword(t, c, "12345678")
				c.WaitFor(t, `The password fails the dictionary check`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "123456789")
				c.WaitFor(t, `Password entries don't match`)
				cliSendPassword(t, c, "authd2404")
				c.WaitFor(t, `Confirm password`)
				cliSendPassword(t, c, "authd2404")
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Prevent_change_password_if_auth_fails": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)

				// Send the wrong passwords. We use a different wrong password on each attempt
				// because bubbletea skips re-renders when the view content is identical.
				// By using a different password, we ensure that the
				// "Invalid password '<password>'" message is different, so that bubbletea
				// re-renders and we can WaitFor the new error message on each attempt.
				for i := 0; i < 4; i++ {
					c.SendLine(t, "wrongpass"+strconv.Itoa(i+1))
					c.WaitFor(t, `invalid password`)
					sanitizeTrailingPasswordEchoSnapshot(c)
				}

				c.SendLine(t, "wrongpass-final")
				c.WaitFor(t, `Maximum number of authentication attempts reached`)
				// The snapshot at this point is flaky: sometimes the PAM result
				// has already been rendered alongside the error message, sometimes
				// not. Discard it; cliWaitForResult captures the stable final state.
				c.DiscardLastSnapshot()
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Prevent_change_password_if_user_does_not_exist": {
			username: examplebroker.UserIntegrationUnexistent,
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Prevent_change_password_if_current_user_is_not_root_as_can_not_authenticate": {
			currentUserNotRoot: true,
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Exit_authd_if_local_broker_is_selected": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				c.WaitFor(t, `Select your provider`)
				c.WaitFor(t, `1\. local`)
				c.SendKey(t, ptytest.KeyEnter)
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Exit_authd_if_user_sigints": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlC)
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
			},
		},
		"Exit_authd_if_user_presses_ctrl_d": {
			test: func(t *testing.T, socketPath, username string) string {
				t.Helper()
				c := startCLIPAMRunner(t, clientPath, socketPath, pam_test.RunnerActionPasswd, cliEnv, clientOptions{})
				cliEnterUsername(t, c, username)
				cliSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password`)
				c.SendKey(t, ptytest.KeyCtrlD)
				cliWaitForResult(t, c)
				c.RequireSuccessfulExit(t)
				return ptySanitizeSnapshots(t, c)
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
