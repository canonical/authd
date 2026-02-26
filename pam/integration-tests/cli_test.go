package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

const cliTapeBaseCommand = "./pam_authd %s socket=${%s}"

func TestCLIAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)
	tapeCommand := fmt.Sprintf(cliTapeBaseCommand, pam_test.RunnerActionLogin,
		vhsTapeSocketVariable)

	tests := map[string]struct {
		tape          string
		tapeSettings  []tapeSetting
		tapeVariables map[string]string

		clientOptions      clientOptions
		socketPath         string
		currentUserNotRoot bool
		wantLocalGroups    bool
		oldDB              string
		stopDaemonAfter    time.Duration
	}{
		"Authenticate_user_successfully": {
			tape: "simple_auth",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: vhsTestUserName(t, "simple"),
			},
		},
		"Authenticate_user_successfully_with_upper_case": {
			tape: "simple_auth",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: vhsTestUserName(t, "upper-case"),
			},
		},
		"Authenticate_user_successfully_with_preset_user": {
			tape: "simple_auth_with_preset_user",
			clientOptions: clientOptions{
				PamUser: vhsTestUserName(t, "preset"),
			},
		},
		"Authenticate_user_successfully_with_upper_case_preset_user": {
			tape: "simple_auth_with_preset_user",
			clientOptions: clientOptions{
				PamUser: strings.ToUpper(vhsTestUserName(t, "preset-upper-case")),
			},
		},
		"Authenticate_user_successfully_with_invalid_connection_timeout": {
			tape: "simple_auth",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: vhsTestUserName(t, "invalid-timeout"),
			},
			clientOptions: clientOptions{PamTimeout: "invalid"},
		},
		"Authenticate_user_successfully_with_password_only_supported_method": {
			tape: "simple_auth",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationAuthModesPrefix + "password-integration-cli",
			},
		},
		"Authenticate_user_successfully_after_trying_empty_user": {
			tape: "simple_auth_empty_user",
		},
		"Authenticate_user_successfully_after_db_migration": {
			tape:  "simple_auth_with_auto_selected_broker",
			oldDB: "authd_0.4.1_bbolt_with_mixed_case_users",
			clientOptions: clientOptions{
				PamUser: "user-integration-cached",
			},
		},
		"Authenticate_user_with_upper_case_using_lower_case_after_db_migration": {
			tape:  "simple_auth_with_auto_selected_broker",
			oldDB: "authd_0.4.1_bbolt_with_mixed_case_users",
			clientOptions: clientOptions{
				PamUser: "user-integration-upper-case",
			},
		},
		"Authenticate_user_with_mixed_case_after_db_migration": {
			tape:  "simple_auth_with_auto_selected_broker",
			oldDB: "authd_0.4.1_bbolt_with_mixed_case_users",
			clientOptions: clientOptions{
				PamUser: "user-integration-WITH-Mixed-CaSe",
			},
		},
		"Authenticate_user_with_mfa": {
			tape: "mfa_auth",
		},
		"Authenticate_user_with_form_mode_with_button": {
			tape: "form_with_button",
		},
		"Authenticate_user_with_qr_code": {
			tape: "qr_code",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code",
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-tty",
				Term:    "linux",
			},
		},
		"Authenticate_user_with_qr_code_in_a_TTY_session": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-tty-session",
				Term:    "xterm-256color", SessionType: "tty",
			},
		},
		"Authenticate_user_with_qr_code_in_screen": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "qr-code-screen",
				Term:    "screen",
			},
		},
		"Authenticate_user_with_qr_code_after_many_regenerations": {
			tape: "qr_code_quick_regenerate",
			tapeSettings: []tapeSetting{
				{vhsHeight, 800},
				{vhsWaitTimeout, 15 * time.Second},
			},
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			tape: "mandatory_password_reset",
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			tape: "mandatory_password_reset_case_insensitive",
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
			tape: "mfa_reset_pwquality_auth",
		},
		"Authenticate_user_and_offer_password_reset": {
			tape: "optional_password_reset_skip",
		},
		"Authenticate_user_switching_auth_mode": {
			tape: "switch_auth_mode",
		},
		"Authenticate_user_switching_username": {
			tape: "switch_username",
		},
		"Authenticate_user_switching_to_local_broker": {
			tape:         "switch_local_broker",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},
		"Authenticate_user_and_add_it_to_local_group": {
			tape:            "local_group",
			wantLocalGroups: true,
		},
		"Authenticate_with_warnings_on_unsupported_arguments": {
			tape: "simple_auth_with_unsupported_args",
		},

		"Remember_last_successful_broker_and_mode": {
			tape: "remember_broker_and_mode",
		},
		"Autoselect_local_broker_for_local_user": {
			tape: "local_user",
		},
		"Autoselect_local_broker_for_local_user_preset": {
			tape:          "local_user_preset",
			clientOptions: clientOptions{PamUser: "root"},
		},

		"Prevent_user_from_switching_username": {
			tape: "switch_preset_username",
			clientOptions: clientOptions{
				PamUser: examplebroker.UserIntegrationPrefix + "pam-preset",
			},
		},

		"Deny_authentication_if_current_user_is_not_considered_as_root": {
			tape: "not_root", currentUserNotRoot: true,
		},

		"Deny_authentication_if_max_attempts_reached": {
			tape: "max_attempts",
		},
		"Deny_authentication_if_user_does_not_exist": {
			tape:         "unexistent_user",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			tape: "bad_password",
		},

		"Exit_authd_if_local_broker_is_selected": {
			tape:         "local_broker",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},
		"Exit_authd_if_user_sigints": {
			tape: "sigint",
		},
		"Exit_if_authd_is_stopped": {
			tape:            "authd_stopped",
			stopDaemonAfter: sleepDuration(defaultSleepValues[authdSleepLong] * 5),
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
			if tc.wantLocalGroups || tc.currentUserNotRoot || tc.stopDaemonAfter > 0 || tc.oldDB != "" {
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

			td := newTapeData(tc.tape, outDir, tc.tapeSettings...)
			td.Command = tapeCommand
			td.Variables = tc.tapeVariables
			td.Env[vhsTapeSocketVariable] = socketPath
			td.Env["AUTHD_TEST_PID_FILE"] = pidFile
			td.AddClientOptions(t, tc.clientOptions)
			td.RunVHS(t, vhsTestTypeCLI, cliEnv)
			got := td.SanitizedOutput(t)
			golden.CheckOrUpdate(t, got)

			localgroupstestutils.RequireGroupFile(t, groupFileOutput, golden.Path(t))

			requireRunnerResultForUser(t, authd.SessionMode_LOGIN, tc.clientOptions.PamUser, got)
		})
	}
}

