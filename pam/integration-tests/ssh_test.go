package main_test

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/grpcutils"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/testutils/ptytest"
	localgroupstestutils "github.com/canonical/authd/internal/users/localentries/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// sshTestsHomeBase is unique per process so concurrent LXD invocations
	// (each running their own test binary) don't clobber each other's home
	// directories when they call os.RemoveAll(sshTestsHomeBase) during setup.
	sshTestsHomeBase = filepath.Join(os.TempDir(),
		fmt.Sprintf("authd-tests-%d", os.Getpid()), "home")

	sshEnvVariablesRegex = regexp.MustCompile(`(?m)  (PATH|HOME|PWD|SSH_[A-Z]+)=.*(\n*)($[^ ]{2}.*)?$`)
	sshHostPortRegex     = regexp.MustCompile(`([\d\.:]+) port ([\d:]+)`)
	// OpenSSH's privsep architecture creates a race between the client
	// reading the SSH2_MSG_DISCONNECT packet and the server closing the
	// socket. Depending on timing, the client prints either:
	//   - "Connection closed by ..." (saw EOF before the packet), or
	//   - "Received disconnect ... Too many authentication failures" +
	//     "Disconnected from ..." (read the packet before EOF).
	// Normalize the two-line variant for deterministic golden files.
	sshTooManyAuthFailuresRegex = regexp.MustCompile(
		`(?m)^Received disconnect from \$\{SSH_HOST\} port \$\{SSH_PORT\} Too many authentication failures\n` +
			`Disconnected from \$\{SSH_HOST\} port \$\{SSH_PORT\}\n?`,
	)
	// sshNoiseRegex matches lines that are environment-specific noise and should
	// be stripped before golden-file comparison. This covers:
	//   - sshd debug messages (e.g. "debug1: PAM: establishing credentials")
	//   - NSS library log lines (e.g. "16:00:00 INFO  [nss_authd::client] ...")
	sshNoiseRegex = regexp.MustCompile(`(?m)^(debug\d+: |\d{2}:\d{2}:\d{2} \w+ +\[nss_authd::).*\n?`)

	prepareSSHTestsOnce sync.Once
	sshTestsPrepared    atomic.Bool

	prepareSharedSSHDTestsOnce sync.Once
	sharedSSHDTestsPrepared    atomic.Bool

	execModule, execChild, pamMkHomeDirModule string
	nssEnv                                    []string
	nssLibrary                                string
	sshdPreloadLibraries                      []string
	sshdPreloaderCFlags                       []string
	sshdEnv                                   []string
	sshdHostKeyPath                           string
	sshdHostPubKey                            []byte
)

func TestSSHAuthenticate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	runSharedDaemonTests := testutils.IsRace() || os.Getenv("AUTHD_TESTS_SSHD_SHARED") != ""

	testSSHAuthenticate(t, runSharedDaemonTests)

	if golden.UpdateEnabled() {
		testSSHAuthenticate(t, !runSharedDaemonTests)
	}
}

