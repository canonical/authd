package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildAuthctl builds the authctl binary in a temporary directory for testing purposes.
func BuildAuthctl() (binaryPath string, cleanup func(), err error) {
	tempDir, err := os.MkdirTemp("", "authctl")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(tempDir) }
	binaryPath = filepath.Join(tempDir, "authctl")

	cmd := exec.Command("go", "build")
	cmd.Args = append(cmd.Args, GoBuildFlags()...)
	cmd.Args = append(cmd.Args, "-o", binaryPath, "./cmd/authctl")
	cmd.Dir = ProjectRoot()

	fmt.Fprintln(os.Stderr, "Running command:", cmd.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		fmt.Printf("Command output:\n%s\n", output)
		return "", nil, fmt.Errorf("failed to build authctl: %w", err)
	}

	return binaryPath, cleanup, nil
}
