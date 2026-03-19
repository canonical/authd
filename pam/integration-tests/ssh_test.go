package main_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd/examplebroker"
	"github.com/ubuntu/authd/internal/grpcutils"
	"github.com/ubuntu/authd/internal/proto/authd"
	"github.com/ubuntu/authd/internal/services/errmessages"
	"github.com/ubuntu/authd/internal/testutils"
	"github.com/ubuntu/authd/internal/testutils/golden"
	localgroupstestutils "github.com/ubuntu/authd/internal/users/localentries/testutils"
	"github.com/ubuntu/authd/pam/internal/pam_test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	sshEnvVariablesRegex *regexp.Regexp
	sshHostPortRegex     *regexp.Regexp

	sshDefaultFinalWaitTimeout = sleepDuration(3 * defaultSleepValues[authdWaitDefault])
)

func TestSSHAuthenticate(t *testing.T) {
	t.Parallel()

	runSharedDaemonTests := testutils.IsRace() || os.Getenv("AUTHD_TESTS_SSHD_SHARED") != ""

	// We only test the single-sshd instance when in race mode.
	testSSHAuthenticate(t, runSharedDaemonTests)

	// When updating the golden files we need to perform all kind of tests.
	if golden.UpdateEnabled() {
		testSSHAuthenticate(t, !runSharedDaemonTests)
	}
}

