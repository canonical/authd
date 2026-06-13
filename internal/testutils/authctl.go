package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/canonical/authd/internal/testlog"
)

// AuthctlGoBuildArgs returns the `go build` arguments (run from [ProjectRoot])
// used to build the authctl binary into outputPath. It is the single source of
// truth shared by BuildAuthctl and the LXD build-cache warmup, so their build
// (and thus cache) keys stay in sync.
func AuthctlGoBuildArgs(outputPath string) []string {
	args := []string{"build"}
	args = append(args, GoBuildFlags()...)
	args = append(args, "-o", outputPath, "./cmd/authctl")
	return args
}

// BuildAuthctl builds the authctl binary in a temporary directory for testing purposes.
func BuildAuthctl() (binaryPath string, cleanup func(), err error) {
	tempDir, err := os.MkdirTemp("", "authctl")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(tempDir) }
	binaryPath = filepath.Join(tempDir, "authctl")

	//nolint:gosec // G204 - test-only code; args are controlled by AuthctlGoBuildArgs.
	cmd := exec.Command("go", AuthctlGoBuildArgs(binaryPath)...)
	cmd.Dir = ProjectRoot()

	if err := testlog.RunWithTiming(nil, "Building authctl", cmd); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to build authctl: %w", err)
	}

	return binaryPath, cleanup, nil
}