//nolint:thelper // This is actually a test function!
func testSSHAuthenticate(t *testing.T, sharedSSHD bool) {
	// These tests are flaky, see https://github.com/canonical/authd/issues/1328
	if os.Getenv("AUTHD_SKIP_FLAKY_TESTS") != "" {
		t.Skip("skipping flaky test")
	}

	if uv := getUbuntuVersion(t); uv == 0 || uv < 2404 {
		require.Empty(t, os.Getenv("GITHUB_REPOSITORY"),
			"Golden files need to be updated to run tests on Ubuntu %v", uv)
		t.Skipf("Skipping SSH tests since they require new golden files for Ubuntu %v", uv)
	}

	// Reset one-time setup state so testSSHAuthenticate is re-entrant for
	// -count>1 test runs. By the time testSSHAuthenticate is called again all
	// subtests from the previous run have completed and their temp directories
	// have been cleaned up, so it is safe to rebuild everything from scratch.
	prepareSSHTestsOnce = sync.Once{}
	sshTestsPrepared.Store(false)
	prepareSharedSSHDTestsOnce = sync.Once{}
	sharedSSHDTestsPrepared.Store(false)
	execModule, execChild, pamMkHomeDirModule = "", "", ""
	nssEnv = nil
	nssLibrary = ""
	sshdPreloadLibraries = nil
	sshdPreloaderCFlags = nil
	sshdEnv = nil
	sshdHostKeyPath = ""
	sshdHostPubKey = nil

	currentDir, err := os.Getwd()
	require.NoError(t, err, "Setup: Could not get current directory for the tests")

	prepareSSHTests := func(subtest *testing.T) {
		t.Logf("Preparing SSH pty tests, triggered by %q", subtest.Name())

		execModule = buildExecModuleWithCFlags(t, []string{"-std=c11"}, true)
		execChild = buildPAMExecChild(t)

		mkHomeDirHelper, err := exec.LookPath("mkhomedir_helper")
		require.NoError(t, err, "Setup: mkhomedir_helper not found")
		pamMkHomeDirModule = buildSharedModule(t,
			"Building pam_mkhomedir module",
			[]string{"./pam/integration-tests/pam_mkhomedir/pam_mkhomedir.c"},
			nil,
			[]string{
				fmt.Sprintf("-DMKHOMEDIR_HELPER=%q", mkHomeDirHelper),
			},
			[]string{"-lpam"},
			"pam_mkhomedir_test.so", true)

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

		sources := []string{filepath.Join(currentDir, "/sshd_preloader/sshd_preloader.c")}
		sshdPreloadLibrary := buildSharedModule(t, "Building sshd_preloader library", sources,
			nil, sshdPreloaderCFlags, nil, "sshd_preloader", true)
		sshdPreloadLibraries = append(sshdPreloadLibraries, sshdPreloadLibrary)

		sshdHostKeyPath = filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
		//#nosec:G204 - we control the command arguments in tests
		out, err := exec.Command("ssh-keygen", "-q", "-f", sshdHostKeyPath, "-N", "", "-t", "ed25519").CombinedOutput()
		require.NoError(t, err, "Setup: Failed generating SSH host key: %s", out)
		testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, sshdHostKeyPath)

		sshdHostPubKey, err = os.ReadFile(sshdHostKeyPath + ".pub")
		require.NoError(t, err, "Setup: Can't read sshd host public key")

		testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, sshdHostKeyPath+".pub")

		_ = os.RemoveAll(sshTestsHomeBase)
		err = os.MkdirAll(sshTestsHomeBase, 0750)
		require.NoError(t, err, "Setup: failed to create home base directory")
		// Ensure the entire path is world-traversable so that session processes
		// running as non-root UIDs (e.g. when sshd runs in a root LXD container
		// and the preloader assigns uid=65534 to fake users) can chdir into it.
		for dir := sshTestsHomeBase; dir != os.TempDir(); dir = filepath.Dir(dir) {
			err = os.Chmod(dir, 0777) //nolint:gosec // 0777 is intentional: world traversal needed
			require.NoError(t, err, "Setup: failed to chmod %s", dir)
		}
		t.Cleanup(func() {
			_ = os.RemoveAll(sshTestsHomeBase)
		})

		if !t.Failed() {
			t.Log("Prepared SSH pty tests")
			sshTestsPrepared.Store(true)
		}
	}

	var sharedSSHDPort, sharedSSHDUserHome, sharedAuthdSocket, sharedAuthdGroupOutput string
	prepareSharedSSHDTests := func(subtest *testing.T) {
		t.Logf("Preparing SSH pty tests with shared sshd, triggered by %q", subtest.Name())
		sharedAuthdSocket, sharedAuthdGroupOutput = sharedAuthd(t,
			testutils.WithHomeBaseDir(sshTestsHomeBase))
		serviceFile := createSSHDServiceFile(t, execModule, execChild, pamMkHomeDirModule, sharedAuthdSocket)
		sshdEnv = append(sshdEnv, nssEnv...)
		sshdEnv = append(sshdEnv, fmt.Sprintf("AUTHD_NSS_SOCKET=%s", sharedAuthdSocket))

		sharedSSHDPort, sharedSSHDUserHome = startSSHDForTest(t, serviceFile, sshdHostKeyPath,
			"authd-test-user-sshd-accept-all@example.com", sshdPreloadLibraries, sshdEnv, false)

		if !t.Failed() {
			t.Log("Prepared SSH pty tests with shared sshd")
			sharedSSHDTestsPrepared.Store(true)
		}
	}

	authctlPath, authctlCleanup, err := testutils.BuildAuthctl()
	require.NoError(t, err)
	t.Cleanup(authctlCleanup)

	tests := map[string]struct {
		user             string
		isLocalUser      bool
		userPrefix       string
		pamServiceName   string
		socketPath       string
		interactiveShell bool
		ubuntuVersion    string

		wantUserAlreadyExist bool
		wantNotLoggedInUser  bool
		wantLocalGroups      bool

		test func(t *testing.T, args sshPtyArgs)
	}{
		"Authenticate_user_successfully": {
			test: sshPtySimpleAuth,
		},
		"Authenticate_user_successfully_if_already_registered": {
			user: "user-ssh@example.com",
			test: sshPtySimpleAuth,
		},
		"Authenticate_user_successfully_and_enters_shell": {
			interactiveShell: true,
			test:             sshPtyAuthWithShell,
		},
		// On Ubuntu 24.04 (OpenSSH 9.6p1) there is no check_pam_user() in sshd,
		// so uppercase usernames are handled by authd which normalises them to
		// lowercase and authenticates successfully.
		"Authenticate_user_successfully_with_upper_case_on_ubuntu_24.04": {
			ubuntuVersion: "24.04",
			user: strings.ToUpper(testUserNameFull(t,
				examplebroker.UserIntegrationPreCheckPrefix, "upper-case")),
			test: sshPtySimpleAuth,
		},
		"Authenticate_user_successfully_if_already_registered_with_upper_case_on_ubuntu_24.04": {
			ubuntuVersion: "24.04",
			user:          "USER-SSH2@example.com",
			test:          sshPtySimpleAuth,
		},
		// On Ubuntu 26.04 (OpenSSH 10.2+) check_pam_user() was added to sshd and
		// rejects logins where PAM_USER doesn't match pw_name, so uppercase
		// usernames are denied before authentication even starts.
		"Deny_authentication_if_username_has_uppercase_on_ubuntu_26.04": {
			ubuntuVersion: "26.04",
			user: strings.ToUpper(testUserNameFull(t,
				examplebroker.UserIntegrationPreCheckPrefix, "upper-case")),
			wantNotLoggedInUser: true,
			test:                sshPtyUppercaseRejected,
		},
		"Deny_authentication_if_username_has_uppercase_and_already_registered_on_ubuntu_26.04": {
			ubuntuVersion:       "26.04",
			user:                "USER-SSH2@example.com",
			wantNotLoggedInUser: true,
			test:                sshPtyUppercaseRejected,
		},
		"Authenticate_user_with_mfa": {
			userPrefix: examplebroker.UserIntegrationMfaPrefix,
			test:       sshPtyMfaAuth,
		},
		"Authenticate_user_with_form_mode_with_button": {
			test: sshPtyFormWithButton,
		},
		"Authenticate_user_with_qr_code": {
			test: sshPtyQRCode,
		},
		"Authenticate_user_and_reset_password_while_enforcing_policy": {
			userPrefix: examplebroker.UserIntegrationNeedsResetPrefix,
			test:       sshPtyMandatoryPasswordReset,
		},
		// As in the previous uppercase tests, behavior differs between Ubuntu versions.
		"Authenticate_user_and_reset_password_then_allow_uppercase_re-login_on_ubuntu_24.04": {
			ubuntuVersion: "24.04",
			user: testUserNameFull(t,
				examplebroker.UserIntegrationNeedsResetPrefix+
					examplebroker.UserIntegrationPreCheckValue, "case-insensitive"),
			test: sshPtyMandatoryPasswordResetThenUppercaseSucceeds,
		},
		"Authenticate_user_and_reset_password_then_deny_uppercase_re-login_on_ubuntu_26.04": {
			ubuntuVersion: "26.04",
			user: testUserNameFull(t,
				examplebroker.UserIntegrationNeedsResetPrefix+
					examplebroker.UserIntegrationPreCheckValue, "case-insensitive"),
			test: sshPtyMandatoryPasswordResetThenUppercaseRejected,
		},
		"Authenticate_user_with_mfa_and_reset_password_while_enforcing_policy": {
			userPrefix: examplebroker.UserIntegrationMfaWithResetPrefix,
			test:       sshPtyMfaResetPwqualityAuth,
		},
		"Authenticate_user_with_mfa_and_reset_same_password": {
			userPrefix: examplebroker.UserIntegrationMfaWithResetPrefix,
			test:       sshPtyMfaResetSamePassword,
		},
		"Authenticate_user_and_offer_password_reset": {
			userPrefix: examplebroker.UserIntegrationCanResetPrefix,
			test:       sshPtyOptionalPasswordResetSkip,
		},
		"Authenticate_user_and_accept_password_reset": {
			userPrefix: examplebroker.UserIntegrationCanResetPrefix,
			test:       sshPtyOptionalPasswordResetAccept,
		},
		"Authenticate_user_switching_auth_mode": {
			test: sshPtySwitchAuthMode,
		},
		"Authenticate_user_switching_to_local_broker": {
			wantNotLoggedInUser: true,
			test:                sshPtySwitchLocalBroker,
		},
		"Authenticate_user_and_add_it_to_local_group": {
			userPrefix:      examplebroker.UserIntegrationLocalGroupsPrefix,
			wantLocalGroups: true,
			test:            sshPtySimpleAuth,
		},

		"Remember_last_successful_broker_and_mode": {
			test: sshPtyRememberBrokerAndMode,
		},
		"Autoselect_local_broker_for_local_user": {
			user:                "root",
			isLocalUser:         true,
			wantNotLoggedInUser: true,
			test:                sshPtyLocalUserPreset,
		},
		"Authenticate_user_locks_and_unlocks_it": {
			test: sshPtyLocksUnlocks,
		},

		"Deny_authentication_if_max_attempts_reached": {
			wantNotLoggedInUser: true,
			test:                sshPtyMaxAttempts,
		},
		"Deny_authentication_if_user_does_not_exist_on_ubuntu_24.04": {
			ubuntuVersion:       "24.04",
			user:                examplebroker.UserIntegrationUnexistent,
			wantNotLoggedInUser: true,
			test:                sshPtyUnexistentUser,
		},
		"Deny_authentication_if_user_does_not_exist_on_ubuntu_26.04": {
			ubuntuVersion:       "26.04",
			user:                examplebroker.UserIntegrationUnexistent,
			wantNotLoggedInUser: true,
			test:                sshPtyUnexistentUser,
		},
		"Deny_authentication_if_user_does_not_exist_and_matches_cancel_key_on_ubuntu_24.04": {
			ubuntuVersion:       "24.04",
			user:                "r",
			wantNotLoggedInUser: true,
			test:                sshPtyCancelKeyUser,
		},
		"Deny_authentication_if_user_does_not_exist_and_matches_cancel_key_on_ubuntu_26.04": {
			ubuntuVersion:       "26.04",
			user:                "r",
			wantNotLoggedInUser: true,
			test:                sshPtyCancelKeyUser,
		},
		"Deny_authentication_if_newpassword_does_not_match_required_criteria": {
			userPrefix: examplebroker.UserIntegrationNeedsResetPrefix,
			test:       sshPtyBadPassword,
		},

		"Prevent_user_from_switching_username": {
			test: sshPtySwitchPresetUsername,
		},

		"Exit_authd_if_local_broker_is_selected": {
			wantNotLoggedInUser: true,
			test:                sshPtyLocalBroker,
		},
		"Exit_if_user_is_not_pre-checked_on_ssh_service_on_ubuntu_24.04": {
			ubuntuVersion:       "24.04",
			user:                examplebroker.UserIntegrationPrefix + "ssh-service-not-allowed@example.com",
			pamServiceName:      "sshd",
			wantNotLoggedInUser: true,
			test:                sshPtyLocalSSH,
		},
		"Exit_if_user_is_not_pre-checked_on_ssh_service_on_ubuntu_26.04": {
			ubuntuVersion:       "26.04",
			user:                examplebroker.UserIntegrationPrefix + "ssh-service-not-allowed@example.com",
			pamServiceName:      "sshd",
			wantNotLoggedInUser: true,
			test:                sshPtyLocalSSH,
		},
		"Exit_authd_if_user_sigints": {
			wantNotLoggedInUser: true,
			test:                sshPtySigint,
		},

		"Error_if_cannot_connect_to_authd_on_ubuntu_24.04": {
			ubuntuVersion:       "24.04",
			socketPath:          "/some-path/not-existent-socket",
			wantNotLoggedInUser: true,
			test:                sshPtyConnectionError,
		},
		"Error_if_cannot_connect_to_authd_on_ubuntu_26.04": {
			ubuntuVersion:       "26.04",
			socketPath:          "/some-path/not-existent-socket",
			wantNotLoggedInUser: true,
			test:                sshPtyConnectionError,
		},
	}
	for name, tc := range tests {
		if sharedSSHD {
			name = fmt.Sprintf("%s_with_shared_sshd", name)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// If this test is version-specific, delegate to LXD if needed.
			if tc.ubuntuVersion != "" && testutils.RunTestInLXD(t, tc.ubuntuVersion) {
				return
			}

			if !sshTestsPrepared.Load() {
				t.Log("Waiting for SSH pty tests to be prepared")
				start := time.Now()
				prepareSSHTestsOnce.Do(func() {
					prepareSSHTests(t)
				})
				require.True(t, sshTestsPrepared.Load(), "Setup: preparing SSH pty tests failed")
				t.Logf("SSH pty tests prepared after %.3fs", time.Since(start).Seconds())
			}

			if sharedSSHD && !sharedSSHDTestsPrepared.Load() {
				t.Log("Waiting for shared SSHD pty tests to be prepared")
				start := time.Now()
				prepareSharedSSHDTestsOnce.Do(func() {
					prepareSharedSSHDTests(t)
				})
				require.True(t, sharedSSHDTestsPrepared.Load(), "Setup: creating shared sshd service file failed")
				t.Logf("Shared SSHD pty tests prepared after %.3fs", time.Since(start).Seconds())
			}

			socketPath := sharedAuthdSocket
			groupOutput := sharedAuthdGroupOutput

			var authdEnv []string
			var authdSocketLink string
			if nssLibrary != "" {
				authdEnv = slices.Clone(nssEnv)

				socketDir, err := os.MkdirTemp("", "authd-sockets")
				require.NoError(t, err, "Setup: failed to create socket dir")
				authdSocketLink = filepath.Join(socketDir, "authd.sock")
				t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

				authdEnv = append(authdEnv, nssTestEnv(t, nssLibrary, authdSocketLink)...)
			}

			if tc.wantLocalGroups {
				_, groupOutput = prepareGroupFiles(t)

				socketPath = runAuthd(t,
					testutils.WithCurrentUserAsRoot,
					testutils.WithGroupFile(groupOutput),
					testutils.WithEnvironment(authdEnv...),
					testutils.WithHomeBaseDir(sshTestsHomeBase),
				)
			} else if !sharedSSHD {
				socketPath, groupOutput = sharedAuthd(t,
					testutils.WithGroupFileOutput(sharedAuthdGroupOutput),
					testutils.WithEnvironment(authdEnv...),
					testutils.WithHomeBaseDir(sshTestsHomeBase))
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
				user = testUserNameFull(t, tc.userPrefix, "ssh")
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

			sshdPort := sharedSSHDPort
			userHome := sharedSSHDUserHome
			if !sharedSSHD || tc.wantLocalGroups || tc.interactiveShell || tc.socketPath != "" {
				sshdEnv := sshdEnv
				if nssLibrary != "" {
					sshdEnv = slices.Clone(sshdEnv)
					sshdEnv = append(sshdEnv, nssEnv...)
					sshdEnv = append(sshdEnv, fmt.Sprintf("AUTHD_NSS_SOCKET=%s", socketPath))

					err := os.Symlink(socketPath, authdSocketLink)
					require.NoError(t, err, "Setup: symlinking the authd socket")
				}
				serviceFile := createSSHDServiceFile(t, execModule, execChild,
					pamMkHomeDirModule, socketPath)
				sshdPort, userHome = startSSHDForTest(t, serviceFile, sshdHostKeyPath, user,
					sshdPreloadLibraries, sshdEnv, tc.interactiveShell)
			}

			// When golden update is enabled, testSSHAuthenticate is called twice
			// (once with sharedSSHD=false, once with sharedSSHD=true) and all subtests
			// run in parallel. The two variants share the same user names, so the
			// sharedSSHD variant may create the home directory before this check runs.
			if !sharedSSHD && !golden.UpdateEnabled() {
				_, err := os.Stat(userHome)
				require.ErrorIs(t, err, os.ErrNotExist, "Unexpected error checking for %q", userHome)
			}

			args := sshPtyArgs{
				user:        user,
				sshdPort:    sshdPort,
				hostPubKey:  sshdHostPubKey,
				authctlPath: authctlPath,
				socketPath:  socketPath,
				signalFn: func(username string) {
					testutils.CreateBrokerCompletionSignal(t, socketPath, username)
				},
			}

			tc.test(t, args)

			// After the test interaction is complete, verify user state.
			if tc.wantNotLoggedInUser {
				if userClient != nil {
					requireNoAuthdUser(t, userClient, user)
				}
				if nssLibrary != "" {
					requireGetEntExists(t, nssLibrary, socketPath, user, tc.isLocalUser)
				}
			} else {
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
					stat, err := os.Stat(userHome)
					require.NoError(t, err, "Home directory does not exist: %q", userHome)
					require.True(t, stat.IsDir(), "%q is not a directory", userHome)
				}
			}

			localgroupstestutils.RequireGroupFile(t, groupOutput, golden.Path(t))
		})
	}
}