//nolint:thelper // This is actually a test function!
func testSSHAuthenticate(t *testing.T, sharedSSHd bool) {
	// Due to external dependencies such as `vhs`, we can't run the tests in some environments (like LP builders), as we
	// can't install the dependencies there. So we need to be able to skip these tests on-demand.
	if os.Getenv("AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS") != "" {
		t.Skip("Skipping tests with external dependencies as requested")
	}

	// These tests are flaky, see https://github.com/canonical/authd/issues/1328
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	if uv := getUbuntuVersion(t); uv == 0 || uv < 2404 {
		require.Empty(t, os.Getenv("GITHUB_REPOSITORY"),
			"Golden files need to be updated to run tests on Ubuntu %v", uv)
		t.Skipf("Skipping SSH tests since they require new golden files for Ubuntu %v", uv)
	}

	currentDir, err := os.Getwd()
	require.NoError(t, err, "Setup: Could not get current directory for the tests")

	execModule := buildExecModuleWithCFlags(t, []string{"-std=c11"}, true)
	execChild := buildPAMExecChild(t)

	mkHomeDirHelper, err := exec.LookPath("mkhomedir_helper")
	require.NoError(t, err, "Setup: mkhomedir_helper not found")
	pamMkHomeDirModule := buildCPAMModule(t,
		[]string{"./pam/integration-tests/pam_mkhomedir/pam_mkhomedir.c"},
		nil,
		[]string{
			"-DAUTHD_TESTS_SSH_USE_AUTHD_NSS",
			fmt.Sprintf("-DMKHOMEDIR_HELPER=%q", mkHomeDirHelper),
		},
		"pam_mkhomedir_test.so", true)

	var nssEnv []string
	var nssLibrary string
	var sshdPreloadLibraries []string
	var sshdPreloaderCFlags []string
	err = testutils.CanRunRustTests(false)
	if os.Getenv("AUTHD_TESTS_SSH_USE_DUMMY_NSS") == "" && err == nil {
		nssLibrary, nssEnv = testutils.BuildRustNSSLib(t, true)
		sshdPreloadLibraries = append(sshdPreloadLibraries, nssLibrary)
		sshdPreloaderCFlags = append(sshdPreloaderCFlags,
			"-DAUTHD_TESTS_SSH_USE_AUTHD_NSS")
		nssEnv = append(nssEnv, nssTestEnvBase(t, nssLibrary)...)
	} else if err != nil {
		t.Logf("Using the dummy library to implement NSS: %v", err)
	}

	sshdPreloadLibrary := buildCModule(t, []string{
		filepath.Join(currentDir, "/sshd_preloader/sshd_preloader.c"),
	}, nil, sshdPreloaderCFlags, nil, "sshd_preloader", true)
	sshdPreloadLibraries = append(sshdPreloadLibraries, sshdPreloadLibrary)

	sshdHostKey := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	//#nosec:G204 - we control the command arguments in tests
	out, err := exec.Command("ssh-keygen", "-q", "-f", sshdHostKey, "-N", "", "-t", "ed25519").CombinedOutput()
	require.NoError(t, err, "Setup: Failed generating SSH host key: %s", out)
	saveArtifactsForDebugOnCleanup(t, []string{sshdHostKey})

	pubKey, err := os.ReadFile(sshdHostKey + ".pub")
	require.NoError(t, err, "Setup: Can't read sshd host public key")
	saveArtifactsForDebugOnCleanup(t, []string{sshdHostKey + ".pub"})

	const pamSSHUserEnv = "AUTHD_PAM_SSH_USER"
	const baseTapeCommand = "ssh ${%s}@localhost ${AUTHD_PAM_SSH_ARGS}"
	tapeCommand := fmt.Sprintf(baseTapeCommand, pamSSHUserEnv)
	defaultTapeSettings := []tapeSetting{{vhsHeight, 1000}, {vhsWidth, 1500}}

	var sshdEnv []string
	var defaultSSHDPort, defaultUserHome, defaultSocketPath, defaultGPasswdOutput string
	if sharedSSHd {
		defaultSocketPath, defaultGPasswdOutput = sharedAuthd(t)
		serviceFile := createSshdServiceFile(t, execModule, execChild, pamMkHomeDirModule, defaultSocketPath)
		sshdEnv = append(sshdEnv, nssEnv...)
		sshdEnv = append(sshdEnv, fmt.Sprintf("AUTHD_NSS_SOCKET=%s", defaultSocketPath))
		defaultSSHDPort, defaultUserHome = startSSHdForTest(t, serviceFile, sshdHostKey,
			"authd-test-user-sshd-accept-all", sshdPreloadLibraries, sshdEnv, true, false)
	}

	sshEnvVariablesRegex = regexp.MustCompile(`(?m)  (PATH|HOME|PWD|SSH_[A-Z]+)=.*(\n*)($[^ ]{2}.*)?$`)
	sshHostPortRegex = regexp.MustCompile(`([\d\.:]+) port ([\d:]+)`)

	tests := map[string]struct {
		tape          string
		tapeSettings  []tapeSetting
		tapeVariables map[string]string

		user             string
		isLocalUser      bool
		userPrefix       string
		pamServiceName   string
		socketPath       string
		daemonizeSSHd    bool
		interactiveShell bool
		oldDB            string

		wantUserAlreadyExist bool
		wantNotLoggedInUser  bool
		wantLocalGroups      bool
	}{
		"Authenticate_user_successfully": {
			tape: "simple_auth",
		},
		"Authenticate_user_successfully_if_already_registered": {
			user: "user-ssh",
			tape: "simple_auth",
		},
		"Authenticate_user_successfully_and_enters_shell": {
			tape:             "simple_auth_with_shell",
			interactiveShell: true,
		},
		"Authenticate_user_successfully_with_upper_case": {
			tape: "simple_auth",
			user: strings.ToUpper(vhsTestUserNameFull(t,
				examplebroker.UserIntegrationPreCheckPrefix, "upper-case")),
		},
		"Authenticate_user_successfully_if_already_registered_with_upper_case": {
			user: "USER-SSH2",
			tape: "simple_auth",
		},
		"Authenticate_user_successfully_after_db_migration": {
			tape:                 "simple_auth_with_auto_selected_broker",
			oldDB:                "authd_0.4.1_bbolt_with_mixed_case_users",
			wantUserAlreadyExist: true,
			user:                 "user-integration-cached",
		},
		"Authenticate_user_with_upper_case_using_lower_case_after_db_migration": {
			tape:                 "simple_auth_with_auto_selected_broker",
			oldDB:                "authd_0.4.1_bbolt_with_mixed_case_users",
			wantUserAlreadyExist: true,
			user:                 "user-integration-upper-case",
		},
		"Authenticate_user_with_mixed_case_after_db_migration": {
			tape:                 "simple_auth_with_auto_selected_broker",
			oldDB:                "authd_0.4.1_bbolt_with_mixed_case_users",
			wantUserAlreadyExist: true,
			user:                 "user-integration-WITH-Mixed-CaSe",
		},
		"Authenticate_user_with_mfa": {
			tape:         "mfa_auth",
			tapeSettings: []tapeSetting{{vhsHeight, 1500}},
			userPrefix:   examplebroker.UserIntegrationMfaPrefix,
		},
		"Authenticate_user_with_form_mode_with_button": {
			tape:         "form_with_button",
			tapeSettings: []tapeSetting{{vhsHeight, 1500}},
			tapeVariables: map[string]string{
				"AUTHD_FORM_BUTTON_TAPE_ITEM": "8",
			},
		},
		"Authenticate_user_with_qr_code": {
			tape:         "qr_code",
			tapeSettings: []tapeSetting{{vhsHeight, 1500}},
			tapeVariables: map[string]string{
				"AUTHD_QRCODE_TAPE_ITEM":      "2",
				"AUTHD_QRCODE_TAPE_ITEM_NAME": "Login code",
			},
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			tape:       "mandatory_password_reset",
			userPrefix: examplebroker.UserIntegrationNeedsResetPrefix,
		},
		"Authenticate_user_and_reset_password_with_case_insensitive_user_selection": {
			tape: "mandatory_password_reset_case_insensitive",
			user: strings.ToUpper(vhsTestUserNameFull(t,
				examplebroker.UserIntegrationNeedsResetPrefix+
					examplebroker.UserIntegrationPreCheckValue, "case-insensitive")),
			daemonizeSSHd: true,
			tapeVariables: map[string]string{
				"AUTHD_TEST_TAPE_SSH_USER_VAR": pamSSHUserEnv,
				"AUTHD_TEST_TAPE_LOWER_CASE_USERNAME": vhsTestUserNameFull(t,
					examplebroker.UserIntegrationNeedsResetPrefix+
						examplebroker.UserIntegrationPreCheckValue, "case-insensitive"),
				"AUTHD_TEST_TAPE_MIXED_CASE_USERNAME": vhsTestUserNameFull(t,
					examplebroker.UserIntegrationNeedsResetPrefix+
						examplebroker.UserIntegrationPreCheckValue, "Case-INSENSITIVE"),
			},
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			tape:         "mfa_reset_pwquality_auth",
			tapeSettings: []tapeSetting{{vhsHeight, 1500}, {vhsWidth, 1800}},
			userPrefix:   examplebroker.UserIntegrationMfaWithResetPrefix,
		},
		"Authenticate_user_and_offer_password_reset": {
			tape:       "optional_password_reset_skip",
			userPrefix: examplebroker.UserIntegrationCanResetPrefix,
		},
		"Authenticate_user_and_accept_password_reset": {
			tape:       "optional_password_reset_accept",
			userPrefix: examplebroker.UserIntegrationCanResetPrefix,
		},
		"Authenticate_user_switching_auth_mode": {
			tape:         "switch_auth_mode",
			tapeSettings: []tapeSetting{{vhsHeight, 3500}},
			tapeVariables: map[string]string{
				"AUTHD_SWITCH_AUTH_MODE_TAPE_QR_OR_LOGIN_CODE_ITEM":    "2",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_SEND_URL_TO_EMAIL_ITEM":   "3",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_FIDO_DEVICE_FOO_ITEM":     "4",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PHONE_33_ITEM":            "5",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PHONE_1_ITEM":             "6",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_PIN_CODE_ITEM":            "7",
				"AUTHD_SWITCH_AUTH_MODE_TAPE_AUTHENTICATION_CODE_ITEM": "8",

				"AUTHD_SWITCH_AUTH_MODE_TAPE_QR_OR_LOGIN_CODE_ITEM_NAME": "Login code",
			},
		},
		"Authenticate_user_switching_to_local_broker": {
			tape:                "switch_local_broker",
			wantNotLoggedInUser: true,
			tapeSettings:        []tapeSetting{{vhsHeight, 900}},
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: `Wait /Password:/`,
			},
		},
		"Authenticate_user_and_add_it_to_local_group": {
			tape:            "local_group",
			userPrefix:      examplebroker.UserIntegrationLocalGroupsPrefix,
			wantLocalGroups: true,
		},

		"Remember_last_successful_broker_and_mode": {
			tape:          "remember_broker_and_mode",
			daemonizeSSHd: true,
		},
		"Autoselect_local_broker_for_local_user": {
			tape:                "local_user_preset",
			user:                "root",
			isLocalUser:         true,
			wantNotLoggedInUser: true,
			tapeSettings: []tapeSetting{
				{vhsHeight, 200},
			},
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: `Wait /Password:/`,
			},
		},

		"Deny_authentication_if_max_attempts_reached": {
			tape:                "max_attempts",
			wantNotLoggedInUser: true,
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: fmt.Sprintf(
					`Wait+Screen /Too many authentication failures/
Wait@%dms`, sshDefaultFinalWaitTimeout),
			},
		},
		"Deny_authentication_if_user_does_not_exist": {
			tape:                "unexistent_user",
			user:                examplebroker.UserIntegrationUnexistent,
			wantNotLoggedInUser: true,
		},
		"Deny_authentication_if_user_does_not_exist_and_matches_cancel_key": {
			tape:                "cancel_key_user",
			user:                "r",
			wantNotLoggedInUser: true,
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			tape:         "bad_password",
			userPrefix:   examplebroker.UserIntegrationNeedsResetPrefix,
			tapeSettings: []tapeSetting{{vhsHeight, 1200}},
		},

		"Prevent_user_from_switching_username": {
			tape: "switch_preset_username",
		},

		"Exit_authd_if_local_broker_is_selected": {
			tape:                "local_broker",
			wantNotLoggedInUser: true,
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: `Wait /Password:/`,
			},
		},
		"Exit_if_user_is_not_pre-checked_on_ssh_service": {
			tape:                "local_ssh",
			user:                examplebroker.UserIntegrationPrefix + "ssh-service-not-allowed",
			pamServiceName:      "sshd",
			wantNotLoggedInUser: true,
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: `Wait /Password:/`,
			},
		},
		"Exit_authd_if_user_sigints": {
			tape:                "sigint",
			wantNotLoggedInUser: true,
		},

		"Error_if_cannot_connect_to_authd": {
			tape: "connection_error",
			tapeVariables: map[string]string{
				vhsCommandFinalAuthWaitVariable: `Wait /Password:/`,
			},
			socketPath:          "/some-path/not-existent-socket",
			wantNotLoggedInUser: true,
		},
	}
	for name, tc := range tests {
		if sharedSSHd {
			name = fmt.Sprintf("%s on shared SSHd", name)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			socketPath := defaultSocketPath
			gpasswdOutput := defaultGPasswdOutput

			var authdEnv []string
			var authdSocketLink string
			if nssLibrary != "" {
				authdEnv = slices.Clone(nssEnv)

				// Chicken-egg problem here: we need to start authd with the
				// AUTHD_NSS_SOCKET env set, but its content is not yet known to
				// us, so let's pass to it a path that we'll eventually symlink to
				// the real socket path, once we've one.
				socketDir, err := os.MkdirTemp("", "authd-sockets")
				require.NoError(t, err, "Setup: failed to create socket dir")
				authdSocketLink = filepath.Join(socketDir, "authd.sock")
				t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

				authdEnv = append(authdEnv, nssTestEnv(t, nssLibrary, authdSocketLink)...)
			}

			var groupsFile string
			if tc.wantLocalGroups || tc.oldDB != "" {
				// For the local groups tests we need to run authd again so that it has
				// special environment that generates a fake gpasswd output for us to test.
				// In the other cases this is not needed, so we can just use a shared authd.
				gpasswdOutput, groupsFile = prepareGPasswdFiles(t)

				authdEnv = append(authdEnv, useOldDatabaseEnv(t, tc.oldDB)...)

				socketPath = runAuthd(t, gpasswdOutput, groupsFile, true,
					testutils.WithEnvironment(authdEnv...))
			} else if !sharedSSHd {
				socketPath, gpasswdOutput = sharedAuthd(t,
					testutils.WithEnvironment(authdEnv...))
			}
			if tc.socketPath != "" {
				socketPath = tc.socketPath
			}

			user := tc.user
			if tc.userPrefix != "" {
				tc.userPrefix = tc.userPrefix + examplebroker.UserIntegrationPreCheckValue
			}
			if tc.userPrefix == "" {
				tc.userPrefix = examplebroker.UserIntegrationPreCheckPrefix
			}
			if user == "" {
				user = vhsTestUserNameFull(t, tc.userPrefix, "ssh")
			}

			var userClient authd.UserServiceClient
			if tc.socketPath == "" {
				conn, err := grpc.NewClient("unix://"+socketPath,
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithUnaryInterceptor(errmessages.FormatErrorMessage))
				require.NoError(t, err, "Setup: could not dial the server")
				t.Cleanup(func() { conn.Close() })

				require.NoError(t, grpcutils.WaitForConnection(context.TODO(), conn,
					sleepDuration(5*time.Second)))

				userClient = authd.NewUserServiceClient(conn)

				if tc.wantUserAlreadyExist {
					requireAuthdUser(t, userClient, user)
				} else {
					requireNoAuthdUser(t, userClient, user)
				}
			}

			sshdPort := defaultSSHDPort
			userHome := defaultUserHome
			if !sharedSSHd || tc.wantLocalGroups || tc.oldDB != "" ||
				tc.interactiveShell || tc.socketPath != "" {
				sshdEnv := sshdEnv
				if nssLibrary != "" {
					sshdEnv = slices.Clone(sshdEnv)
					sshdEnv = append(sshdEnv, nssEnv...)
					sshdEnv = append(sshdEnv, fmt.Sprintf("AUTHD_NSS_SOCKET=%s", socketPath))

					// Let's give authd access to its own NSS module via the socket.
					err := os.Symlink(socketPath, authdSocketLink)
					require.NoError(t, err, "Setup: symlinking the authd socket")
				}
				serviceFile := createSshdServiceFile(t, execModule, execChild,
					pamMkHomeDirModule, socketPath)
				sshdPort, userHome = startSSHdForTest(t, serviceFile, sshdHostKey, user,
					sshdPreloadLibraries, sshdEnv, tc.daemonizeSSHd, tc.interactiveShell)
			}

			if !sharedSSHd {
				_, err := os.Stat(userHome)
				require.ErrorIs(t, err, os.ErrNotExist, "Unexpected error checking for %q", userHome)
			}

			knownHost := filepath.Join(t.TempDir(), "known_hosts")
			err := os.WriteFile(knownHost, []byte(
				fmt.Sprintf("[localhost]:%s %s", sshdPort, pubKey),
			), 0600)
			require.NoError(t, err, "Setup: can't create known hosts file")

			outDir := t.TempDir()
			td := newTapeData(tc.tape, append(defaultTapeSettings, tc.tapeSettings...)...)
			td.Command = tapeCommand
			td.Env[pam_test.RunnerEnvSupportsConversation] = "1"
			td.Env["HOME"] = t.TempDir()
			td.Env[pamSSHUserEnv] = user
			td.Env["AUTHD_PAM_SSH_ARGS"] = strings.Join([]string{
				"-p", sshdPort,
				"-F", os.DevNull,
				"-i", os.DevNull,
				"-o", "ServerAliveInterval=300",
				"-o", "PasswordAuthentication=no",
				"-o", "PubkeyAuthentication=no",
				"-o", "UserKnownHostsFile=" + knownHost,
			}, " ")
			td.Variables = tc.tapeVariables
			td.RunVhs(t, vhsTestTypeSSH, outDir, nil)
			got := sanitizeGoldenFile(t, td, outDir)
			golden.CheckOrUpdate(t, got)

			userEnv := fmt.Sprintf("USER=%s", strings.ToLower(user))
			if tc.wantNotLoggedInUser {
				require.NotContains(t, got, userEnv, "Should not have a logged in user")

				if userClient != nil {
					requireNoAuthdUser(t, userClient, user)
				}
				if nssLibrary != "" {
					requireGetEntExists(t, nssLibrary, socketPath, user, tc.isLocalUser)
				}
			} else {
				require.Contains(t, got, userEnv, "Logged in user does not matches")

				if userClient != nil {
					authdUser := requireAuthdUser(t, userClient, user)
					group := requireAuthdGroup(t, userClient, authdUser.Gid)
					require.Contains(t, group.Members, authdUser.Name,
						"Group lacks of the expected user")

					if nssLibrary != "" {
						userHome = authdUser.Homedir

						requireGetEntEqualsUser(t, nssLibrary, socketPath, user, authdUser)
						requireGetEntEqualsGroup(t, nssLibrary, socketPath, group.Name, group)
					}
				}

				if !tc.wantUserAlreadyExist {
					// Check if user home has been created, but only if the user is a new one.
					stat, err := os.Stat(userHome)
					require.NoError(t, err, "Error checking for %q", userHome)
					require.True(t, stat.IsDir(), "%q is not a directory", userHome)
				}
			}

			if tc.wantLocalGroups || tc.oldDB != "" {
				actualGroups, err := os.ReadFile(groupsFile)
				require.NoError(t, err, "Failed to read the groups file")
				golden.CheckOrUpdate(t, string(actualGroups), golden.WithSuffix(".groups"))
			}

			localgroupstestutils.RequireGPasswdOutput(t, gpasswdOutput, golden.Path(t)+".gpasswd_out")
		})
	}
}

