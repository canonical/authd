package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/stretchr/testify/require"
)

type nativePtySessionRunner struct {
	clientPath string
	socketPath string
	cliEnv     []string
}

type nativePtySessionSpec struct {
	action           pam_test.RunnerAction
	clientOptions    clientOptions
	username         string
	extraArgs        []string
	expectedExitCode int
}

type nativePtyTestContext struct {
	runner      nativePtySessionRunner
	baseSpec    nativePtySessionSpec
	rawOutputs  []string
	authdCancel func()
}

func (r nativePtySessionRunner) start(t *testing.T, spec nativePtySessionSpec) *ptytest.Console {
	t.Helper()

	// Native PAM client uses text-based prompts via PAM conversation, so it
	// needs the pam-runner to support conversations (unlike the CLI client
	// which handles all interaction via its own bubbletea TUI).
	cliEnv := append(r.cliEnv,
		fmt.Sprintf("%s=1", pam_test.RunnerEnvSupportsConversation),
	)
	extraArgs := append([]string{"force_native_client=true"}, spec.extraArgs...)
	c := startPAMRunner(t, r.clientPath, r.socketPath, spec.action, cliEnv, spec.clientOptions, extraArgs...)
	if spec.username != "" && spec.clientOptions.PamUser == "" {
		nativeEnterUsername(t, c, spec.username)
	}

	return c
}

func (ctx *nativePtyTestContext) waitForExitAndCapture(t *testing.T, c *ptytest.Console, expectedExitCode int) {
	t.Helper()

	c.RequireExitCode(t, expectedExitCode)
	ctx.rawOutputs = append(ctx.rawOutputs, c.RawOutput())
}

func (ctx *nativePtyTestContext) run(t *testing.T, spec nativePtySessionSpec, test func(t *testing.T, c *ptytest.Console)) {
	t.Helper()

	c := ctx.runner.start(t, spec)
	if test != nil {
		test(t, c)
	}
	ctx.waitForExitAndCapture(t, c, spec.expectedExitCode)
}

func TestNativeAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tests := map[string]struct {
		username string

		clientOptions      clientOptions
		currentUserNotRoot bool
		wantLocalGroups    bool
		wantSeparateDaemon bool
		skipRunnerCheck    bool
		socketPath         string
		extraArgs          []string
		expectedUser       string
		expectedExitCode   int

		test            func(t *testing.T, c *ptytest.Console)
		testWithSignals func(t *testing.T, c *ptytest.Console, signalFn func(username string))
		after           func(t *testing.T, ctx *nativePtyTestContext)
	}{
		"Authenticate_user_successfully": {
			test:         nativeSimpleAuth,
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_successfully_with_upper_case": {
			clientOptions: clientOptions{PamUser: strings.ToUpper(testUserName(t, "upper-case-native"))},
			test:          nativeSimpleAuth,
			expectedUser:  strings.ToUpper(testUserName(t, "upper-case-native")),
		},
		"Authenticate_user_successfully_with_user_selection": {
			username:     testUserName(t, "user-selection-native"),
			test:         nativeSimpleAuth,
			expectedUser: testUserName(t, "user-selection-native"),
		},
		"Authenticate_user_successfully_using_upper_case_with_user_selection": {
			username:     strings.ToUpper(testUserName(t, "selection-upper-case-native")),
			test:         nativeSimpleAuth,
			expectedUser: strings.ToUpper(testUserName(t, "selection-upper-case-native")),
		},
		"Authenticate_user_successfully_with_invalid_connection_timeout": {
			clientOptions: clientOptions{PamTimeout: "invalid"},
			test:          nativeSimpleAuth,
			expectedUser:  testUserName(t, "invalid-timeout-native"),
		},
		"Authenticate_user_successfully_with_password_only_supported_method": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password-integration-native")},
			test:          nativeSimpleAuth,
			expectedUser:  testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password-integration-native"),
		},
		"Authenticate_user_successfully_with_password_only_supported_method_in_polkit": {
			clientOptions: clientOptions{
				PamServiceName: "polkit-1",
				PamUser:        testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password-integration-polkit-native"),
			},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password-integration-polkit-native"),
		},
		"Authenticate_user_with_mfa": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "auth-native")},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				username := testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "auth-native")
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "1")
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "1")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "2")
				c.WaitFor(t, regexp.QuoteMeta(`Unlock your phone +33... or accept request on web interface:`))
				signalFn(username)
				c.SendLine(t, "")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				signalFn(username)
				c.SendLine(t, "")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "auth-native"),
		},
		"Authenticate_user_with_form_mode_with_button": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "8")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				c.WaitFor(t, `Enter your one time credential:`)
				sendEchoedLine(t, c, "temporary pass00")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_form_mode_with_button_two_supported_methods": {
			clientOptions: clientOptions{PamUser: examplebroker.UserIntegrationAuthModesPrefix + "totp_with_button,password-integration-native@example.com"},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				c.WaitFor(t, `Enter your one time credential:`)
				sendEchoedLine(t, c, "temporary pass00")
				nativeWaitForResult(t, c)
			},
			expectedUser: examplebroker.UserIntegrationAuthModesPrefix + "totp_with_button,password-integration-native@example.com",
		},
		"Authenticate_user_with_form_mode_with_button_in_polkit": {
			clientOptions: clientOptions{PamServiceName: "polkit-1"},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "7")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				c.WaitFor(t, `Enter your one time credential:`)
				sendEchoedLine(t, c, "temporary pass00")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_qr_code": {
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "7", testUserName(t, "native"), signalFn)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_qr_code_without_code": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationQRcodeWithoutCodePrefix, "native")},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "7", testUserNameFull(t, examplebroker.UserIntegrationQRcodeWithoutCodePrefix, "native"), signalFn)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationQRcodeWithoutCodePrefix, "native"),
		},
		"Authenticate_user_with_qr_code_in_a_TTY": {
			clientOptions: clientOptions{Term: "linux"},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "7", testUserName(t, "native"), signalFn)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_qr_code_in_a_TTY_session": {
			clientOptions: clientOptions{Term: "xterm-256color", SessionType: "tty"},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "7", testUserName(t, "native"), signalFn)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_qr_code_in_screen": {
			clientOptions: clientOptions{Term: "screen"},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "7", testUserName(t, "native"), signalFn)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_with_qr_code_in_ssh": {
			clientOptions: clientOptions{
				PamUser:        testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-service-qr-code-native"),
				PamServiceName: "sshd",
			},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				nativeQRCodeAuth(t, c, "2", testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-service-qr-code-native"), signalFn)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-service-qr-code-native"),
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "mandatory-native")},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "mandatory-native"),
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			username:     testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "case-insensitive-native"),
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "case-insensitive-native"),
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForResult(t, c)
			},
			after: func(t *testing.T, ctx *nativePtyTestContext) {
				t.Helper()

				upper := strings.ToUpper(ctx.baseSpec.username)
				mixed := strings.Replace(ctx.baseSpec.username, "case-insensitive", "Case-INSENSITIVE", 1)
				for _, username := range []string{upper, mixed} {
					ctx.run(t, nativePtySessionSpec{action: pam_test.RunnerActionLogin, username: username}, func(t *testing.T, c *ptytest.Console) {
						t.Helper()
						nativeWaitForLoginPasswordPrompt(t, c)
						c.SendLine(t, "authd2404")
						nativeWaitForResult(t, c)
					})
				}
			},
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "pwquality-native")},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				username := testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "pwquality-native")
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				signalFn(username)
				c.SendLine(t, "")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				c.WaitFor(t, `Enter your new password`)
				c.SendLine(t, "password")
				c.WaitFor(t, `The password fails the dictionary check`)
				c.WaitFor(t, `Enter your new password`)
				c.SendLine(t, "1234")
				c.WaitFor(t, `The password is shorter than`)
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "pwquality-native"),
		},
		"Authenticate_user_with_mfa_and_reset_same_password": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "same-password-native")},
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				username := testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "same-password-native")
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				signalFn(username)
				c.SendLine(t, "")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationMfaWithResetPrefix, "same-password-native"),
		},
		"Authenticate_user_and_offer_password_reset": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationCanResetPrefix, "skip-native")},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "2")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationCanResetPrefix, "skip-native"),
		},
		"Authenticate_user_and_accept_password_reset": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationCanResetPrefix, "accept-native")},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationCanResetPrefix, "accept-native"),
		},
		"Authenticate_user_switching_auth_mode": {
			clientOptions: clientOptions{PamUser: testUserName(t, "switch-mode-native")},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				for _, choice := range []string{"2", "3", "4", "5", "6", "7", "8"} {
					c.WaitFor(t, `Choose your authentication method:`)
					sendEchoedLine(t, c, choice)
					switch choice {
					case "2":
						c.WaitFor(t, `Click on the link received at`)
						sendEchoedLine(t, c, "r")
					case "3":
						c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
						sendEchoedLine(t, c, "r")
					case "4", "5":
						c.WaitFor(t, `Unlock your phone`)
						sendEchoedLine(t, c, "r")
					case "6":
						c.WaitFor(t, `Enter your pin code:`)
						sendEchoedLine(t, c, "r")
					case "7":
						c.WaitFor(t, `Choose action:`)
						sendEchoedLine(t, c, "r")
					case "8":
						c.WaitFor(t, `Choose action:`)
						sendEchoedLine(t, c, "r")
					}
				}
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "invalid-selection")
				c.WaitFor(t, `PAM Error Message: Unsupported input`)
				sendEchoedLine(t, c, "-1")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "6")
				c.WaitFor(t, `Enter your pin code:`)
				sendEchoedLine(t, c, "4242")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "switch-mode-native"),
		},
		"Authenticate_user_switching_username": {
			username: testUserName(t, "native-username"),
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `(?s)== Provider selection ==.*Choose your provider:`)
				sendEchoedLine(t, c, "r")
				nativeEnterUsername(t, c, testUserName(t, "native-username-switched"))
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeWaitForResult(t, c)
			},
		},
		"Authenticate_user_switching_to_local_broker": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "invalid-ID")
				c.WaitFor(t, `PAM Error Message: Unsupported input`)
				sendEchoedLine(t, c, "555")
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "1")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Authenticate_user_and_add_it_to_local_group": {
			clientOptions:   clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationLocalGroupsPrefix, "auth-native")},
			wantLocalGroups: true,
			test:            nativeSimpleAuth,
			expectedUser:    testUserNameFull(t, examplebroker.UserIntegrationLocalGroupsPrefix, "auth-native"),
		},
		"Authenticate_user_on_ssh_service": {
			clientOptions: clientOptions{
				PamUser:        testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-service-native"),
				PamServiceName: "sshd",
			},
			test:         nativeSimpleAuth,
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-service-native"),
		},
		"Authenticate_user_on_ssh_service_with_custom_name_and_connection_env": {
			clientOptions: clientOptions{
				PamUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-connection-native"),
				PamEnv:  []string{"SSH_CONNECTION=foo-connection"},
			},
			test:         nativeSimpleAuth,
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-connection-native"),
		},
		"Authenticate_user_on_ssh_service_with_custom_name_and_auth_info_env": {
			clientOptions: clientOptions{
				PamUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-auth-info-native"),
				PamEnv:  []string{"SSH_AUTH_INFO_0=foo-authinfo"},
			},
			test:         nativeSimpleAuth,
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationPreCheckPrefix, "ssh-auth-info-native"),
		},
		"Authenticate_with_warnings_on_unsupported_arguments": {
			extraArgs:    []string{"invalid_flag=foo", "bar"},
			test:         nativeSimpleAuth,
			expectedUser: testUserName(t, "native"),
		},
		"Remember_last_successful_broker_and_mode": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "8")
				c.WaitFor(t, `Choose action:`)
				sendEchoedLine(t, c, "1")
				c.WaitFor(t, `Enter your one time credential:`)
				sendEchoedLine(t, c, "temporary pass0")
				nativeWaitForResult(t, c)
			},
			after: func(t *testing.T, ctx *nativePtyTestContext) {
				t.Helper()
				ctx.run(t, nativePtySessionSpec{action: pam_test.RunnerActionLogin, clientOptions: clientOptions{PamUser: testUserName(t, "native")}}, func(t *testing.T, c *ptytest.Console) {
					t.Helper()
					c.WaitFor(t, `Choose action:`)
					sendEchoedLine(t, c, "1")
					c.WaitFor(t, `Enter your one time credential:`)
					sendEchoedLine(t, c, "temporary pass0")
					nativeWaitForResult(t, c)
				})
			},
			expectedUser: testUserName(t, "native"),
		},
		"Autoselect_local_broker_for_local_user": {
			username:     "root",
			test:         func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser: "root",
		},
		"Autoselect_local_broker_for_local_user_on_polkit": {
			username:      "root",
			clientOptions: clientOptions{PamServiceName: "polkit-1"},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  "root",
		},
		"Autoselect_local_broker_for_local_user_preset": {
			clientOptions: clientOptions{PamUser: "root"},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  "root",
		},
		"Autoselect_local_broker_for_local_user_preset_on_polkit": {
			clientOptions: clientOptions{PamServiceName: "polkit-1", PamUser: "root"},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  "root",
		},
		"Deny_authentication_if_current_user_is_not_considered_as_root": {
			currentUserNotRoot: true,
			test:               func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:       testUserName(t, "native"),
		},
		"Deny_authentication_if_max_attempts_reached": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				for i := 0; i < 5; i++ {
					c.WaitFor(t, `Gimme your password:`)
					c.SendLine(t, "wrongpass")
				}
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Deny_authentication_if_user_does_not_exist": {
			clientOptions: clientOptions{PamUser: examplebroker.UserIntegrationUnexistent},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				nativeWaitForResult(t, c)
			},
			expectedUser: examplebroker.UserIntegrationUnexistent,
		},
		"Deny_authentication_if_user_does_not_exist_and_matches_cancel_key": {
			username:     "r",
			test:         nativeSimpleAuth,
			expectedUser: "r",
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			clientOptions: clientOptions{PamUser: testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "bad-password-native")},
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "short")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "password")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm Password:`)
				c.SendLine(t, "mismatch")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm Password:`)
				c.SendLine(t, "authd2404")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationNeedsResetPrefix, "bad-password-native"),
		},
		"Prevent_preset_user_from_switching_username": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Unsupported input`)
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Unsupported input`)
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Unsupported input`)
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "2")
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Exit_authd_if_local_broker_is_selected": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "1")
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Exit_if_user_is_not_pre-checked_on_ssh_service": {
			clientOptions: clientOptions{PamServiceName: "sshd"},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  testUserName(t, "native"),
		},
		"Exit_if_user_is_not_pre-checked_on_custom_ssh_service_with_connection_env": {
			clientOptions: clientOptions{PamEnv: []string{"SSH_CONNECTION=foo-connection"}},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  testUserName(t, "native"),
		},
		"Exit_if_user_is_not_pre-checked_on_custom_ssh_service_with_auth_info_env": {
			clientOptions: clientOptions{PamEnv: []string{"SSH_AUTH_INFO_0=foo-authinfo"}},
			test:          func(t *testing.T, c *ptytest.Console) { t.Helper(); nativeWaitForResult(t, c) },
			expectedUser:  testUserName(t, "native"),
		},
		"Exit_authd_if_user_sigints": {
			skipRunnerCheck:  true,
			expectedExitCode: 130,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendKey(t, ptytest.KeyCtrlC)
			},
			expectedUser: testUserName(t, "native"),
		},
		"Exit_if_authd_is_stopped": {
			wantSeparateDaemon: true,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Choose your provider:`)
			},
			after: func(t *testing.T, ctx *nativePtyTestContext) {
				t.Helper()
				if ctx.authdCancel != nil {
					ctx.authdCancel()
				}
			},
			expectedUser: testUserName(t, "native"),
		},
		//nolint:dupl // This is not a duplicate test
		"Exit_the_pam_client_if_parent_pam_application_is_stopped": {
			skipRunnerCheck:  true,
			expectedExitCode: -1,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()

				c.WaitFor(t, `Choose your provider:`)

				parentPID := c.Pid()
				helperPID := findPAMExecChildPID(t, parentPID)
				t.Logf("Found %s helper child pid %d under PAM runner pid %d",
					pamExecChildName, helperPID, parentPID)

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
			},
		},
		"Error_if_cannot_connect_to_authd": {
			socketPath: "/some-path/not-existent-socket",
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `could not connect to unix:`)
				nativeWaitForResult(t, c)
			},
			expectedUser: testUserName(t, "native"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var socketPath, groupFileOutput string
			var authdCancel func()
			switch {
			case tc.wantSeparateDaemon:
				var groupFile string
				groupFileOutput, groupFile = prepareGroupFiles(t)
				socketPath, authdCancel = runAuthdForTestingWithCancel(t, false,
					testutils.WithCurrentUserAsRoot,
					testutils.WithGroupFile(groupFile),
					testutils.WithGroupFileOutput(groupFileOutput),
				)
				t.Cleanup(authdCancel)
			case tc.wantLocalGroups || tc.currentUserNotRoot:
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
				socketPath = runAuthd(t, args...)
			default:
				socketPath, groupFileOutput = sharedAuthd(t)
			}
			if tc.socketPath != "" {
				socketPath = tc.socketPath
			}

			clientOptions := tc.clientOptions
			username := tc.username
			expectedUser := tc.expectedUser
			if clientOptions.PamUser == "" && username == "" {
				clientOptions.PamUser = testUserName(t, "native")
			}
			if tc.clientOptions.PamUser == "" && tc.username == "" {
				expectedUser = clientOptions.PamUser
			}
			if expectedUser == "" {
				switch {
				case username != "":
					expectedUser = username
				case clientOptions.PamUser != "":
					expectedUser = clientOptions.PamUser
				}
			}

			ctx := &nativePtyTestContext{
				runner: nativePtySessionRunner{
					clientPath: clientPath,
					socketPath: socketPath,
					cliEnv:     cliEnv,
				},
				baseSpec: nativePtySessionSpec{
					action:        pam_test.RunnerActionLogin,
					clientOptions: clientOptions,
					username:      username,
					extraArgs:     tc.extraArgs,
				},
				authdCancel: authdCancel,
			}

			c := ctx.runner.start(t, ctx.baseSpec)
			if tc.testWithSignals != nil {
				signalFn := func(_ string) {
					testutils.CreateBrokerCompletionSignal(t, socketPath, expectedUser)
				}
				tc.testWithSignals(t, c, signalFn)
			} else if tc.test != nil {
				tc.test(t, c)
			}
			if name == "Exit_if_authd_is_stopped" && ctx.authdCancel != nil {
				ctx.authdCancel()
				ctx.authdCancel = nil
				sendEchoedLine(t, c, "2")
				nativeWaitForResult(t, c)
			}
			if name == "Authenticate_user_switching_username" {
				expectedUser = testUserName(t, "native-username-switched")
			}
			ctx.waitForExitAndCapture(t, c, tc.expectedExitCode)
			if tc.after != nil {
				tc.after(t, ctx)
			}

			got := ptySanitizeOutput(t, strings.Join(ctx.rawOutputs, "\n"))
			golden.CheckOrUpdate(t, got)
			localgroupstestutils.RequireGroupFile(t, groupFileOutput, golden.Path(t))
			if !tc.skipRunnerCheck {
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, expectedUser, got)
			}
		})
	}
}