func TestCLIChangeAuthTok(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	clientPath := t.TempDir()
	cliEnv := preparePamRunnerTest(t, clientPath)

	tapeCommand := fmt.Sprintf(cliTapeBaseCommand, pam_test.RunnerActionPasswd,
		vhsTapeSocketVariable)

	tests := map[string]struct {
		tape          string
		tapeSettings  []tapeSetting
		tapeVariables map[string]string

		currentUserNotRoot bool
	}{
		"Change_password_successfully_and_authenticate_with_new_one": {
			tape: "passwd_simple",
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_LOGIN_COMMAND": fmt.Sprintf(
					cliTapeBaseCommand, pam_test.RunnerActionLogin, vhsTapeSocketVariable),
				vhsTapeUserVariable:              vhsTestUserName(t, "simple"),
				"AUTHD_TEST_TAPE_LOGIN_USERNAME": vhsTestUserName(t, "simple"),
			},
		},
		"Change_password_successfully_and_authenticate_with_new_one_with_different_case": {
			tape: "passwd_simple",
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_LOGIN_COMMAND": fmt.Sprintf(
					cliTapeBaseCommand, pam_test.RunnerActionLogin, vhsTapeSocketVariable),
				vhsTapeUserVariable:              strings.ToUpper(vhsTestUserName(t, "case-insensitive")),
				"AUTHD_TEST_TAPE_LOGIN_USERNAME": vhsTestUserName(t, "case-insensitive"),
			},
		},
		"Change_passwd_after_MFA_auth": {
			tape: "passwd_mfa",
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationMfaPrefix + "cli-passwd",
			},
		},

		"Retry_if_new_password_is_rejected_by_broker": {
			tape: "passwd_rejected",
		},
		"Retry_if_new_password_is_same_of_previous": {
			tape: "passwd_not_changed",
		},
		"Retry_if_password_confirmation_is_not_the_same": {
			tape: "passwd_not_confirmed",
		},
		"Retry_if_new_password_does_not_match_quality_criteria": {
			tape: "passwd_bad_password",
		},

		"Prevent_change_password_if_auth_fails": {
			tape: "passwd_auth_fail",
		},
		"Prevent_change_password_if_user_does_not_exist": {
			tape:         "passwd_unexistent_user",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
			tapeVariables: map[string]string{
				vhsTapeUserVariable: examplebroker.UserIntegrationUnexistent,
			},
		},
		"Prevent_change_password_if_current_user_is_not_root_as_can_not_authenticate": {
			tape:               "passwd_not_root",
			currentUserNotRoot: true,
		},

		"Exit_authd_if_local_broker_is_selected": {
			tape:         "passwd_local_broker",
			tapeSettings: []tapeSetting{{vhsHeight, 800}},
		},
		"Exit_authd_if_user_sigints": {
			tape: "passwd_sigint",
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

			if _, ok := tc.tapeVariables[vhsTapeUserVariable]; !ok && !tc.currentUserNotRoot {
				if tc.tapeVariables == nil {
					tc.tapeVariables = make(map[string]string)
				}
				tc.tapeVariables[vhsTapeUserVariable] = vhsTestUserName(t, "cli-passwd")
			}

			td := newTapeData(tc.tape, outDir, tc.tapeSettings...)
			td.Command = tapeCommand
			td.Variables = tc.tapeVariables
			td.Env[vhsTapeSocketVariable] = socketPath
			td.AddClientOptions(t, clientOptions{})
			td.RunVHS(t, vhsTestTypeCLI, cliEnv)
			got := td.SanitizedOutput(t)
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