func sanitizeGoldenFile(t *testing.T, td tapeData, outDir string) string {
	t.Helper()

	golden := td.ExpectedOutput(t, outDir)

	// When sshd is in debug mode, it shows the environment variables, so let's sanitize them
	golden = sshEnvVariablesRegex.ReplaceAllString(golden, "  $1=$${AUTHD_TEST_$1}")

	return sshHostPortRegex.ReplaceAllLiteralString(golden, "${SSH_HOST} port ${SSH_PORT}")
}

func createSshdServiceFile(t *testing.T, module, execChild, mkHomeModule, socketPath string) string {
	t.Helper()

	moduleArgs := []string{
		execChild,
		"socket=" + socketPath,
		fmt.Sprintf("connection_timeout=%d", defaultConnectionTimeout),
		"debug=true",
		"logfile=" + os.Stderr.Name(),
		"--exec-debug",
	}

	if env := testutils.CoverDirEnv(); env != "" {
		moduleArgs = append(moduleArgs, "--exec-env", env)
	}
	if testutils.IsAsan() {
		if o := os.Getenv("ASAN_OPTIONS"); o != "" {
			moduleArgs = append(moduleArgs, "--exec-env",
				fmt.Sprintf("ASAN_OPTIONS=%s", o))
		}
		if o := os.Getenv("LSAN_OPTIONS"); o != "" {
			moduleArgs = append(moduleArgs, "--exec-env",
				fmt.Sprintf("LSAN_OPTIONS=%s", o))
		}
	}

	outDir := t.TempDir()
	pamServiceName := "authd-sshd"
	// Keep control values in sync with debian/pam-configs/authd.in.
	authControl := "[success=ok default=die authinfo_unavail=2 ignore=2]"
	accountControl := "[default=ignore success=ok]"
	notifyState := pam_test.ServiceLine{
		Action: pam_test.Auth, Control: pam_test.Optional, Module: "pam_echo.so",
		Args: []string{fmt.Sprintf("%s finished for user '%%u'", pam_test.RunnerResultActionAuthenticate.Message(""))},
	}
	serviceFile, err := pam_test.CreateService(outDir, pamServiceName, []pam_test.ServiceLine{
		{Action: pam_test.Auth, Control: pam_test.NewControl(authControl), Module: module, Args: moduleArgs},
		// Success case:
		notifyState,
		{Action: pam_test.Auth, Control: pam_test.Sufficient, Module: pam_test.Permit.String()},

		// Ignore case:
		notifyState,
		{Action: pam_test.Auth, Control: pam_test.Optional, Module: "pam_echo.so", Args: []string{"SSH PAM user '%u' using local broker"}},
		{Action: pam_test.Include, Module: "common-auth"},

		{Action: pam_test.Account, Control: pam_test.NewControl(accountControl), Module: module, Args: moduleArgs},
		{
			Action: pam_test.Account, Control: pam_test.Optional, Module: "pam_echo.so",
			Args: []string{fmt.Sprintf("%s finished for user '%%u'", pam_test.RunnerResultActionAcctMgmt.Message(""))},
		},
		{Action: pam_test.Session, Control: pam_test.Optional, Module: mkHomeModule, Args: []string{"debug"}},
		{Action: pam_test.Session, Control: pam_test.Requisite, Module: pam_test.Permit.String()},
	})
	require.NoError(t, err, "Setup: Creation of service file %s", pamServiceName)
	saveArtifactsForDebugOnCleanup(t, []string{serviceFile})

	return serviceFile
}

