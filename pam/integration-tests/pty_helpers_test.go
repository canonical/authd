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

var separator = strings.Repeat("─", 80) + "\n"

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
		env = append(env, "TERM="+opts.Term)
	} else {
		// Set a color-capable terminal so the QR code renderer uses the compact
		// half-block format (ToSmallString) instead of the larger full-block
		// format (ToString). Without TERM, termenv detects Ascii profile and
		// falls back to the larger format.
		env = append(env, "TERM=xterm-256color")
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

	return startPAMRunnerWithPtyOpts(t, clientPath, socketPath, action, cliEnv, opts, nil, extraArgs...)
}

// startCLIPAMRunner starts the PAM runner with snapshot capture enabled.
// Use ptySanitizeSnapshots to get the golden output from the returned Console.
func startCLIPAMRunner(t *testing.T, clientPath string, socketPath string,
	action pam_test.RunnerAction, cliEnv []string, opts clientOptions,
	extraArgs ...string,
) *ptytest.Console {
	t.Helper()

	return startPAMRunnerWithPtyOpts(t, clientPath, socketPath, action, cliEnv, opts,
		[]ptytest.Option{ptytest.WithSnapshots()}, extraArgs...)
}

// startPAMRunnerWithPtyOpts is like startPAMRunner but allows passing
// additional ptytest options (e.g. WithSnapshots).
func startPAMRunnerWithPtyOpts(t *testing.T, clientPath string, socketPath string,
	action pam_test.RunnerAction, cliEnv []string, opts clientOptions,
	extraPtyOpts []ptytest.Option, extraArgs ...string,
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
	ptyOpts = append(ptyOpts, extraPtyOpts...)

	return ptytest.Start(t, pamRunnerPath, args, ptyOpts...)
}

// ptySanitizeOutput sanitizes terminal output for golden file comparison.
var (
	ptyUnixSocketRegex   = regexp.MustCompile(fmt.Sprintf(`unix://%s/(\S*)\b`, regexp.QuoteMeta(os.TempDir())))
	ptyDirectSocketRegex = regexp.MustCompile(fmt.Sprintf(`%s/\S*/authd\.(?:socket|sock)\b`, regexp.QuoteMeta(os.TempDir())))

	// ptyAuthdUnavailableRegex normalises the two error forms that appear
	// when authd stops while the PAM module is running: a dial failure
	// ("couldn't connect") and a health-check detection ("stopped serving").
	// Which one fires first is a race, so both are collapsed to one stable
	// string for golden-file comparisons.
	ptyAuthdUnavailableRegex = regexp.MustCompile(
		`couldn't connect to authd daemon: connection error: desc = "transport: Error while dialing: dial unix \S+: connect: no such file or directory"` +
			`|unix://\S+ stopped serving`,
	)
)

// terminalWidth is the terminal width used for ptytest sessions.
const terminalWidth = 160

func ptySanitizeOutput(t *testing.T, rawOutput string) string {
	t.Helper()

	// Process raw output through a terminal emulator to resolve cursor
	// movement and screen clearing sequences. This produces deterministic
	// output regardless of bubbletea's internal render batching timing.
	s := ptytest.ProcessRawOutput(rawOutput)

	return ptySanitizeScreen(t, s)
}

// ptySanitizeScreen sanitizes a terminal screen (already processed through
// ProcessRawOutput) for golden file comparison.
func ptySanitizeScreen(t *testing.T, s string) string {
	t.Helper()

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

	// Normalise authd-unavailable errors: a dial failure and a health-check
	// "stopped serving" are both valid outcomes of authd stopping mid-run;
	// collapse them to one stable string so golden files don't flake.
	s = ptyAuthdUnavailableRegex.ReplaceAllLiteralString(s, "unix:///authd/test_socket.sock stopped serving")

	// Strip UID from permission error messages (makes output deterministic).
	s = permissions.Z_ForTests_IdempotentPermissionError(s)

	return s
}

// ptySanitizeSnapshotFrames takes terminal snapshots, sanitizes each one,
// deduplicates consecutive identical results, and formats them as cumulative
// frames separated by a horizontal rule.
func ptySanitizeSnapshotFrames(t *testing.T, snapshots []string, sanitizeScreen func(string) string) string {
	t.Helper()

	if len(snapshots) == 0 {
		return ""
	}

	var sanitized []string
	for _, snap := range snapshots {
		sanitized = append(sanitized, sanitizeScreen(snap))
	}

	// Deduplicate consecutive identical sanitized snapshots.
	deduped := []string{sanitized[0]}
	for i := 1; i < len(sanitized); i++ {
		if sanitized[i] != sanitized[i-1] {
			deduped = append(deduped, sanitized[i])
		}
	}

	var b strings.Builder
	for _, snap := range deduped {
		b.WriteString(snap)
		b.WriteString(separator)
	}

	return b.String()
}

// sendEchoedLine sends s to the console without Enter, waits for the echo
// to appear in the terminal output (capturing an intermediate snapshot that
// matches VHS behaviour), then sends Enter. Use for non-password, non-empty
// inputs where the terminal echoes the typed characters back (i.e. everything
// except actual password fields such as "Gimme your password:").
func sendEchoedLine(t *testing.T, c *ptytest.Console, s string) {
	t.Helper()
	require.NotEmpty(t, s, "sendEchoedLine requires a non-empty input")

	// The caller has just matched the label preceding the input prompt and
	// captured a snapshot, but the "> " prompt may not be rendered yet. If we
	// Send before it is, the PTY kernel echoes the typed characters before the
	// prompt (e.g. "2>" instead of "> 2"), racing the subsequent WaitFor.
	//
	// Discard that (possibly prompt-less) caller frame and recapture once "> "
	// is guaranteed present. The "> " frame is a cumulative-screen superset of
	// the discarded label frame, so this reproduces the golden frame
	// deterministically while ensuring our echo lands after the prompt.
	c.DiscardLastSnapshot()
	c.WaitFor(t, regexp.QuoteMeta("> "))
	c.Send(t, s)
	c.WaitFor(t, regexp.QuoteMeta(s))
	c.SendKey(t, ptytest.KeyEnter)
}

// waitForRunnerResult waits for a complete PAM runner result block, including
// the "Result:" line, to avoid matching an intermediate frame that only
// contains the action header.
func waitForRunnerResult(t *testing.T, c *ptytest.Console, action pam_test.RunnerResultAction) {
	t.Helper()
	c.WaitFor(t, `(?s)`+regexp.QuoteMeta(action.String())+`\r?\n(?:  User: .*\r?\n)?  Result: `)
}

// ptySanitizeSnapshots takes the snapshots captured by a Console (with
// WithSnapshots enabled), sanitizes them, and formats them as cumulative
// frames.
func ptySanitizeSnapshots(t *testing.T, c *ptytest.Console) string {
	t.Helper()

	snapshots := c.Snapshots()
	if len(snapshots) == 0 {
		return ptySanitizeOutput(t, c.RawOutput())
	}

	return ptySanitizeSnapshotFrames(t, snapshots, func(snap string) string {
		return ptySanitizeScreen(t, snap)
	})
}
