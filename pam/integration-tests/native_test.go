package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/stretchr/testify/require"
)

const nativeTapeBaseCommand = "./pam_authd %s socket=${%s} force_native_client=true"

func TestNativeAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)
	tapeCommand := fmt.Sprintf(nativeTapeBaseCommand, pam_test.RunnerActionLogin,
		vhsTapeSocketVariable)

	tests := map[string]struct {
		tape          string
		tapeSettings  []tapeSetting
		tapeVariables map[string]string
		tapeCommand   string

		clientOptions      clientOptions
		currentUserNotRoot bool
		userSelection      bool
		userSuffixSkip     bool
		oldDB              string
		wantLocalGroups    bool
		wantSeparateDaemon bool
		skipRunnerCheck    bool
		socketPath         string
	}{
		"Authenticate_user_successfully": {
			tape: "simple_auth",
		},
		"Authenticate_user_successfully_with_upper_case": {
			tape: "simple_auth",
			clientOptions: clientOptions{
				PamUser: strings.ToUpper(vhsTestUserName(t, "upper-case")),
			},
		},
		"Authenticate_user_successfully_with_user_selection": {
			tape:          "simple_auth_with_user_selection",
			userSelection: true,
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationPrefix + "native-user-selection",
			},
		},
		"Authenticate_user_successfully_using_upper_case_with_user_selection": {
			tape:          "simple_auth_with_user_selection",
			userSelection: true,
			tapeVariables: map[string]string{
				vhsTapeUserVariable: strings.ToUpper(vhsTestUserName(t, "selection-upper-case")),
			},
		},
		"Authenticate_user_successfully_with_invalid_connection_timeout": {
			tape: "simple_auth",
			clientOptions: clientOptions{
				PamUser:    "user-integration-simple-auth-invalid-timeout",
				PamTimeout: "invalid",
			},
		},
		"Authenticate_user_successfully_with_password_only_supported_method": {
			tape: "simple_auth",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationAuthModesPrefix + "password-integration-native",
			},
		},
		"Authenticate_user_successfully_with_password_only_supported_method_in_polkit": {
			tape: "simple_auth_one_broker_only",
			clientOptions: clientOptions{
				PamServiceName: "polkit-1",
				PamUser: vhsTestUserNameFull(t,
					examplebroker.UserIntegrationAuthModesPrefix, "password-integration-polkit"),
			},
		},
		"Authenticate_user_successfully_after_db_migration": {
			tape:           "simple_auth_with_auto_selected_broker",
			oldDB:          "authd_0.4.1_bbolt_with_mixed_case_users",
			userSuffixSkip: true,
			clientOptions: clientOptions{
				PamUser: "user-integration-cached",
			},
		},
		"Authenticate_user_with_upper_case_using_lower_case_after_db_migration": {
			tape:           "simple_auth_with_auto_selected_broker",
			oldDB:          "authd_0.4.1_bbolt_with_mixed_case_users",
			userSuffixSkip: true,
			clientOptions: clientOptions{
				PamUser: "user-integration-upper-case",
			},
		},
		"Authenticate_user_with_mixed_case_after_db_migration": {
			tape:           "simple_auth_with_auto_selected_broker",
			oldDB:          "authd_0.4.1_bbolt_with_mixed_case_users",
			userSuffixSkip: true,
			clientOptions: clientOptions{
				PamUser: "user-integration-WITH-Mixed-CaSe",
			},
		},
		"Authenticate_user_with_mfa": {
			tape:         "mfa_auth",
			tapeSettings: []tapeSetting{{vhsHeight, 1200}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationMfaPrefix + "auth",
			},
		},
		"Authenticate_user_with_form_mode_with_button": {
			tape:         "form_with_button",
			tapeSettings: []tapeSetting{{vhsHeight, 700}},
			tapeVariables: map[string]string{
				"AUTHD_FORM_BUTTON_TAPE_ITEM": "8",
			},
		},
		"Authenticate_user_with_form_mode_with_button_two_supported_methods": {
			tape: "form_with_button",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationAuthModesPrefix + "totp_with_button,password-integration-native",
			},
			tapeSettings: []tapeSetting{{vhsHeight, 700}},
			tapeVariables: map[string]string{
				"AUTHD_FORM_BUTTON_TAPE_ITEM": "2",
			},
		},
		"Authenticate_user_with_form_mode_with_button_in_polkit": {
			tape:          "form_with_button_polkit",
			tapeSettings:  []tapeSetting{{vhsHeight, 700}},
			clientOptions: clientOptions{PamServiceName: "polkit-1"},
			tapeVariables: map[string]string{
				"AUTHD_FORM_BUTTON_TAPE_ITEM": "7",
			},
		},
		"Authenticate_user_with_qr_code": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 3000}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "7",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "QR code",
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 4000}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "7",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "QR code",
			},
			clientOptions: clientOptions{
				Term: "linux",
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY_session": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 4000}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "7",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "QR code",
			},
			clientOptions: clientOptions{
				Term: "xterm-256color", SessionType: "tty",
			},
		},
		"Authenticate_user_with_qr_code_in_screen": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 4000}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "7",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "QR code",
			},
			clientOptions: clientOptions{
				Term: "screen",
			},
		},
		"Authenticate_user_with_qr_code_in_ssh": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 3500}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "2",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "Login code",
			},
			clientOptions: clientOptions{
				PamUser:        examplebroker.UserIntegrationPreCheckPrefix + "ssh-service-qr-code",
				PamServiceName: "sshd",
			},
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			tape:         "mandatory_password_reset",
			tapeSettings: []tapeSetting{{vhsHeight, 550}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationNeedsResetPrefix + "mandatory",
			},
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			tape:          "mandatory_password_reset_case_insensitive",
			tapeSettings:  []tapeSetting{{vhsHeight, 600}},
			userSelection: true,
			tapeVariables: map[string]string{
				vhsTapeUserVariable: vhsTestUserNameFull(t,
					examplebroker.UserIntegrationNeedsResetPrefix, "case-insensitive"),
				"AUTHD_TEST_TAPE_UPPER_CASE_USERNAME": strings.ToUpper(
					vhsTestUserNameFull(t,
						examplebroker.UserIntegrationNeedsResetPrefix, "Case-INSENSITIVE")),
				"AUTHD_TEST_TAPE_MIXED_CASE_USERNAME": vhsTestUserNameFull(t,
					examplebroker.UserIntegrationNeedsResetPrefix, "Case-INSENSITIVE"),
			},
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			tape:         "mfa_reset_pwquality_auth",
			tapeSettings: []tapeSetting{{vhsHeight, 3000}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationMfaWithResetPrefix + "pwquality",
			},
		},
		"Authenticate_user_with_mfa_and_reset_same_password": {
			tape:         "mfa_reset_same_password",
			tapeSettings: []tapeSetting{{vhsHeight, 3000}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationMfaWithResetPrefix + "same-password",
			},
		},
		"Authenticate_user_and_offer_password_reset": {
			tape: "optional_password_reset_skip",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationCanResetPrefix + "skip",
			},
		},
		"Authenticate_user_and_accept_password_reset": {
			tape: "optional_password_reset_accept",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationCanResetPrefix + "accept",
			},
		},
		"Authenticate_user_switching_auth_mode": {
			tape:          "switch_auth_mode",
			tapeSettings:  []tapeSetting{{vhsHeight, 3000}},
			clientOptions: clientOptions{PamUser: "user-integration-switch-mode"},
			tapeVariables: map[string]string{
				"AUTHD_SWITCH_AUTH_MODE_TAPE_SEND_URL_TO_EMAIL_ITEM":   "2",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_FIDO_DEVICE_FOO_ITEM":     "3",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PHONE_33_ITEM":            "4",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PHONE_1_ITEM":             "5",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PIN_CODE_ITEM":            "6",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_QR_OR_LOGIN_CODE_ITEM":    "7",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_AUTHENTICATION_CODE_ITEM": "8",

				"AUTHD_SWITCH_AUTH_MODE_TAPE_QR_OR_LOGIN_CODE_ITEM_NAME": "QR code",
			},
		},
		"Authenticate_user_switching_username": {
			tape:          "switch_username",
			userSelection: true,
			tapeVariables: map[string]string{
				vhsTapeUserVariable:               examplebroker.UserIntegrationPrefix + "native-username",
				vhsTapeUserVariable + "_SWITCHED": examplebroker.UserIntegrationPrefix + "native-username-switched",
			},
		},
		"Authenticate_user_switching_to_local_broker": {
			tape:         "switch_local_broker",
			tapeSettings: []tapeSetting{{vhsHeight, 700}},
		},
		"Authenticate_user_and_add_it_to_local_group": {
			tape:            "local_group",
			tapeSettings:    []tapeSetting{{vhsHeight, 700}},
			wantLocalGroups: true,
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationLocalGroupsPrefix + "auth",
			},
		},
		"Authenticate_user_on_ssh_service": {
			tape: "simple_ssh_auth",
			clientOptions: clientOptions{
				PamUser:        examplebroker.UserIntegrationPreCheckPrefix + "ssh-service",
				PamServiceName: "sshd",
			},
		},
		"Authenticate_user_on_ssh_service_with_custom_name_and_connection_env": {
			tape: "simple_ssh_auth",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPreCheckPrefix + "ssh-connection",
				PamEnv:  []string{"SSH_CONNECTION=foo-connection"},
			},
		},
		"Authenticate_user_on_ssh_service_with_custom_name_and_auth_info_env": {
			tape: "simple_ssh_auth",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPreCheckPrefix + "ssh-auth-info",
				PamEnv:  []string{"SSH_AUTH_INFO_0=foo-authinfo"},
			},
		},
		"Authenticate_with_warnings_on_unsupported_arguments": {
			tape: "simple_auth_with_unsupported_args",
			tapeCommand: strings.ReplaceAll(tapeCommand, "force_native_client=true",
				"invalid_flag=foo force_native_client=true bar"),
		},

		"Remember_last_successful_broker_and_mode": {
			tape:         "remember_broker_and_mode",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},
		"Autoselect_local_broker_for_local_user": {
			tape:          "local_user",
			userSelection: true,
		},
		"Autoselect_local_broker_for_local_user_on_polkit": {
			tape:          "local_user",
			userSelection: true,
			clientOptions: clientOptions{PamServiceName: "polkit-1"},
		},
		"Autoselect_local_broker_for_local_user_preset": {
			tape: "local_user_preset",
			clientOptions: clientOptions{
				PamUser: "root",
			},
		},
		"Autoselect_local_broker_for_local_user_preset_on_polkit": {
			tape: "local_user_preset",
			clientOptions: clientOptions{
				PamServiceName: "polkit-1",
				PamUser:        "root",
			},
		},

		"Deny_authentication_if_current_user_is_not_considered_as_root": {
			tape: "not_root", currentUserNotRoot: true,
		},

		"Deny_authentication_if_max_attempts_reached": {
			tape:         "max_attempts",
			tapeSettings: []tapeSetting{{vhsHeight, 700}},
		},
		"Deny_authentication_if_user_does_not_exist": {
			tape: "unexistent_user",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationUnexistent,
			},
		},
		"Deny_authentication_if_user_does_not_exist_and_matches_cancel_key": {
			tape:          "cancel_key_user",
			userSelection: true,
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			tape:         "bad_password",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationNeedsResetPrefix + "bad-password",
			},
		},

		"Prevent_preset_user_from_switching_username": {
			tape:         "switch_preset_username",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},

		"Exit_authd_if_local_broker_is_selected": {
			tape: "local_broker",
		},
		"Exit_if_user_is_not_pre-checked_on_ssh_service": {
			tape: "local_ssh",
			clientOptions: clientOptions{
				PamServiceName: "sshd",
			},
		},
		"Exit_if_user_is_not_pre-checked_on_custom_ssh_service_with_connection_env": {
			tape: "local_ssh",
			clientOptions: clientOptions{
				PamEnv: []string{"SSH_CONNECTION=foo-connection"},
			},
		},
		"Exit_if_user_is_not_pre-checked_on_custom_ssh_service_with_auth_info_env": {
			tape: "local_ssh",
			clientOptions: clientOptions{
				PamEnv: []string{"SSH_AUTH_INFO_0=foo-authinfo"},
			},
		},
		// FIXME: While this works now, it requires proper handling via signal_fd
		"Exit_authd_if_user_sigints": {
			tape:            "sigint",
			skipRunnerCheck: true,
		},
		"Exit_if_authd_is_stopped": {
			tape:               "authd_stopped",
			wantSeparateDaemon: true,
		},

		"Error_if_cannot_connect_to_authd": {
			tape:       "connection_error",
			socketPath: "/some-path/not-existent-socket",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			err := os.Symlink(filepath.Join(clientPath, "pam_authd"),
				filepath.Join(outDir, "pam_authd"))
			require.NoError(t, err, "Setup: symlinking the pam client")

			var socketPath, groupFileOutput, pidFile string
			if tc.wantLocalGroups || tc.currentUserNotRoot || tc.wantSeparateDaemon ||
				tc.oldDB != "" {
				// For the local groups tests we need to run authd again so that it has
				// special environment that saves the updated group file to a writable
				// location for us to test.
				// Similarly for the not-root tests authd has to run in a more restricted way.
				// In the other cases this is not needed, so we can just use a shared authd.
				var groupFile string
				groupFileOutput, groupFile = prepareGroupFiles(t)

				if tc.wantLocalGroups || tc.oldDB != "" {
					// We don't want to use separate input ant output files here.
					groupFileOutput = groupFile
				}

				pidFile = filepath.Join(outDir, "authd.pid")

				args := []testutils.DaemonOption{
					testutils.WithGroupFile(groupFile),
					testutils.WithGroupFileOutput(groupFileOutput),
					testutils.WithPidFile(pidFile),
					testutils.WithEnvironment(useOldDatabaseEnv(t, tc.oldDB)...),
				}
				if !tc.currentUserNotRoot {
					args = append(args, testutils.WithCurrentUserAsRoot)
				}

				socketPath = runAuthd(t, args...)
			} else {
				socketPath, groupFileOutput = sharedAuthd(t)
			}
			if tc.socketPath != "" {
				socketPath = tc.socketPath
			}

			if tc.tapeCommand == "" {
				tc.tapeCommand = tapeCommand
			}

			if u := tc.clientOptions.PamUser; !tc.userSuffixSkip &&
				strings.Contains(u, "integration") && !strings.Contains(u, "native") {
				tc.clientOptions.PamUser += "-native"
			}
			if tc.clientOptions.PamUser == "" && !tc.userSelection {
				tc.clientOptions.PamUser = vhsTestUserName(t, "native")
			}

			td := newTapeData(tc.tape, outDir, tc.tapeSettings...)
			td.Command = tc.tapeCommand
			td.Env[vhsTapeSocketVariable] = socketPath
			td.Env[pam_test.RunnerEnvSupportsConversation] = "1"
			td.Env["AUTHD_TEST_PID_FILE"] = pidFile
			td.Variables = tc.tapeVariables
			td.AddClientOptions(t, tc.clientOptions)
			td.RunVHS(t, vhsTestTypeNative, cliEnv)
			got := td.SanitizedOutput(t)
			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, groupFileOutput, golden.Path(t))

			if !tc.skipRunnerCheck {
				requireRunnerResultForUser(t, authd.SessionMode_LOGIN, tc.clientOptions.PamUser, got)
			}
		})
	}
}

