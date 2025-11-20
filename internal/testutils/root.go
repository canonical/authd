package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/testutils/golden"
)

var (
	needsSudoOnce sync.Once
	needsSudo     bool
)

// RunningAsRoot returns true if the current process is running as root.
func RunningAsRoot() bool {
	return os.Geteuid() == 0
}

// SkipIfCannotRunAsRoot checks whether we can run tests as root or skip the tests otherwise.
func SkipIfCannotRunAsRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Log("Running as EUID 0")
		return
	}

	needsSudoOnce.Do(func() {
		needsSudo = canUseSudoNonInteractively(t)
	})
	if needsSudo {
		return
	}

	t.Skip("Skipping test: requires root privileges")
}

// RunTestAsRoot runs the given test as root.
func RunTestAsRoot(t *testing.T, args ...string) {
	t.Helper()

	SkipIfCannotRunAsRoot(t)

	if v := os.Getenv(golden.UpdateGoldenFilesEnv); v != "" {
		args = append(args, fmt.Sprintf("%s=%s", golden.UpdateGoldenFilesEnv, v))
	}

	testCommand := []string{os.Args[0], "-test.run", "^" + t.Name() + "$"}
	if testing.Verbose() {
		testCommand = append(testCommand, "-test.v")
	}
	if c := CoverDirForTests(); c != "" {
		testCommand = append(testCommand, fmt.Sprintf("-test.gocoverdir=%s", c))
	}
	args = append(args, testCommand...)

	t.Logf("Running %s as root", t.Name())
	err := runSudoCommand(t, args...)
	if err != nil {
		t.Fatalf("Failed to run test %s as root: %v", t.Name(), err)
	}
}

func runSudoCommand(t *testing.T, args ...string) error {
	t.Helper()

	sudoArgs := append([]string{"-n"}, args...)
	//nolint:gosec // G204 we want to use exec.Command with variables here
	cmd := exec.Command("sudo", sudoArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	t.Log("Running command:", cmd.String())
	return cmd.Run()
}

func canUseSudoNonInteractively(t *testing.T) bool {
	t.Helper()

	t.Log("Checking if we can use sudo non-interactively")

	cmd := exec.Command("sudo", "-n", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("Can't use sudo non-interactively: %v\n%s", err, out)
		return false
	}
	return true
}
