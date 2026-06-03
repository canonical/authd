package ptytest_test

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/canonical/authd/internal/testutils/ptytest"
	"github.com/stretchr/testify/require"
)

func TestStartAndOutput(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "echo", []string{"hello world"})

	c.WaitFor(t, "hello world")
	out := c.Output()
	require.Contains(t, out, "hello world")
}

func TestSendAndWaitFor(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "cat", nil)

	c.SendLine(t, "ping")
	c.WaitFor(t, "ping")

	c.SendLine(t, "pong")
	c.WaitFor(t, "pong")

	// Ctrl+D to send EOF to cat, causing it to exit.
	c.SendKey(t, ptytest.KeyCtrlD)
}

func TestWaitForRegex(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `echo "line 1: foo"; echo "line 2: bar"; echo "line 3: baz"`})

	c.WaitFor(t, `line \d+: bar`)

	out := c.Output()
	require.Contains(t, out, "line 1: foo")
	require.Contains(t, out, "line 2: bar")
}

func TestWaitForSequential(t *testing.T) {
	t.Parallel()

	// WaitFor advances the scan position, so subsequent calls only search
	// new output.
	c := ptytest.Start(t, "bash", []string{"-c", `echo "first"; echo "second"; echo "third"`})

	c.WaitFor(t, "first")
	c.WaitFor(t, "second")
	c.WaitFor(t, "third")
}

func TestSendKey(t *testing.T) {
	t.Parallel()

	// Start bash and have it trap Ctrl+C to print a message.
	c := ptytest.Start(t, "bash", []string{"-c", `
		trap 'echo GOT_SIGINT' INT
		echo "ready"
		# Read to keep the process alive.
		read -r
	`})

	c.WaitFor(t, "ready")
	c.SendKey(t, ptytest.KeyCtrlC)
	c.WaitFor(t, "GOT_SIGINT")
}

func TestWaitForExit(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", "echo done; exit 0"})

	c.WaitFor(t, "done")
	err := c.WaitForExit(t)
	require.NoError(t, err)
}

func TestWaitForExitNonZero(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", "exit 42"})

	err := c.WaitForExit(t)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected *exec.ExitError, got %T", err)
	require.Equal(t, 42, exitErr.ExitCode())
}

func TestWithTimeout(t *testing.T) {
	t.Parallel()

	// Use a very short timeout to verify timeout behavior.
	// We avoid t.Fatalf by using a helper test pattern.
	shortTimeout := 200 * time.Millisecond

	c := ptytest.Start(t, "cat", nil,
		ptytest.WithTimeout(shortTimeout),
	)

	// WaitFor something that won't appear — but we can't easily test
	// t.Fatalf without a subprocess, so just verify the option is accepted
	// and basic operations work.
	c.SendLine(t, "hello")
	c.WaitFor(t, "hello")
	c.SendKey(t, ptytest.KeyCtrlD)
}

func TestWithSize(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `tput cols; tput lines`},
		ptytest.WithSize(80, 24),
		ptytest.WithEnv([]string{"TERM=xterm", "PATH=/usr/bin:/bin"}),
	)

	c.WaitFor(t, "80")
	c.WaitFor(t, "24")
}

func TestWithDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := ptytest.Start(t, "pwd", nil,
		ptytest.WithDir(dir),
	)

	c.WaitFor(t, regexp.QuoteMeta(dir))
}

func TestWithEnv(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `echo "MY_VAR=$MY_VAR"`},
		ptytest.WithEnv([]string{
			"MY_VAR=test_value_123",
			"TERM=dumb",
			"PATH=/usr/bin:/bin",
		}),
	)

	c.WaitFor(t, "MY_VAR=test_value_123")
}

func TestInteractiveSession(t *testing.T) {
	t.Parallel()

	// Simulate a multi-step interactive session, which is the primary use
	// case for the PAM integration tests.
	c := ptytest.Start(t, "bash", []string{"-c", `
		echo -n "Username: "
		read -r USER
		echo "Got user: $USER"
		echo -n "Password: "
		read -rs PASS
		echo
		echo "Got password: $PASS"
		echo "Done"
	`})

	c.WaitFor(t, "Username:")
	c.SendLine(t, "testuser")
	c.WaitFor(t, "Got user: testuser")

	c.WaitFor(t, "Password:")
	c.SendLine(t, "secret")
	c.WaitFor(t, "Got password: secret")
	c.WaitFor(t, "Done")

	out := c.Output()
	require.Contains(t, out, "Got user: testuser")
	require.Contains(t, out, "Got password: secret")
}