func createSSHDServiceFile(t *testing.T, module, execChild, mkHomeModule, socketPath string) string {
	t.Helper()

	var pamLog string
	if testutils.TestVerbosity() >= 1 {
		pamLog = os.Stderr.Name()
	} else {
		pamLog = filepath.Join(t.TempDir(), "authd-pam.log")
		f, err := os.Create(pamLog)
		require.NoError(t, err, "Setup: Could not create pam_authd log file")
		err = f.Close()
		require.NoError(t, err, "Setup: Could not close pam_authd log file")
		testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, pamLog)
	}

	moduleArgs := []string{
		execChild,
		"socket=" + socketPath,
		fmt.Sprintf("connection_timeout=%d", defaultConnectionTimeout),
		"debug=true",
		"logfile=" + pamLog,
		"--exec-debug",
	}

	if env := testutils.CoverDirEnv(); env != "" {
		moduleArgs = append(moduleArgs, "--exec-env", env)
	}
	if testutils.IsRace() {
		moduleArgs = append(moduleArgs, "--exec-env", "GORACE")
	}
	if testutils.IsAsan() {
		moduleArgs = append(moduleArgs, "--exec-env", "ASAN_OPTIONS")
		moduleArgs = append(moduleArgs, "--exec-env", "LSAN_OPTIONS")
	}

	outDir := t.TempDir()

	// Point pam_mkhomedir at a dedicated skeleton directory rather than the
	// host's /etc/skel. We seed it with .bashrc and .profile so that shells
	// started in the new home behave normally, but omit everything else: on CI
	// runners /etc/skel can contain a large rustup toolchain.
	skelDir := filepath.Join(outDir, "skel")
	require.NoError(t, os.MkdirAll(skelDir, 0755), //nolint:gosec // 0755: root helper must traverse it
		"Setup: failed to create empty skel directory")
	for _, name := range []string{".bashrc", ".profile"} {
		data, err := os.ReadFile(filepath.Join("/etc/skel", name))
		require.NoError(t, err, "Setup: failed to read %s from /etc/skel", name)
		require.NoError(t, os.WriteFile(filepath.Join(skelDir, name), data, 0644), //nolint:gosec // 0644: world-readable dotfile
			"Setup: failed to write %s into skel directory", name)
	}

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
		{Action: pam_test.Auth, Control: pam_test.Required, Module: "pam_unix.so"},

		{Action: pam_test.Account, Control: pam_test.NewControl(accountControl), Module: module, Args: moduleArgs},
		{
			Action: pam_test.Account, Control: pam_test.Optional, Module: "pam_echo.so",
			Args: []string{fmt.Sprintf("%s finished for user '%%u'", pam_test.RunnerResultActionAcctMgmt.Message(""))},
		},
		{Action: pam_test.Session, Control: pam_test.Optional, Module: mkHomeModule, Args: []string{"debug", "skel=" + skelDir}},
		{Action: pam_test.Session, Control: pam_test.Requisite, Module: pam_test.Permit.String()},
	})
	require.NoError(t, err, "Setup: Creation of service file %s", pamServiceName)
	testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, serviceFile)

	return serviceFile
}

