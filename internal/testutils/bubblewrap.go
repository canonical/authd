package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/fileutils"
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

// SkipIfCannotRunBubbleWrap checks whether we can run tests running in bubblewrap or
// skip the tests otherwise.
func SkipIfCannotRunBubbleWrap(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Log("Running as EUID 0")
		return
	}

	bubbleWrapSupportsUnprivilegedNamespacesOnce.Do(func() {
		bubbleWrapSupportsUnprivilegedNamespaces = canUseUnprivilegedUserNamespaces(t)
	})
	if bubbleWrapSupportsUnprivilegedNamespaces {
		return
	}

	bubbleWrapNeedsSudoOnce.Do(func() {
		bubbleWrapNeedsSudo = canUseBwrapWithSudoNonInteractively(t)
	})
	if bubbleWrapNeedsSudo {
		return
	}

	t.Skip("Skipping test: requires root privileges or unprivileged user namespaces")
}

// RunTestInBubbleWrap runs the given test in bubblewrap.
func RunTestInBubbleWrap(t *testing.T, args ...string) {
	t.Helper()

	SkipIfCannotRunBubbleWrap(t)

	testCommand := []string{os.Args[0], "-test.run", "^" + t.Name() + "$"}
	if testing.Verbose() {
		testCommand = append(testCommand, "-test.v")
	}
	if c := CoverDirForTests(); c != "" {
		testCommand = append(testCommand, fmt.Sprintf("-test.gocoverdir=%s", c))
	}
	args = append(args, testCommand...)

	t.Logf("Running %s in bubblewrap", t.Name())
	err := runInBubbleWrap(t, bubbleWrapNeedsSudo, "", nil, args...)
	if err != nil {
		t.Fatalf("Running %s in bubblewrap failed: %v", t.Name(), err)
	}
}

func runInBubbleWrap(t *testing.T, withSudo bool, testDataPath string, env []string, args ...string) error {
	t.Helper()

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

	// To be able to use chown in bubblewrap, we need to run it in a user namespace
	// with a uid mapping. Bubblewrap itself only supports mapping a single UID via
	// --uid, so we use unshare to create a new user namespace with the desired mapping
	// and run bwrap in that.
	//nolint:gosec // We're not running untrusted code here.
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--map-auto",
		copiedBwrapPath)

	cmd.Env = AppendCovEnv(os.Environ())
	cmd.Env = append(cmd.Env, env...)
	cmd.Env = append(cmd.Env, bubbleWrapTestEnvVar+"=1")

	if withSudo {
		cmd.Args = append([]string{"sudo"}, cmd.Args...)
	}

	if testDataPath == "" {
		testDataPath = TempDir(t)
	}

	etcDir := filepath.Join(testDataPath, "etc")
	err := os.MkdirAll(etcDir, 0700)
	require.NoError(t, err, "Setup: could not create etc dir")

	// Copy files needed to create users and groups inside bubblewrap.
	for _, f := range []string{"passwd", "group", "subgid"} {
		err := fileutils.CopyFile("/etc/"+f, filepath.Join(etcDir, f))
		require.NoError(t, err, "Setup: Copying /etc/%s to %s failed", f, etcDir)
	}

	cmd.Args = append(cmd.Args,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--bind", os.TempDir(), os.TempDir(),
		"--bind", testDataPath, testDataPath,
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
	)

	if coverDir := CoverDirForTests(); coverDir != "" {
		cmd.Args = append(cmd.Args, "--bind", coverDir, coverDir)
	}

	cmd.Args = append(cmd.Args, args...)
	cmd.Stderr = t.Output()
	cmd.Stdout = t.Output()

	t.Log("Running command:", cmd.String())
	return cmd.Run()
}

func canUseUnprivilegedUserNamespaces(t *testing.T) bool {
	t.Helper()

	t.Log("Checking if we can use unprivileged user namespaces")

	if err := runInBubbleWrap(t, false, t.TempDir(), nil, "/bin/true"); err != nil {
		t.Logf("Can't use user namespaces: %v", err)
		return false
	}

	t.Log("Can use unprivileged user namespaces")
	return true
}

func canUseBwrapWithSudoNonInteractively(t *testing.T) bool {
	t.Helper()

	if !canUseSudoNonInteractively(t) {
		return false
	}

	if err := runInBubbleWrap(t, true, t.TempDir(), nil, "/bin/true"); err != nil {
		t.Logf("Can't use bubblewrap with sudo: %v", err)
		return false
	}

	t.Log("Can use sudo non-interactively")
	return true
}
