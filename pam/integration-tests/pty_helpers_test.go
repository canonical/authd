package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/ptytest"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/stretchr/testify/require"
)

// ptyRunnerEnv builds the environment variable list for the PAM runner process
// when launched via ptytest.
func ptyRunnerEnv(t *testing.T, cliEnv []string, opts clientOptions) []string {
	t.Helper()

	logFile := prepareFileLogging(t, "authd-pam-test-client.log")

	shellPath, err := exec.LookPath("bash")
	require.NoError(t, err, "Setup: bash not found")

	env := append(testutils.AppendCovEnv(nil), cliEnv...)
	env = append(env,
		testutils.MinimalPathEnv,
		fmt.Sprintf("%s=%s", pam_test.RunnerEnvLogFile, logFile),
		fmt.Sprintf("%s=%s", pam_test.RunnerEnvTestName, t.Name()),
		fmt.Sprintf("%s=1", pam_test.RunnerEnvSupportsConversation),
		"SHELL="+shellPath,
		"HOME="+t.TempDir(),
	)

	timeout := fmt.Sprintf("%d", defaultConnectionTimeout)
	if opts.PamTimeout != "" {
		timeout = opts.PamTimeout
	}
	env = append(env, fmt.Sprintf("%s=%s", pam_test.RunnerEnvConnectionTimeout, timeout))

	if opts.PamUser != "" {
		env = append(env, fmt.Sprintf("%s=%s", pam_test.RunnerEnvUser, opts.PamUser))
	}
	if opts.PamEnv != nil {
		env = append(env, fmt.Sprintf("%s=%s", pam_test.RunnerEnvEnvs, strings.Join(opts.PamEnv, ";")))
	}
	if opts.PamServiceName != "" {
		env = append(env, fmt.Sprintf("%s=%s", pam_test.RunnerEnvService, opts.PamServiceName))
	}
	if opts.Term != "" {
		env = append(env, "AUTHD_PAM_CLI_TERM="+opts.Term)
	}
	if opts.SessionType != "" {
		env = append(env, "XDG_SESSION_TYPE="+opts.SessionType)
	}

	if testutils.IsRace() {
		raceLog := filepath.Join(t.TempDir(), "gorace.log")
		env = append(env, fmt.Sprintf("GORACE=log_path=%s exitcode=0", raceLog))
		t.Cleanup(func() { checkDataRaces(t, raceLog) })
	}

	if testutils.IsAsan() {
		if asanOptions := os.Getenv("ASAN_OPTIONS"); asanOptions != "" {
			env = append(env, "ASAN_OPTIONS="+asanOptions)
		}
		if lsanOptions := os.Getenv("LSAN_OPTIONS"); lsanOptions != "" {
			env = append(env, "LSAN_OPTIONS="+lsanOptions)
		}
	}

	return env
}

// startPAMRunner starts the PAM runner in a ptytest Console, returning the console.
func startPAMRunner(t *testing.T, clientPath string, socketPath string,
	action pam_test.RunnerAction, cliEnv []string, opts clientOptions,
	extraArgs ...string,
) *ptytest.Console {
	t.Helper()

	pamRunnerPath := filepath.Join(clientPath, "pam_authd")
	args := []string{action.String(), "socket=" + socketPath}
	args = append(args, extraArgs...)

	env := ptyRunnerEnv(t, cliEnv, opts)

	ptyOpts := []ptytest.Option{
		ptytest.WithEnv(env),
		ptytest.WithSize(terminalWidth, 50),
		ptytest.WithTimeout(30 * time.Second),
	}

	return ptytest.Start(t, pamRunnerPath, args, ptyOpts...)
}

// ptySanitizeOutput sanitizes terminal output for golden file comparison.
var (
	ptyUnixSocketRegex   = regexp.MustCompile(fmt.Sprintf(`unix://%s/(\S*)\b`, regexp.QuoteMeta(os.TempDir())))
	ptyDirectSocketRegex = regexp.MustCompile(fmt.Sprintf(`%s/\S*/authd\.(?:socket|sock)\b`, regexp.QuoteMeta(os.TempDir())))
)

// terminalWidth is the terminal width used for ptytest sessions.
const terminalWidth = 160

func ptySanitizeOutput(t *testing.T, rawOutput string) string {
	t.Helper()

	// Process raw output through a terminal emulator to resolve cursor
	// movement and screen clearing sequences. This produces deterministic
	// output regardless of bubbletea's internal render batching timing.
	s := ptytest.ProcessRawOutput(rawOutput)

	// Collapse runs of 3+ blank lines to just 2.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}

	// Remove leading/trailing blank lines.
	s = strings.TrimLeft(s, "\n")
	s = strings.TrimRight(s, "\n \t")
	s += "\n"

	// Replace socket path references.
	s = ptyUnixSocketRegex.ReplaceAllLiteralString(s, "unix:///authd/test_socket.sock")
	s = ptyDirectSocketRegex.ReplaceAllLiteralString(s, "/authd/test_socket.sock")

	// Strip UID from permission error messages (makes output deterministic).
	s = permissions.Z_ForTests_IdempotentPermissionError(s)

	return s
}