func startSSHDForTest(t *testing.T, serviceFile, hostKey, user string, preloadLibraries []string, env []string, interactiveShell bool) (string, string) {
	t.Helper()

	sshdConnectCommand := fmt.Sprintf(
		"/usr/bin/echo ' SSHD: Connected to ssh via authd module! [%s]' && env | sort | sed 's/^/  /'",
		t.Name())
	if interactiveShell {
		sshdConnectCommand += "&& /bin/sh"
	}

	userHome := filepath.Join(sshTestsHomeBase, user)
	sshdPort := startSSHD(t, hostKey, sshdConnectCommand, append([]string{
		fmt.Sprintf("HOME=%s", sshTestsHomeBase),
		fmt.Sprintf("LD_PRELOAD=%s", strings.Join(preloadLibraries, ":")),
		fmt.Sprintf("AUTHD_TEST_SSH_USER=%s", user),
		fmt.Sprintf("AUTHD_TEST_SSH_HOME_BASE=%s", sshTestsHomeBase),
		fmt.Sprintf("AUTHD_TEST_SSH_PAM_SERVICE=%s", serviceFile),
	}, env...))

	return sshdPort, userHome
}

func sshdCommand(t *testing.T, port, hostKey, forcedCommand string, env []string) (*exec.Cmd, string) {
	t.Helper()

	pidFile := filepath.Join(t.TempDir(), "sshd.pid")

	// #nosec:G204 - we control the command arguments in tests
	sshd := exec.Command("/usr/sbin/sshd",
		"-f", os.DevNull,
		"-p", port,
		"-h", hostKey,
		"-D",
		"-e",
		"-o", "LogLevel=DEBUG3",
		"-o", "PidFile="+pidFile,
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
		// Raise MaxStartups well above the default (10:30:100) to prevent
		// connections from being reset during key exchange when many parallel
		// test subtests share the same sshd instance and connect concurrently.
		// The start:rate:full format must be used explicitly: a single-number
		// value only sets the "full" field, leaving "start" and "rate" at
		// their defaults (10 and 30%), which still causes drops at 10+ connections.
		"-o", "MaxStartups=200:100:200",
	)
	sshd.Env = append(sshd.Env, env...)
	sshd.Env = testutils.AppendCovEnv(sshd.Env)

	return sshd, pidFile
}

