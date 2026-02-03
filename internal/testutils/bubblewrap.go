package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
)

var (
	bubbleWrapSupportsUnprivilegedNamespacesOnce sync.Once
	bubbleWrapSupportsUnprivilegedNamespaces     bool

	bubbleWrapNeedsSudoOnce sync.Once
	bubbleWrapNeedsSudo     bool

	copyBwrapOnce   sync.Once
	copiedBwrapPath string
)

const bubbleWrapTestEnvVar = "BUBBLEWRAP_TEST"

// RunningInBubblewrap returns true if the test is being run in bubblewrap.
func RunningInBubblewrap() bool {
	return os.Getenv(bubbleWrapTestEnvVar) == "1"
}

func canRunBubblewrap(t *testing.T) bool {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Log("Running as EUID 0")
		return true
	}

	bubbleWrapSupportsUnprivilegedNamespacesOnce.Do(func() {
		bubbleWrapSupportsUnprivilegedNamespaces = canUseUnprivilegedUserNamespaces(t)
	})
	if bubbleWrapSupportsUnprivilegedNamespaces {
		return true
	}

	bubbleWrapNeedsSudoOnce.Do(func() {
		bubbleWrapNeedsSudo = canUseBwrapWithSudoNonInteractively(t)
	})
	return bubbleWrapNeedsSudo
}

// RunTestInBubbleWrap runs the given test in bubblewrap.
func RunTestInBubbleWrap(t *testing.T, args ...string) {
	t.Helper()

	RequireBubblewrap(t)

	etcDir := filepath.Join(TempDir(t), "etc")
	err := os.MkdirAll(etcDir, 0700)
	require.NoError(t, err, "Setup: could not create etc dir")

	// Copy files needed to create users and groups inside bubblewrap.
	for _, f := range []string{"passwd", "group", "subgid"} {
		err := fileutils.CopyFile("/etc/"+f, filepath.Join(etcDir, f))
		require.NoError(t, err, "Setup: Copying /etc/%s to %s failed", f, etcDir)
	}

	env := []string{
		"PATH=" + MinimalPathEnv,
		bubbleWrapTestEnvVar + "=1",
		golden.UpdateGoldenFilesEnv + "=" + os.Getenv(golden.UpdateGoldenFilesEnv),
	}
	env = AppendCovEnv(env)

	cmd := bubbleWrapCommand(t, env, bubbleWrapNeedsSudo)

	cmd.Args = append(cmd.Args,
		"--bind", etcDir, "/etc",

		// Bind relevant etc files. We go manual here, since there's no
		// need to get much more than those, while we could in theory just
		// bind everything that is in host, and excluding the ones we want
		// to override.
		"--ro-bind", "/etc/environment", "/etc/environment",
		"--ro-bind", "/etc/localtime", "/etc/localtime",
		"--ro-bind", "/etc/login.defs", "/etc/login.defs",
		"--ro-bind", "/etc/nsswitch.conf", "/etc/nsswitch.conf",
		"--ro-bind", "/etc/sudo.conf", "/etc/sudo.conf",
		"--ro-bind", "/etc/sudoers", "/etc/sudoers",
		"--ro-bind-try", "/etc/timezone", "/etc/timezone",
		"--ro-bind", "/etc/pam.d", "/etc/pam.d",
		"--ro-bind", "/etc/security", "/etc/security",

		// Bind the test binary itself so that it can be run in bubblewrap.
		"--bind", os.Args[0], os.Args[0],
	)

	if coverDir := CoverDirForTests(); coverDir != "" {
		cmd.Args = append(cmd.Args, "--bind", coverDir, coverDir)
	}

	goldenDir := golden.Dir(t)
	exists, err := fileutils.FileExists(goldenDir)
	require.NoError(t, err, "Setup: could not check if golden dir exists")
	if exists && golden.UpdateEnabled() {
		// Bind the golden directory read-write so that the tests can update it.
		cmd.Args = append(cmd.Args, "--bind", goldenDir, goldenDir)
	}
	cmd.Args = append(cmd.Args, args...)

	testCommand := []string{os.Args[0], "-test.run", "^" + t.Name() + "$"}
	if testing.Verbose() {
		testCommand = append(testCommand, "-test.v")
	}
	if c := CoverDirForTests(); c != "" {
		testCommand = append(testCommand, fmt.Sprintf("-test.gocoverdir=%s", c))
	}
	cmd.Args = append(cmd.Args, testCommand...)

	testlog.LogCommand(t, fmt.Sprintf("Running %s in bubblewrap", t.Name()), cmd)
	err = cmd.Run()
	if err != nil {
		testlog.LogEndSeparator(t, fmt.Sprintf("%s in bubblewrap failed", t.Name()))
		t.Fatalf("Running %s in bubblewrap failed: %v", t.Name(), err)
	}
	testlog.LogEndSeparator(t, fmt.Sprintf("%s in bubblewrap finished", t.Name()))
}