func TestNativeChangeAuthTok(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tapeCommand := fmt.Sprintf(nativeTapeBaseCommand, pam_test.RunnerActionPasswd,
		vhsTapeSocketVariable)
	tapeLoginCommand := fmt.Sprintf(nativeTapeBaseCommand, pam_test.RunnerActionLogin,
		vhsTapeSocketVariable)

	tests := map[string]struct {
		tape          string
		tapeSettings  []tapeSetting
		tapeVariables map[string]string
		clientOptions clientOptions

		currentUserNotRoot bool
		skipRunnerCheck    bool
	}{
		"Change_password_successfully_and_authenticate_with_new_one": {
			tape: "passwd_simple",
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_LOGIN_COMMAND":  tapeLoginCommand,
				vhsTapeUserVariable:              vhsTestUserName(t, "simple"),
				"AUTHD_TEST_TAPE_LOGIN_USERNAME": vhsTestUserName(t, "simple"),
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_single_broker_and_password_only_supported_method": {
			tape: "passwd_simple_one_broker_only",
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_LOGIN_COMMAND": tapeLoginCommand,
			},
			clientOptions: clientOptions{
				PamServiceName: "polkit-1",
				PamUser: vhsTestUserNameFull(t,
					examplebroker.UserIntegrationAuthModesPrefix, "password,mandatoryreset-integration-polkit"),
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_different_case": {
			tape: "passwd_simple",
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_LOGIN_COMMAND":  tapeLoginCommand,
				vhsTapeUserVariable:              vhsTestUserName(t, "case-insensitive"),
				"AUTHD_TEST_TAPE_LOGIN_USERNAME": vhsTestUserName(t, "case-insensitive"),
			},
		},
		"Change_passwd_after_MFA_auth": {
			tape:         "passwd_mfa",
			tapeSettings: []tapeSetting{{vhsHeight, 1300}},
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationMfaPrefix + "native-passwd",
			},
		},

		"Retry_if_new_password_is_rejected_by_broker": {
			tape:         "passwd_rejected",
			tapeSettings: []tapeSetting{{vhsHeight, 1000}},
		},
		"Retry_if_new_password_is_same_of_previous": {
			tape: "passwd_not_changed",
		},
		"Retry_if_password_confirmation_is_not_the_same": {
			tape: "passwd_not_confirmed",
		},
		"Retry_if_new_password_does_not_match_quality_criteria": {
			tape:         "passwd_bad_password",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},

		"Prevent_change_password_if_auth_fails": {
			tape:         "passwd_auth_fail",
			tapeSettings: []tapeSetting{{vhsHeight, 700}},
		},
		"Prevent_change_password_if_user_does_not_exist": {
			tape: "passwd_unexistent_user",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationUnexistent,
			},
		},
		"Prevent_change_password_if_current_user_is_not_root_as_can_not_authenticate": {
			tape: "passwd_not_root", currentUserNotRoot: true,
		},

		"Exit_authd_if_local_broker_is_selected": {
			tape: "passwd_local_broker",
		},
		// FIXME: While this works now, it requires proper handling via signal_fd
		"Exit_authd_if_user_sigints": {
			tape:            "passwd_sigint",
			skipRunnerCheck: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			err := os.Symlink(filepath.Join(clientPath, "pam_authd"),
				filepath.Join(outDir, "pam_authd"))
			require.NoError(t, err, "Setup: symlinking the pam client")

			var socketPath string
			if tc.currentUserNotRoot {
				// For the not-root tests authd has to run in a more restricted way.
				// In the other cases this is not needed, so we can just use a shared authd.
				socketPath = runAuthd(t, testutils.WithGroupFile(filepath.Join(t.TempDir(), "group")))
			} else {
				socketPath, _ = sharedAuthd(t)
			}

			if _, ok := tc.tapeVariables[vhsTapeUserVariable]; !ok &&
				!tc.currentUserNotRoot && tc.clientOptions.PamUser == "" {
				if tc.tapeVariables == nil {
					tc.tapeVariables = make(map[string]string)
				}
				tc.tapeVariables[vhsTapeUserVariable] = vhsTestUserName(t, "native-passwd")
			}

			td := newTapeData(tc.tape, outDir, tc.tapeSettings...)
			td.Command = tapeCommand
			td.Variables = tc.tapeVariables
			td.Env[vhsTapeSocketVariable] = socketPath
			td.Env[pam_test.RunnerEnvSupportsConversation] = "1"
			td.AddClientOptions(t, tc.clientOptions)
			td.RunVHS(t, vhsTestTypeNative, cliEnv)
			got := td.SanitizedOutput(t)
			golden.CheckOrUpdate(t, got)

			if !tc.skipRunnerCheck {
				requireRunnerResultForUser(t, authd.SessionMode_CHANGE_PASSWORD,
					tc.clientOptions.PamUser, got)
			}
		})
	}
}