func startSSHD(t *testing.T, hostKey, forcedCommand string, env []string) string {
	t.Helper()

	// We use this to easily find a free port we can use, without going random
	server := httptest.NewServer(http.HandlerFunc(nil))
	url, err := url.Parse(server.URL)
	require.NoError(t, err, "Setup: Impossible to find a valid port to use")
	sshdPort := url.Port()
	server.Close()

	sshd, sshdPidFile := sshdCommand(t, sshdPort, hostKey, forcedCommand, env)

	sshdOutput := &testutils.SyncBuffer{}

	// Write stdout/stderr both to our stdout/stderr and to the buffer
	sshd.Stdout = io.MultiWriter(t.Output(), sshdOutput)
	sshd.Stderr = sshdOutput

	testlog.LogCommand(t, "Starting sshd", sshd)
	start := time.Now()
	err = sshd.Start()
	require.NoError(t, err, "Setup: Impossible to start sshd")
	sshdPid := sshd.Process.Pid

	testutils.MaybeSaveBufferAsArtifactOnCleanup(t, sshdOutput, "sshd.log")

	t.Cleanup(func() {
		if sshd.Process == nil {
			return
		}

		sshdExited := make(chan *os.ProcessState)
		go func() {
			processState, err := sshd.Process.Wait()
			require.NoError(t, err, "TearDown: Waiting sshd failed")
			sshdExited <- processState
		}()

		t.Log("Teardown: Waiting for sshd to be terminated")
		select {
		case <-time.After(sleepDuration(5 * time.Second)):
			require.NoError(t, sshd.Process.Kill(), "TearDown: Killing sshd failed")
			t.Fatal("sshd didn't exit in time!")
		case state := <-sshdExited:
			t.Logf("sshd[%v] exited (%s)", sshdPid, state)
			expectedExitCode := -1
			require.Equal(t, expectedExitCode, state.ExitCode(), "TearDown: sshd exited with %s", state)
		}
	})

	t.Cleanup(func() {
		pidFileContent, err := os.ReadFile(sshdPidFile)
		require.NoError(t, err, "TearDown: Reading sshd pid file failed")
		p := strings.TrimSpace(string(pidFileContent))
		pid, err := strconv.Atoi(p)
		require.NoError(t, err, "TearDown: Parsing sshd pid file content: %q", p)
		process, err := os.FindProcess(pid)
		require.NoError(t, err, "TearDown: Finding sshd process")
		err = process.Kill()
		require.NoError(t, err, "TearDown: Killing sshd process")
		t.Logf("Teardown: Sent SIGKILL to sshd[%d]", pid)
	})

	// Log the sshd process tree just before it gets killed, to help diagnose
	// hangs where the session setup doesn't complete (e.g. pam_mkhomedir waiting
	// on mkhomedir_helper). This cleanup runs first (LIFO order).
	t.Cleanup(func() {
		ptytest.LogProcessTree(t, "sshd", sshdPid)
	})

	sshdStarted := make(chan error)
	go func() {
		for {
			conn, err := net.DialTimeout("tcp", ":"+sshdPort, sleepDuration(1*time.Second))
			if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
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
		t.Fatal("sshd didn't start in time!")
	case err := <-sshdStarted:
		require.NoError(t, err, "Setup: sshd startup checking failed")
	}
	require.NoError(t, err, "Setup: Waiting sshd failed")

	pidFileContent, err := os.ReadFile(sshdPidFile)
	require.NoError(t, err, "Setup: Reading sshd pid file failed")

	duration := time.Since(start)
	testlog.LogEndSeparatorf(t, "sshd started in %.3fs - pid: %d (%s), listen port: %s",
		duration.Seconds(), sshdPid, strings.TrimSpace(string(pidFileContent)), sshdPort)

	return sshdPort
}

// sshPtyArgs contains parameters for starting SSH in a pty.
type sshPtyArgs struct {
	user        string
	sshdPort    string
	hostPubKey  []byte
	authctlPath string
	socketPath  string
	signalFn    func(username string)
}

// startSSHForPty starts an SSH session in a ptytest Console.
func startSSHForPty(t *testing.T, args sshPtyArgs) *ptytest.Console {
	t.Helper()

	return startSSHForPtyWithUser(t, args, args.user)
}

// startSSHForPtyWithUser starts an SSH session with a specific user.
func startSSHForPtyWithUser(t *testing.T, args sshPtyArgs, user string) *ptytest.Console {
	t.Helper()

	knownHost := filepath.Join(t.TempDir(), "known_hosts")
	err := os.WriteFile(knownHost, []byte(
		fmt.Sprintf("[localhost]:%s %s", args.sshdPort, args.hostPubKey),
	), 0600)
	require.NoError(t, err, "Setup: can't create known hosts file")

	sshArgs := []string{
		fmt.Sprintf("%s@localhost", user),
		"-p", args.sshdPort,
		"-F", os.DevNull,
		"-i", os.DevNull,
		"-o", "ServerAliveInterval=300",
		"-o", "PasswordAuthentication=no",
		"-o", "PubkeyAuthentication=no",
		"-o", "UserKnownHostsFile=" + knownHost,
	}

	env := testutils.AppendCovEnv(nil)
	env = append(env, testutils.MinimalPathEnv)
	env = append(env, "TERM=xterm-256color")

	ptyOpts := []ptytest.Option{
		ptytest.WithEnv(env),
		ptytest.WithSize(terminalWidth, 50),
		ptytest.WithTimeout(30 * time.Second),
	}

	return ptytest.Start(t, "ssh", sshArgs, ptyOpts...)
}

// sshPtySanitizeOutput sanitizes SSH terminal output for golden file comparison.
func sshPtySanitizeOutput(t *testing.T, rawOutput string) string {
	t.Helper()

	s := ptytest.ProcessRawOutput(rawOutput)

	// Collapse runs of 3+ blank lines to just 2.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}

	// Remove leading/trailing blank lines.
	s = strings.TrimLeft(s, "\n")
	s = strings.TrimRight(s, "\n \t")
	s += "\n"

	// Strip environment-specific noise lines (sshd debug messages, NSS log lines).
	s = sshNoiseRegex.ReplaceAllString(s, "")

	// Replace socket path references.
	s = ptyUnixSocketRegex.ReplaceAllLiteralString(s, "unix:///authd/test_socket.sock")

	// Sanitize SSH environment variables.
	s = sshEnvVariablesRegex.ReplaceAllString(s, "  $1=$${AUTHD_TEST_$1}")

	// Sanitize host:port references.
	s = sshHostPortRegex.ReplaceAllLiteralString(s, "${SSH_HOST} port ${SSH_PORT}")

	return s
}

// sshPtySelectBroker waits for the provider selection and selects ExampleBroker.
func sshPtySelectBroker(t *testing.T, c *ptytest.Console) {
	t.Helper()

	c.WaitFor(t, `Choose your provider`)
	sendEchoedLine(t, c, "2")
}

// sshPtyWaitForSSHConnection waits for the SSH connection output.
func sshPtyWaitForSSHConnection(t *testing.T, c *ptytest.Console) {
	t.Helper()

	c.WaitFor(t, `SSHD: Connected to ssh via authd module!`)
}

// --- Test functions ---

func sshPtySimpleAuth(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyAuthWithShell(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	sshPtyWaitForSSHConnection(t, c)

	// Wait for the shell prompt.
	c.WaitFor(t, `\$`)

	// Check user.
	c.SendLine(t, "echo $USER")
	c.WaitFor(t, strings.ToLower(args.user))

	// Check we're inside SSH.
	c.SendLine(t, `[ -n "${SSH_CONNECTION}" ] && echo "Inside SSH"`)
	c.WaitFor(t, `Inside SSH`)

	// Exit shell with Ctrl+D.
	c.SendKey(t, ptytest.KeyCtrlD)
	c.WaitFor(t, `Connection to localhost closed`)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyUppercaseRejected(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	// sshd 10.2 rejects uppercase usernames because PAM_USER doesn't match pw_name.
	// The PAM module detects this and returns a helpful error.
	out := c.WaitFor(t, `uppercase characters|Disconnected from|Connection closed|Choose your provider`)
	if strings.Contains(out, "Choose your provider") {
		c.SendKey(t, ptytest.KeyCtrlC)
	}
	c.Close(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMfaAuth(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel and go to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `1\. Password authentication`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select password auth.
	c.SendLine(t, "1")
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// MFA: fido device.
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Cancel and go back to auth method selection (MFA modes only).
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `1\. Use your fido device foo`)
	c.WaitFor(t, `Choose your authentication method`)

	// Re-select fido auth.
	c.SendLine(t, "1")
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Cancel again.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `2\. Use your phone \+33`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select phone auth.
	c.SendLine(t, "2")
	c.WaitFor(t, `Unlock your phone \+33`)

	// Advance.
	args.signalFn(args.user)
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Complete.
	args.signalFn(args.user)
	c.SendKey(t, ptytest.KeyEnter)
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyFormWithButton(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `8\. Authentication code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Authentication code.
	sendEchoedLine(t, c, "8")
	c.WaitFor(t, `Choose action`)

	// Select "resend sms" button.
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)

	// Select "enter credential".
	sendEchoedLine(t, c, "1")
	c.WaitFor(t, `Enter your one time credential`)

	sendEchoedLine(t, c, "temporary pass00")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyQRCode(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `2\. Use a Login code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Login code mode.
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)

	// Regenerate QR code several times.
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)

	// Accept the code.
	args.signalFn(args.user)
	sendEchoedLine(t, c, "1")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtySwitchAuthMode(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `3\. Send URL to `)
	c.WaitFor(t, `Choose your authentication method`)

	// Select "Send URL to" mode.
	sendEchoedLine(t, c, "3")
	c.WaitFor(t, `enter the code`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `4\. Use your fido device foo`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select fido.
	sendEchoedLine(t, c, "4")
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `5\. Use your phone \+33`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select phone +33.
	sendEchoedLine(t, c, "5")
	c.WaitFor(t, `Unlock your phone \+33`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `6\. Use your phone \+1`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select phone +1.
	sendEchoedLine(t, c, "6")
	c.WaitFor(t, `Unlock your phone \+1`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `7\. Pin code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select pin code.
	sendEchoedLine(t, c, "7")
	c.WaitFor(t, `Enter your pin code`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `2\. Use a Login code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Login code (QR).
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Choose action`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `8\. Authentication code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Authentication code.
	sendEchoedLine(t, c, "8")
	c.WaitFor(t, `Choose action`)

	// Go back.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your authentication method`)

	// Try invalid selection.
	sendEchoedLine(t, c, "invalid-selection")
	c.WaitFor(t, `Choose your authentication method`)

	// Try negative number.
	sendEchoedLine(t, c, "-1")
	c.WaitFor(t, `7\. Pin code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Pin code.
	sendEchoedLine(t, c, "7")
	c.WaitFor(t, `Enter your pin code`)

	// Enter pin.
	sendEchoedLine(t, c, "4242")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtySwitchLocalBroker(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `Choose your authentication method`)

	// Go back to provider selection.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your provider`)

	// Try invalid ID.
	sendEchoedLine(t, c, "invalid-ID")
	c.WaitFor(t, `Unsupported input`)
	c.WaitFor(t, `Choose your provider`)

	// Try invalid number.
	sendEchoedLine(t, c, "555")
	c.WaitFor(t, `Invalid selection`)
	c.WaitFor(t, `Choose your provider`)

	// Select local broker.
	sendEchoedLine(t, c, "1")
	c.WaitFor(t, `Password:`)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	c.Close(t)
	golden.CheckOrUpdate(t, got)
}

func sshPtySwitchPresetUsername(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	c.WaitFor(t, `Choose your provider`)

	// Try going back with 'r' (should stay at provider selection with preset user).
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your provider`)

	// Select ExampleBroker.
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method.
	c.SendLine(t, "r")
	c.WaitFor(t, `Choose your authentication method`)

	// Go back to provider.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your provider`)

	// Try 'r' again at provider level.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your provider`)

	// And again.
	sendEchoedLine(t, c, "r")
	c.WaitFor(t, `Choose your provider`)

	// Select ExampleBroker and authenticate.
	sendEchoedLine(t, c, "2")
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyRememberBrokerAndMode(t *testing.T, args sshPtyArgs) {
	t.Helper()

	// First login: select broker and auth code mode.
	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Cancel to auth method selection.
	c.SendLine(t, "r")
	c.WaitFor(t, `8\. Authentication code`)
	c.WaitFor(t, `Choose your authentication method`)

	// Select Authentication code.
	sendEchoedLine(t, c, "8")
	c.WaitFor(t, `Choose action`)

	// Enter credential.
	sendEchoedLine(t, c, "1")
	c.WaitFor(t, `Enter your one time credential`)
	sendEchoedLine(t, c, "temporary pass0")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	// Second login: broker and mode should be remembered.
	c2 := startSSHForPty(t, args)

	// Should go directly to the remembered auth mode.
	c2.WaitFor(t, `Choose action`)

	sendEchoedLine(t, c2, "1")
	c2.WaitFor(t, `Enter your one time credential`)
	sendEchoedLine(t, c2, "temporary pass0")
	sshPtyWaitForSSHConnection(t, c2)

	c2.RequireSuccessfulExit(t)

	// Combine outputs for the golden file.
	got := sshPtySanitizeOutput(t, c.RawOutput()) + sshPtySanitizeOutput(t, c2.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyLocksUnlocks(t *testing.T, args sshPtyArgs) {
	t.Helper()

	// First auth: succeed.
	c := startSSHForPty(t, args)
	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())

	// Lock the user.
	//#nosec:G204 - we control the command arguments in tests
	lockCmd := exec.Command(args.authctlPath, "user", "lock", args.user)
	lockCmd.Env = append(os.Environ(), "AUTHD_SOCKET="+args.socketPath)
	out, err := lockCmd.CombinedOutput()
	require.NoError(t, err, "Setup: locking user failed: %s", out)
	got += "> authctl user lock ${USERNAME}\n" + fmt.Sprintln(string(out)) + separator

	// Try to auth while locked - should be rejected with a specific message about the user being locked.
	c2 := startSSHForPty(t, args)
	c2.WaitFor(t, `Gimme your password`)
	c2.SendLine(t, "goodpass")
	c2.WaitFor(t, `permission denied: user .* is locked\s+Received disconnect`)

	c2.RequireExitCode(t, 255)

	got += sshPtySanitizeOutput(t, c2.RawOutput())

	// Lock again (idempotent).
	//#nosec:G204 - we control the command arguments in tests
	lockCmd = exec.Command(args.authctlPath, "user", "lock", args.user)
	lockCmd.Env = append(os.Environ(), "AUTHD_SOCKET="+args.socketPath)
	out, err = lockCmd.CombinedOutput()
	require.NoError(t, err, "Setup: re-locking user failed: %s", out)
	got += "> authctl user lock ${USERNAME}\n" + fmt.Sprintln(string(out)) + separator

	// Unlock the user.
	//#nosec:G204 - we control the command arguments in tests
	unlockCmd := exec.Command(args.authctlPath, "user", "unlock", args.user)
	unlockCmd.Env = append(os.Environ(), "AUTHD_SOCKET="+args.socketPath)
	out, err = unlockCmd.CombinedOutput()
	require.NoError(t, err, "Setup: unlocking user failed: %s", out)
	got += "> authctl user unlock ${USERNAME}\n" + fmt.Sprintln(string(out)) + separator

	// Auth again after unlock.
	c3 := startSSHForPty(t, args)
	c3.WaitFor(t, `Gimme your password`)
	c3.SendLine(t, "goodpass")
	sshPtyWaitForSSHConnection(t, c3)

	c3.RequireSuccessfulExit(t)

	got += sshPtySanitizeOutput(t, c3.RawOutput())

	golden.CheckOrUpdate(t, got)
}

func sshPtyMandatoryPasswordReset(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMandatoryPasswordResetThenUppercaseRejected(t *testing.T, args sshPtyArgs) {
	t.Helper()

	// First auth with lowercase user (initial password reset).
	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	// Second auth with uppercase username - should be rejected.
	upperUser := strings.ToUpper(args.user)
	c2 := startSSHForPtyWithUser(t, args, upperUser)
	c2.WaitFor(t, `uppercase characters|Disconnected from|Connection closed`)

	c2.RequireExitCode(t, 255)

	// Combine outputs.
	got := sshPtySanitizeOutput(t, c.RawOutput()) +
		sshPtySanitizeOutput(t, c2.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMandatoryPasswordResetThenUppercaseSucceeds(t *testing.T, args sshPtyArgs) {
	t.Helper()

	// First auth with lowercase user (initial password reset).
	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	// Second auth with uppercase username - on 24.04 (OpenSSH 9.6p1) there is no
	// check_pam_user(), so authd normalises the username and authenticates successfully.
	// The broker and auth mode are auto-selected from the previous session.
	upperUser := strings.ToUpper(args.user)
	c2 := startSSHForPtyWithUser(t, args, upperUser)
	c2.WaitFor(t, `Gimme your password`)
	c2.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c2)

	c2.RequireSuccessfulExit(t)

	// Combine outputs.
	got := sshPtySanitizeOutput(t, c.RawOutput()) +
		sshPtySanitizeOutput(t, c2.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMfaResetPwqualityAuth(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// MFA: wait for fido device.
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Complete MFA (auto-advance).
	args.signalFn(args.user)
	c.SendKey(t, ptytest.KeyEnter)

	// Password reset with quality checks.
	c.WaitFor(t, `Choose action`)
	sendEchoedLine(t, c, "1")

	c.WaitFor(t, `Enter your new password`)

	// Bad password: dictionary word.
	c.SendLine(t, "password")
	c.WaitFor(t, `The password fails the dictionary check`)

	// Bad password: too short.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "1234")
	c.WaitFor(t, `The password is shorter than`)

	// Good password.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMfaResetSamePassword(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// MFA: wait for fido device.
	c.WaitFor(t, `Plug your fido device and press with your thumb`)

	// Complete MFA.
	args.signalFn(args.user)
	c.SendKey(t, ptytest.KeyEnter)

	// Password reset.
	c.WaitFor(t, `Choose action`)
	sendEchoedLine(t, c, "1")

	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyOptionalPasswordResetSkip(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// Optional reset offered.
	c.WaitFor(t, `Choose action`)

	// Skip the reset.
	sendEchoedLine(t, c, "2")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyOptionalPasswordResetAccept(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// Optional reset offered.
	c.WaitFor(t, `Choose action`)

	// Accept the reset.
	sendEchoedLine(t, c, "1")
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyBadPassword(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)
	c.SendLine(t, "goodpass")

	// Password reset required.
	c.WaitFor(t, `Enter your new password`)

	// Empty password.
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `No password supplied`)

	// Too short.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "1234")
	c.WaitFor(t, `The password is shorter than`)

	// Dictionary word.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "12345678")
	c.WaitFor(t, `The password fails the dictionary check`)

	// Good password.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)

	// Mismatched confirm.
	c.SendLine(t, "123456789")
	c.WaitFor(t, `Password entries don't match`)

	// Empty password again.
	c.WaitFor(t, `Enter your new password`)
	c.SendKey(t, ptytest.KeyEnter)
	c.WaitFor(t, `No password supplied`)

	// Finally, correct password.
	c.WaitFor(t, `Enter your new password`)
	c.SendLine(t, "authd2404")
	c.WaitFor(t, `Confirm Password`)
	c.SendLine(t, "authd2404")
	sshPtyWaitForSSHConnection(t, c)

	c.RequireSuccessfulExit(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyMaxAttempts(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	// Send wrong passwords, waiting for re-prompt after each.
	for i := 0; i < 5; i++ {
		c.SendLine(t, "wrongpass")
		if i < 4 {
			c.WaitFor(t, `Gimme your password`)
		}
	}

	c.WaitFor(t, `Too many authentication failures|Maximum number of authentication attempts reached`)

	c.RequireExitCode(t, 255)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyLocalBroker(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	c.WaitFor(t, `Choose your provider`)
	sendEchoedLine(t, c, "1")
	c.WaitFor(t, `Password:`)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	c.Close(t)
	golden.CheckOrUpdate(t, got)
}

func sshPtyLocalUserPreset(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	c.WaitFor(t, `Password:`)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	c.Close(t)
	golden.CheckOrUpdate(t, got)
}

func sshPtyLocalSSH(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	// User is not pre-checked on ssh service, connection is closed.
	c.WaitFor(t, `Connection closed|Disconnected from|Password:`)

	c.Close(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyCancelKeyUser(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	// User "r" doesn't exist as a real user, SSH connection is closed.
	// In some cases sshd may show a Password: prompt from local PAM before closing.
	c.WaitFor(t, `Connection closed|Disconnected from|Password:`)

	c.Close(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyUnexistentUser(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	// User doesn't exist, SSH connection is closed by sshd.
	// On Ubuntu 24.04 (OpenSSH 9.6p1), authentication is handed off to
	// pam_unix.so which shows a 'Password:' prompt. Starting with OpenSSH
	// 10.2p1 (Ubuntu 26.04+), check_pam_user() was added to sshd and causes
	// auth to fail before pam_unix.so is reached, so no prompt is shown.
	c.WaitFor(t, `Connection closed|Disconnected from|Password:`)

	c.Close(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}

func sshPtyConnectionError(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	// Cannot connect to authd, PAM falls through to pam_unix.so which prompts
	// for a password. Send an empty password so pam_unix.so fails immediately,
	// then wait for the connection to close. This avoids a race where
	// "Connection closed" may or may not appear in the output depending on timing.
	matched := c.WaitFor(t, `Connection closed|Disconnected from|Password:`)
	if strings.Contains(matched, "Password:") {
		c.SendLine(t, "")
		c.WaitFor(t, `Connection closed|Disconnected from`)
	}

	c.Close(t)

	got := sshPtySanitizeOutput(t, c.RawOutput())
	got = sshTooManyAuthFailuresRegex.ReplaceAllString(
		got, "Connection closed by $${SSH_HOST} port $${SSH_PORT}\n")
	golden.CheckOrUpdate(t, got)
}

func sshPtySigint(t *testing.T, args sshPtyArgs) {
	t.Helper()

	c := startSSHForPty(t, args)

	sshPtySelectBroker(t, c)
	c.WaitFor(t, `Gimme your password`)

	c.SendKey(t, ptytest.KeyCtrlC)

	c.RequireExitCode(t, 128+int(syscall.SIGINT))

	got := sshPtySanitizeOutput(t, c.RawOutput())
	golden.CheckOrUpdate(t, got)
}