// RequireBubblewrap ensures that bubblewrap is available and usable for running tests.
// It skips or fails the test if bubblewrap cannot be used in the current environment.
func RequireBubblewrap(t *testing.T) {
	t.Helper()

	if !canRunBubblewrap(t) {
		if IsAutoPkgTest() && !IsCI() {
			// On launchpad builders, we might not be able to run bubblewrap,
			// but we don't want to fail the tests in that case.
			t.Skip("Skipping test: cannot run bubblewrap")
		}
		require.Fail(t, "Cannot run bubblewrap")
	}
}

// BubbleWrapCommand returns a command that runs in bubblewrap.
func BubbleWrapCommand(t *testing.T, env []string) *exec.Cmd {
	t.Helper()

	RequireBubblewrap(t)

	return bubbleWrapCommand(t, env, bubbleWrapNeedsSudo)
}

func bubbleWrapCommand(t *testing.T, env []string, withSudo bool) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd

	// Since 25.10 Ubuntu ships the AppArmor profile /etc/apparmor.d/bwrap-userns-restrict
	// which restricts bwrap and causes chown to fail with "Operation not permitted".
	// We work around that by copying the bwrap binary to a temporary location so that
	// the AppArmor profile is not applied.
	copyBwrapOnce.Do(func() {
		tempDir, err := os.MkdirTemp("", "authd-bwrap-")
		require.NoError(t, err, "Setup: could not create temp dir for bwrap test data")
		copiedBwrapPath = filepath.Join(tempDir, "bwrap")
		err = fileutils.CopyFile("/usr/bin/bwrap", copiedBwrapPath)
		require.NoError(t, err, "Setup: could not copy bubblewrap binary to temp location")
	})

	if withSudo {
		t.Log("Running bubblewrap with sudo")
		cmd = exec.Command("sudo", env...)
		cmd.Args = append(cmd.Args, copiedBwrapPath)
	} else {
		// To be able to use chown in bubblewrap, we need to run it in a user namespace
		// with a uid mapping. Bubblewrap itself only supports mapping a single UID via
		// --uid, so we use unshare to create a new user namespace with the desired mapping
		// and run bwrap in that.
		//nolint:gosec // We're not running untrusted code here.
		cmd = exec.Command(
			"unshare",
			"--user",
			"--map-root-user",
			"--map-users=auto",
			"--map-groups=auto",
			copiedBwrapPath,
		)
		cmd.Env = env
	}

	cmd.Args = append(cmd.Args,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	)

	cmd.Stderr = t.Output()
	cmd.Stdout = t.Output()

	return cmd
}

func canUseUnprivilegedUserNamespaces(t *testing.T) bool {
	t.Helper()

	if IsCI() {
		// Try enabling unprivileged user namespaces in the CI.
		cmd := exec.Command("sudo", "sysctl", "-w",
			"kernel.unprivileged_userns_clone=1",
			"kernel.apparmor_restrict_unprivileged_userns=0",
			"user.max_user_namespaces=100000",
		)
		cmd.Stdout = t.Output()
		cmd.Stderr = t.Output()
		_ = cmd.Run()
	}

	// We don't try bubbleWrapCommand directly here, because that uses
	// `unshare --map-user` via exec.Command and connects the process's
	// stdout and stderr, which causes the command to hang forever if
	// unprivileged user namespaces are disabled. We avoid that by first
	// checking via `unshare --map-root-user` if unprivileged user namespaces
	// are enabled.
	cmd := exec.Command("unshare", "--map-root-user", "/bin/true")
	cmd.Stdout = t.Output()
	cmd.Stderr = t.Output()
	testlog.LogCommand(t, "Checking unprivileged user namespaces", cmd)
	if err := cmd.Run(); err != nil {
		testlog.LogRedEndSeparator(t, "Cannot use unprivileged user namespaces")
		return false
	}
	testlog.LogEndSeparator(t, "Can use unprivileged user namespaces")

	cmd = bubbleWrapCommand(t, nil, false)
	cmd.Args = append(cmd.Args, "/bin/true")
	testlog.LogCommand(t, "Checking bubblewrap with unprivileged user namespaces", cmd)
	if err := cmd.Run(); err != nil {
		testlog.LogRedEndSeparator(t, "Cannot use bubblewrap with unprivileged user namespaces")
		return false
	}

	testlog.LogEndSeparator(t, "Can use unprivileged user namespaces")
	return true
}

func canUseBwrapWithSudoNonInteractively(t *testing.T) bool {
	t.Helper()

	if !canUseSudoNonInteractively(t) {
		return false
	}

	cmd := bubbleWrapCommand(t, nil, true)
	cmd.Args = append(cmd.Args, "/bin/true")
	testlog.LogCommand(t, "Checking bubblewrap with sudo", cmd)
	if err := cmd.Run(); err != nil {
		testlog.LogRedEndSeparatorf(t, "Cannot use bubblewrap with sudo: %v", err)
		return false
	}

	testlog.LogEndSeparator(t, "Can use bubblewrap with sudo")
	return true
}