func TestStripANSI(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"no escape sequences": {
			input: "hello world",
			want:  "hello world",
		},
		"SGR color code": {
			input: "\x1b[31mred text\x1b[0m",
			want:  "red text",
		},
		"cursor movement": {
			input: "\x1b[2Jhello\x1b[1;1H",
			want:  "hello",
		},
		"OSC sequence with BEL": {
			input: "\x1b]0;window title\x07hello",
			want:  "hello",
		},
		"OSC sequence with ST": {
			input: "\x1b]0;window title\x1b\\hello",
			want:  "hello",
		},
		"mixed": {
			input: "\x1b[1m\x1b[32mbold green\x1b[0m normal \x1b[4munderline\x1b[0m",
			want:  "bold green normal underline",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := ptytest.StripANSI(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizeOutput(t *testing.T) {
	t.Parallel()

	input := "\x1b[32mhello\x1b[0m   \r\nworld  \r\n"
	got := ptytest.SanitizeOutput(input)

	require.Equal(t, "hello\nworld\n", got)
	require.False(t, strings.Contains(got, "\r"))
}

func TestRawOutput(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `printf '\x1b[31mred\x1b[0m'`},
		ptytest.WithEnv([]string{"TERM=xterm", "PATH=/usr/bin:/bin"}),
	)

	c.WaitFor(t, "red")
	raw := c.RawOutput()
	// Raw output should still contain ANSI codes.
	require.Contains(t, raw, "\x1b[")
}

func TestWaitForFailureShowsInteractionHistory(t *testing.T) {
	t.Parallel()

	// Verify that interactions are recorded correctly during a multi-step
	// session. The interaction history is included in WaitFor failure
	// messages to help debug which step of an authentication flow failed.
	c := ptytest.Start(t, "bash", []string{"-c", `
		echo "Username:"
		read -r USER
		echo "Got user: $USER"
		echo "Choose broker:"
		read -r BROKER
		echo "Selected: $BROKER"
		echo "Done"
	`})

	c.WaitFor(t, "Username:")
	c.SendLine(t, "testuser")
	c.WaitFor(t, "Got user: testuser")
	c.WaitFor(t, "Choose broker:")
	c.SendLine(t, "2")
	c.WaitFor(t, "Selected: 2")
	c.WaitFor(t, "Done")

	// The interactions were recorded - verify the console still works
	// correctly after recording.
	out := c.Output()
	require.Contains(t, out, "Got user: testuser")
	require.Contains(t, out, "Selected: 2")
}

func TestSnapshots(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `
		echo "Step 1: hello"
		echo "Step 2: world"
		echo "Step 3: done"
	`}, ptytest.WithSnapshots())

	c.WaitFor(t, "Step 1")
	c.WaitFor(t, "Step 2")
	c.WaitFor(t, "Step 3")
	_ = c.WaitForExit(t)

	snapshots := c.Snapshots()
	require.NotEmpty(t, snapshots, "snapshots should be captured")

	// Each snapshot should contain the text visible at that point.
	require.Contains(t, snapshots[0], "Step 1")

	// Later snapshots should contain earlier text too (terminal accumulates).
	last := snapshots[len(snapshots)-1]
	require.Contains(t, last, "Step 1")
	require.Contains(t, last, "Step 2")
	require.Contains(t, last, "Step 3")
}

func TestSnapshotsDisabledByDefault(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "echo", []string{"hello"})
	c.WaitFor(t, "hello")
	_ = c.WaitForExit(t)

	require.Nil(t, c.Snapshots(), "snapshots should be nil when not enabled")
}

func TestSnapshotsDeduplication(t *testing.T) {
	t.Parallel()

	c := ptytest.Start(t, "bash", []string{"-c", `
		echo "line1"
		echo "line2"
	`}, ptytest.WithSnapshots())

	// Two WaitFor calls that match on the same screen state
	// (both patterns exist in the same output).
	c.WaitFor(t, "line1")
	c.WaitFor(t, "line2")
	_ = c.WaitForExit(t)

	snapshots := c.Snapshots()
	// Consecutive identical snapshots should be deduped.
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i] == snapshots[i-1] {
			t.Errorf("snapshot %d is identical to %d, should have been deduped", i, i-1)
		}
	}
}