func startSSHdForTest(t *testing.T, serviceFile, hostKey, user string, preloadLibraries []string, env []string, daemonize bool, interactiveShell bool) (string, string) {
	t.Helper()

	sshdConnectCommand := fmt.Sprintf(
		"/usr/bin/echo ' SSHD: Connected to ssh via authd module! [%s]'",
		t.Name())
	if daemonize {
		// When in daemon mode SSH doesn't show debug infos, so let's
		// handle this manually.
		sshdConnectCommand += "&& env | sort | sed 's/^/  /'"
	}
	if interactiveShell {
		sshdConnectCommand = "/bin/sh"
	}

	homeBase := t.TempDir()
	userHome := filepath.Join(homeBase, user)
	sshdPort := startSSHd(t, hostKey, sshdConnectCommand, append([]string{
		fmt.Sprintf("HOME=%s", homeBase),
		fmt.Sprintf("LD_PRELOAD=%s", strings.Join(preloadLibraries, ":")),
		fmt.Sprintf("AUTHD_TEST_SSH_USER=%s", user),
		fmt.Sprintf("AUTHD_TEST_SSH_HOME=%s", userHome),
		fmt.Sprintf("AUTHD_TEST_SSH_PAM_SERVICE=%s", serviceFile),
	}, env...), daemonize)

	return sshdPort, userHome
}