func TestNativeChangeAuthTok(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	// This test is flaky, see https://github.com/canonical/authd/issues/1330
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tests := map[string]struct {
		username string

		clientOptions      clientOptions
		currentUserNotRoot bool
		skipRunnerCheck    bool
		expectedUser       string
		expectedExitCode   int

		test            func(t *testing.T, c *ptytest.Console)
		testWithSignals func(t *testing.T, c *ptytest.Console, signalFn func(username string))
		after           func(t *testing.T, ctx *nativePtyTestContext)
	}{
		"Change_password_successfully_and_authenticate_with_new_one": {
			username:     testUserName(t, "simple"),
			expectedUser: testUserName(t, "simple"),
			test:         nativePasswdSimpleChange,
			after:        nativeReloginAfterPasswordChange,
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_single_broker_and_password_only_supported_method": {
			clientOptions: clientOptions{
				PamServiceName: "polkit-1",
				PamUser:        testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password,mandatoryreset-integration-polkit"),
			},
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationAuthModesPrefix, "password,mandatoryreset-integration-polkit"),
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
			after: func(t *testing.T, ctx *nativePtyTestContext) {
				t.Helper()
				ctx.run(t, nativePtySessionSpec{action: pam_test.RunnerActionLogin, clientOptions: ctx.baseSpec.clientOptions}, func(t *testing.T, c *ptytest.Console) {
					t.Helper()
					nativeWaitForLoginPasswordPrompt(t, c)
					c.SendLine(t, "authd2404")
					nativeWaitForResult(t, c)
				})
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_different_case": {
			username:     testUserName(t, "case-insensitive"),
			expectedUser: testUserName(t, "case-insensitive"),
			test:         nativePasswdSimpleChange,
			after:        nativeReloginAfterPasswordChange,
		},
		"Change_passwd_after_MFA_auth": {
			username:     testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "native-passwd"),
			expectedUser: testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "native-passwd"),
			testWithSignals: func(t *testing.T, c *ptytest.Console, signalFn func(username string)) {
				t.Helper()
				username := testUserNameFull(t, examplebroker.UserIntegrationMfaPrefix, "native-passwd")
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "1")
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "1")
				c.WaitFor(t, regexp.QuoteMeta(`Plug your fido device and press with your thumb:`))
				signalFn(username)
				c.SendLine(t, "")
				c.WaitFor(t, regexp.QuoteMeta(`Unlock your phone +33... or accept request on web interface:`))
				sendEchoedLine(t, c, "r")
				c.WaitFor(t, `Choose your authentication method:`)
				c.SendLine(t, "1")
				c.WaitFor(t, regexp.QuoteMeta(`Unlock your phone +33... or accept request on web interface:`))
				signalFn(username)
				c.SendLine(t, "")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Retry_if_new_password_is_rejected_by_broker": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeChangePassword(t, c, "wrongpass", "wrongpass")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Retry_if_new_password_is_same_of_previous": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm Password:`)
				c.SendLine(t, "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Retry_if_password_confirmation_is_not_the_same": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				nativeChangePassword(t, c, "authd2404", "mismatch")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Retry_if_new_password_does_not_match_quality_criteria": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendLine(t, "goodpass")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "short")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "password")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "authd2404")
				c.WaitFor(t, `Confirm Password:`)
				c.SendLine(t, "mismatch")
				c.WaitFor(t, `Enter your new password:`)
				c.SendLine(t, "")
				nativeChangePassword(t, c, "authd2404", "authd2404")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Prevent_change_password_if_auth_fails": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				for i := 0; i < 5; i++ {
					c.WaitFor(t, `Gimme your password:`)
					c.SendLine(t, "wrongpass")
				}
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Prevent_change_password_if_user_does_not_exist": {
			username:     examplebroker.UserIntegrationUnexistent,
			expectedUser: examplebroker.UserIntegrationUnexistent,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Prevent_change_password_if_current_user_is_not_root_as_can_not_authenticate": {
			currentUserNotRoot: true,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Exit_authd_if_local_broker_is_selected": {
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				c.WaitFor(t, `Choose your provider:`)
				sendEchoedLine(t, c, "1")
				nativeWaitForChangeAuthTokResult(t, c)
			},
		},
		"Exit_authd_if_user_sigints": {
			skipRunnerCheck:  true,
			expectedExitCode: 130,
			test: func(t *testing.T, c *ptytest.Console) {
				t.Helper()
				nativeSelectBroker(t, c)
				c.WaitFor(t, `Gimme your password:`)
				c.SendKey(t, ptytest.KeyCtrlC)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var socketPath string
			if tc.currentUserNotRoot {
				socketPath = runAuthd(t, testutils.WithGroupFile(filepath.Join(t.TempDir(), "group")))
			} else {
				socketPath, _ = sharedAuthd(t)
			}

			clientOptions := tc.clientOptions
			username := tc.username
			expectedUser := tc.expectedUser
			if clientOptions.PamUser == "" && username == "" && !tc.currentUserNotRoot {
				username = testUserName(t, "native-passwd")
			}
			if expectedUser == "" {
				switch {
				case username != "":
					expectedUser = username
				case clientOptions.PamUser != "":
					expectedUser = clientOptions.PamUser
				}
			}

			ctx := &nativePtyTestContext{runner: nativePtySessionRunner{clientPath: clientPath, socketPath: socketPath, cliEnv: cliEnv}, baseSpec: nativePtySessionSpec{
				action:        pam_test.RunnerActionPasswd,
				clientOptions: clientOptions,
				username:      username,
			}}
			c := ctx.runner.start(t, ctx.baseSpec)
			if tc.testWithSignals != nil {
				signalFn := func(_ string) {
					testutils.CreateBrokerCompletionSignal(t, socketPath, expectedUser)
				}
				tc.testWithSignals(t, c, signalFn)
			} else if tc.test != nil {
				tc.test(t, c)
			}
			ctx.waitForExitAndCapture(t, c, tc.expectedExitCode)
			if tc.after != nil {
				tc.after(t, ctx)
			}

			got := ptySanitizeOutput(t, strings.Join(ctx.rawOutputs, "\n"))
			golden.CheckOrUpdate(t, got)
			if !tc.skipRunnerCheck {
				requireRunnerResultForUser(t, authd.SessionMode_CHANGE_PASSWORD, expectedUser, got)
			}
		})
	}
}

func nativeEnterUsername(t *testing.T, c *ptytest.Console, username string) {
	t.Helper()
	c.WaitFor(t, `Username: `)
	c.SendLine(t, username)
}

func nativePasswdSimpleChange(t *testing.T, c *ptytest.Console) {
	t.Helper()
	nativeSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password:`)
	c.SendLine(t, "goodpass")
	nativeChangePassword(t, c, "authd2404", "authd2404")
	nativeWaitForChangeAuthTokResult(t, c)
}

