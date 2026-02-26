package nss_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// getentOutputForLib returns the specific part for the nss command for the authd service.
// It uses the locally build authd nss module for the integration tests.
func getentOutputForLib(t *testing.T, libPath, socketPath string, rustCovEnv []string, shouldPreCheck bool, cmds ...string) (got string, exitCode int) {
	t.Helper()

	// #nosec:G204 - we control the command arguments in tests
	cmds = append(cmds, "--service", "authd")
	cmd := exec.Command("getent", cmds...)
	cmd.Env = append(cmd.Env,
		"AUTHD_NSS_INFO=stderr",
		// NSS needs both LD_PRELOAD and LD_LIBRARY_PATH to load the module library
		fmt.Sprintf("LD_PRELOAD=%s:%s", libPath, os.Getenv("LD_PRELOAD")),
		fmt.Sprintf("LD_LIBRARY_PATH=%s:%s", filepath.Dir(libPath), os.Getenv("LD_LIBRARY_PATH")),
	)
	cmd.Env = append(cmd.Env, rustCovEnv...)

	if socketPath != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AUTHD_NSS_SOCKET=%s", socketPath))
	}

	if shouldPreCheck {
		cmd.Env = append(cmd.Env, "AUTHD_NSS_SHOULD_PRE_CHECK=1")
	}

	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(t.Output(), &out)
	cmd.Stderr = t.Output()

	// We are only interested in the output and the exit code of the command, so we can ignore the error.
	_ = cmd.Run()

	return out.String(), cmd.ProcessState.ExitCode()
}