func sshdCommand(t *testing.T, port, hostKey, forcedCommand string, env []string, daemonize bool) (*exec.Cmd, string, string) {
	t.Helper()

	logFile := ""
	pidFile := ""
	runModeArgs := []string{"-ddd"}

	if daemonize {
		pidFile = filepath.Join(t.TempDir(), "sshd.pid")
		logFile = filepath.Join(t.TempDir(), "sshd-daemon.log")
		saveArtifactsForDebugOnCleanup(t, []string{logFile})

		runModeArgs = []string{
			"-E", logFile,
			"-o", "PidFile=" + pidFile,
			"-o", "LogLevel=DEBUG3",
		}
	}

	// #nosec:G204 - we control the command arguments in tests
	sshd := exec.Command("/usr/sbin/sshd",
		"-f", os.DevNull,
		"-p", port,
		"-h", hostKey,
		"-o", "UsePAM=yes",
		"-o", "KbdInteractiveAuthentication=yes",
		"-o", "AuthenticationMethods=keyboard-interactive",
		"-o", "IgnoreUserKnownHosts=yes",
		"-o", "AuthorizedKeysFile=none",
		"-o", "PermitUserEnvironment=no",
		"-o", "PermitUserRC=no",
		"-o", "ClientAliveInterval=300",
		"-o", "ClientAliveCountMax=3",
		"-o", "ForceCommand="+forcedCommand,
		"-o", "MaxAuthTries=1",
	)
	sshd.Args = append(sshd.Args, runModeArgs...)
	sshd.Env = append(sshd.Env, env...)
	sshd.Env = testutils.AppendCovEnv(sshd.Env)

	return sshd, pidFile, logFile
}

