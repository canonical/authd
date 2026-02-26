package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/testlog"
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

	cmd := exec.Command("sudo", "-n")
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = t.Output()
	cmd.Stderr = t.Output()
	testlog.LogCommand(t, fmt.Sprintf("Running %s as root", t.Name()), cmd)
	err := cmd.Run()
	if err != nil {
		testlog.LogEndSeparator(t, fmt.Sprintf("%s as root failed", t.Name()))
		t.Fatalf("Running %s as root failed: %v", t.Name(), err)
	}
	testlog.LogEndSeparator(t, fmt.Sprintf("%s as root finished", t.Name()))
}

func canUseSudoNonInteractively(t *testing.T) bool {
	t.Helper()

	cmd := exec.Command("sudo", "-n", "true")
	cmd.Stdout = t.Output()
	cmd.Stderr = t.Output()
	testlog.LogCommand(t, "Checking if we can use sudo non-interactively", cmd)
	if err := cmd.Run(); err != nil {
		testlog.LogRedEndSeparator(t, "Cannot use sudo non-interactively")
		return false
	}
	testlog.LogEndSeparator(t, "Can use sudo non-interactively")
	return true
}