func nativeReloginAfterPasswordChange(t *testing.T, ctx *nativePtyTestContext) {
	t.Helper()
	ctx.run(t, nativePtySessionSpec{action: pam_test.RunnerActionLogin, username: ctx.baseSpec.username}, func(t *testing.T, c *ptytest.Console) {
		t.Helper()
		nativeWaitForLoginPasswordPrompt(t, c)
		c.SendLine(t, "authd2404")
		nativeWaitForResult(t, c)
	})
}

// nativeSelectBroker waits for provider selection and selects ExampleBroker.
func nativeSelectBroker(t *testing.T, c *ptytest.Console) {
	t.Helper()
	c.WaitFor(t, `(?s)== Provider selection ==.*2\. ExampleBroker.*Choose your provider:`)
	sendEchoedLine(t, c, "2")
}

// nativeSimpleAuth performs basic native authentication: select broker, enter password.
func nativeSimpleAuth(t *testing.T, c *ptytest.Console) {
	t.Helper()
	nativeSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password:`)
	c.SendLine(t, "goodpass")
	nativeWaitForResult(t, c)
}

func nativeQRCodeAuth(t *testing.T, c *ptytest.Console, method, username string, signalFn func(string)) {
	t.Helper()
	nativeSelectBroker(t, c)
	c.WaitFor(t, `Gimme your password:`)
	c.SendLine(t, "r")
	c.WaitFor(t, `Choose your authentication method:`)
	sendEchoedLine(t, c, method)
	c.WaitFor(t, `Choose action:`)
	for i := 0; i < 4; i++ {
		sendEchoedLine(t, c, "2")
		c.WaitFor(t, `Choose action:`)
	}
	signalFn(username)
	sendEchoedLine(t, c, "1")
	nativeWaitForResult(t, c)
}

func nativeChangePassword(t *testing.T, c *ptytest.Console, newPassword string, confirm string) {
	t.Helper()
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, newPassword)
	c.WaitFor(t, `Confirm Password:`)
	c.SendLine(t, confirm)
}

func nativeWaitForLoginPasswordPrompt(t *testing.T, c *ptytest.Console) {
	t.Helper()

	matched := c.WaitFor(t, `Choose your provider:|Gimme your password:`)
	if strings.Contains(matched, `Choose your provider:`) {
		sendEchoedLine(t, c, "2")
		c.WaitFor(t, `Gimme your password:`)
	}
}

// nativeWaitForResult waits for the PAM runner result line.
func nativeWaitForResult(t *testing.T, c *ptytest.Console) {
	t.Helper()
	waitForRunnerResult(t, c, pam_test.RunnerResultActionAcctMgmt)
}

// nativeWaitForChangeAuthTokResult waits for the PAM runner ChangeAuthTok result.
// AcctMgmt is the last action to complete, so waiting for it ensures the whole
// authentication token change has finished.
func nativeWaitForChangeAuthTokResult(t *testing.T, c *ptytest.Console) {
	t.Helper()
	waitForRunnerResult(t, c, pam_test.RunnerResultActionAcctMgmt)
}