func startSSHd(t *testing.T, hostKey, forcedCommand string, env []string, daemonize bool) string {
	t.Helper()

	// We use this to easily find a free port we can use, without going random
	server := httptest.NewServer(http.HandlerFunc(nil))
	url, err := url.Parse(server.URL)
	require.NoError(t, err, "Setup: Impossible to find a valid port to use")
	sshdPort := url.Port()
	server.Close()

	sshd, sshdPidFile, sshdLogFile := sshdCommand(t, sshdPort, hostKey, forcedCommand, env, daemonize)
	sshdStderr := bytes.Buffer{}
	sshd.Stderr = &sshdStderr
	if testing.Verbose() {
		sshd.Stdout = os.Stdout
		sshd.Stderr = os.Stderr
	}

	t.Log("Launching sshd with", sshd.Env, sshd.Args)
	err = sshd.Start()
	require.NoError(t, err, "Setup: Impossible to start sshd")
	sshdPid := sshd.Process.Pid

	t.Cleanup(func() {
		if testing.Verbose() || !t.Failed() {
			return
		}
		sshdLog := filepath.Join(t.TempDir(), "sshd.log")
		require.NoError(t, os.WriteFile(sshdLog, sshdStderr.Bytes(), 0600),
			"TearDown: Saving sshd log")
		saveArtifactsForDebug(t, []string{sshdLog})
	})

	t.Cleanup(func() {
		if sshd.Process == nil {
			return
		}

		sshdExited := make(chan *os.ProcessState)
		go func() {
			processState, err := sshd.Process.Wait()
			require.NoError(t, err, "TearDown: Waiting SSHd failed")
			sshdExited <- processState
		}()

		t.Log("Waiting for sshd to be terminated")
		select {
		case <-time.After(sleepDuration(5 * time.Second)):
			require.NoError(t, sshd.Process.Kill(), "TearDown: Killing SSHd failed")
			if !testing.Verbose() {
				t.Logf("SSHd stopped (killed)\n ##### STDERR #####\n %s \n ##### END #####",
					sshdStderr.String())
			}
			t.Fatal("SSHd didn't finish in time!")
		case state := <-sshdExited:
			t.Logf("SSHd %v stopped (%s)!", sshdPid, state)
			if !testing.Verbose() {
				t.Logf("##### STDERR #####\n %s \n ##### END #####", sshdStderr.String())
			}
			expectedExitCode := 255
			if daemonize {
				expectedExitCode = 0
			}
			require.Equal(t, expectedExitCode, state.ExitCode(), "TearDown: SSHd exited with %s", state)
		}
	})

	if !daemonize {
		// Sadly we can't wait for SSHd to be ready using net.Dial, since that will make sshd
		// (when in debug mode) not to accept further connections from the actual test, but we
		// can assume we're good.
		t.Logf("SSHd started with pid %d and listening on port %s", sshdPid, sshdPort)
		return sshdPort
	}

	t.Cleanup(func() {
		if !t.Failed() && !testutils.IsVerbose() {
			return
		}
		contents, err := os.ReadFile(sshdLogFile)
		require.NoError(t, err, "TearDown: Reading SSHd log failed")
		t.Logf(" ##### LOG FILE #####\n %s \n ##### END #####", contents)
	})

	t.Cleanup(func() {
		pidFileContent, err := os.ReadFile(sshdPidFile)
		require.NoError(t, err, "TearDown: Reading SSHd pid file failed")
		p := strings.TrimSpace(string(pidFileContent))
		pid, err := strconv.Atoi(p)
		require.NoError(t, err, "TearDown: Parsing SSHd pid file content: %q", p)
		process, err := os.FindProcess(pid)
		require.NoError(t, err, "TearDown: Finding SSHd process")
		err = process.Kill()
		require.NoError(t, err, "TearDown: Killing SSHd process")
		t.Logf("SSHd pid %d killed", pid)
	})

	sshdStarted := make(chan error)
	go func() {
		for {
			conn, err := net.DialTimeout("tcp", ":"+sshdPort, sleepDuration(1*time.Second))
			if errors.Is(err, syscall.ECONNREFUSED) {
				continue
			}
			if err != nil {
				sshdStarted <- err
				return
			}
			conn.Close()
			break
		}

		for {
			_, err := os.Stat(sshdPidFile)
			if !errors.Is(err, os.ErrNotExist) {
				sshdStarted <- err
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-time.After(sleepDuration(5 * time.Second)):
		_ = sshd.Process.Kill()
		if !testing.Verbose() {
			t.Logf("SSHd stopped (killed)\n ##### STDERR #####\n %s \n ##### END #####",
				sshdStderr.String())
		}
		t.Fatal("SSHd didn't start in time!")
	case err := <-sshdStarted:
		require.NoError(t, err, "Setup: SSHd startup checking failed %s",
			sshdStderr.String())
	}
	require.NoError(t, err, "Setup: Waiting SSHd failed")

	pidFileContent, err := os.ReadFile(sshdPidFile)
	require.NoError(t, err, "Setup: Reading SSHd pid file failed")

	t.Logf("SSHd started with pid %d (%s) and listening on port %s",
		sshdPid, strings.TrimSpace(string(pidFileContent)), sshdPort)

	return sshdPort
}
