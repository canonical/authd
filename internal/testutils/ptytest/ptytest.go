// Package ptytest provides a PTY-based test harness for interactive terminal
// testing. It spawns commands in a pseudo-terminal and provides event-driven
// Send/WaitFor primitives.
package ptytest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Predefined key constants for SendKey.
const (
	KeyEnter     = '\r'
	KeyEscape    = '\x1b'
	KeyBackspace = '\x7f'
	KeyCtrlC     = '\x03'
	KeyCtrlD     = '\x04'
)

// ansiRegex matches ANSI escape sequences (CSI, OSC, and simple escapes).
var ansiRegex = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-9;]*m|[\(\)][AB012]|[=><=]|[ #][a-zA-Z]|.)`)

// ANSI color helpers for test diagnostic output.
func colorDim(s string) string       { return "\033[1;36m" + s + "\033[0m" }
func colorBoldRed(s string) string   { return "\033[1;31m" + s + "\033[0m" }
func colorBoldGreen(s string) string { return "\033[0;32m" + s + "\033[0m" }

func sectionHeader(title string) string {
	return colorDim(fmt.Sprintf("=== %s ===", title))
}

func sectionFooter(title string) string {
	length := len(title) + 8 // length of "===  ==="
	return colorDim(strings.Repeat("=", length))
}

func waitStatusDetails(ws syscall.WaitStatus) string {
	if ws.Exited() {
		return fmt.Sprintf("exited (code=%d)", ws.ExitStatus())
	}
	if ws.Signaled() {
		s := fmt.Sprintf("terminated by signal %s", ws.Signal())
		if ws.CoreDump() {
			s += " (core dumped)"
		}
		return s
	}
	if ws.Stopped() {
		return fmt.Sprintf("stopped by signal %s", ws.StopSignal())
	}
	return fmt.Sprintf("wait status=%#x", int(ws))
}

// options holds configuration for a Console.
type options struct {
	env       []string
	dir       string
	cols      uint16
	rows      uint16
	timeout   time.Duration
	snapshots bool
}

// defaultOptions returns the default configuration.
func defaultOptions() options {
	return options{
		cols:    160,
		rows:    50,
		timeout: testutils.MultipliedSleepDuration(10 * time.Second),
	}
}

// RequireSuccessfulExit waits for command exit and requires exit code 0.
func (c *Console) RequireSuccessfulExit(t *testing.T) {
	t.Helper()

	c.RequireExitCode(t, 0)
}

// RequireExitCode waits for command exit and requires the expected non-zero exit code.
func (c *Console) RequireExitCode(t *testing.T, expectedExitCode int) {
	t.Helper()

	var exitCode int
	err := c.WaitForExit(t)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		c.logProcessDiagnostics(t)
		content, rawSinceLastMatch := c.diagnosticContent()
		require.FailNow(t, fmt.Sprintf("ptytest: WaitForExit failed: %v", err),
			c.formatDiagnostics(content, rawSinceLastMatch))
	}

	if exitCode == expectedExitCode {
		return
	}

	content, rawSinceLastMatch := c.diagnosticContent()
	require.FailNow(t,
		colorBoldRed(fmt.Sprintf("ptytest: expected command to exit with code %d, got %d", expectedExitCode, exitCode)),
		c.formatDiagnostics(content, rawSinceLastMatch))
}

// Option configures a PTY test session.
type Option func(*options)

// WithTimeout sets the default timeout for WaitFor operations.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		o.timeout = testutils.MultipliedSleepDuration(d)
	}
}

// WithEnv sets environment variables for the spawned command.
func WithEnv(env []string) Option {
	return func(o *options) {
		o.env = env
	}
}

// WithSize sets the terminal size (columns, rows).
func WithSize(cols, rows uint16) Option {
	return func(o *options) {
		o.cols = cols
		o.rows = rows
	}
}

// WithDir sets the working directory for the spawned command.
func WithDir(dir string) Option {
	return func(o *options) {
		o.dir = dir
	}
}

// WithSnapshots enables automatic terminal screen snapshot capture after each
// successful WaitFor call. Use Snapshots() to retrieve the captured states.
// This is useful for TUI applications that redraw in-place (e.g. bubbletea),
// where the final terminal state alone loses intermediate interaction steps.
func WithSnapshots() Option {
	return func(o *options) {
		o.snapshots = true
	}
}

// interactionResult describes the outcome of an interaction step.
type interactionResult string

const (
	resultOK          interactionResult = "ok"
	resultTimedOut    interactionResult = "timed out"
	resultExitNoMatch interactionResult = "command exited without matching"
)

// interaction records a single Send/WaitFor step for diagnostic purposes.
type interaction struct {
	op     string            // "WaitFor", "Send", "SendLine", "SendKey"
	detail string            // pattern for WaitFor, text for Send/SendLine, key name for SendKey
	result interactionResult // outcome of the operation
}

// Console represents a running command in a PTY.
type Console struct {
	cmd          *exec.Cmd
	ptmx         *os.File
	opts         options
	mu           sync.RWMutex
	buf          bytes.Buffer
	scanPos      int // position from which the next WaitFor starts scanning
	done         chan error
	copyDone     chan struct{}
	closed       bool
	interactions []interaction
	snapshots    []string // terminal screen snapshots captured at WaitFor points
	startedAt    time.Time
	exitedAt     time.Time

	// spawnSelfSIGINT records the test process's own SIGINT disposition at the
	// moment this command was spawned. A spawned process inherits SIG_IGN for
	// SIGINT across exec (and the Go runtime preserves an inherited ignored
	// SIGINT), so if the test process had SIGINT ignored when it forked the
	// command, the command will silently ignore Ctrl+C. This is logged in the
	// failure diagnostics to help distinguish inherited-ignore from an
	// in-process override.
	spawnSelfSIGINT string
}

// winsize is the struct for the TIOCSWINSZ ioctl.
type winsize struct {
	Rows uint16
	Cols uint16
	X    uint16 // unused
	Y    uint16 // unused
}

// openPTY opens a new PTY pair using /dev/ptmx (standard Linux approach).
func openPTY() (ptmx, pts *os.File, err error) {
	ptmx, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	defer func() {
		if err != nil {
			ptmx.Close()
		}
	}()

	// grantpt
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCGPTN, 0); errno != 0 {
		// grantpt is a no-op on modern Linux with devpts, but we still try.
		_ = errno
	}

	// unlockpt
	var unlock int32
	//nolint:gosec // G103: unsafe.Pointer is required for ioctl syscalls — this is standard POSIX PTY setup.
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		return nil, nil, fmt.Errorf("unlockpt: %w", errno)
	}

	// ptsname
	var ptsNum uint32
	//nolint:gosec // G103: unsafe.Pointer is required for ioctl syscalls — this is standard POSIX PTY setup.
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptsNum))); errno != 0 {
		return nil, nil, fmt.Errorf("ptsname: %w", errno)
	}
	ptsName := fmt.Sprintf("/dev/pts/%d", ptsNum)

	pts, err = os.OpenFile(ptsName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open pts %s: %w", ptsName, err)
	}

	return ptmx, pts, nil
}

// setWinsize sets the terminal size on the given fd.
func setWinsize(fd uintptr, ws *winsize) error {
	//nolint:gosec // G103: Using unsafe.Pointer for ioctl is required and standard practice.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
	if errno != 0 {
		return errno
	}
	return nil
}

// Start spawns the command in a PTY and returns a Console.
// The command is automatically terminated and the PTY cleaned up when the test ends.
func Start(t *testing.T, name string, args []string, opts ...Option) *Console {
	t.Helper()

	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	ptmx, pts, err := openPTY()
	require.NoError(t, err, "ptytest: failed to open PTY")

	ws := &winsize{Rows: o.rows, Cols: o.cols}
	if err := setWinsize(ptmx.Fd(), ws); err != nil {
		pts.Close()
		ptmx.Close()
		require.FailNow(t, err.Error(), "ptytest: failed to set winsize")
	}

	//nolint:gosec // G204: The command is provided by the caller (test code).
	cmd := exec.Command(name, args...)
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0, // stdin fd in the child
	}
	if o.env != nil {
		cmd.Env = o.env
	}
	if o.dir != "" {
		cmd.Dir = o.dir
	}

	if err := cmd.Start(); err != nil {
		pts.Close()
		ptmx.Close()
		require.FailNow(t, err.Error(), "ptytest: failed to start command %q", name)
	}

	// Close the slave side in the parent — the child owns it.
	pts.Close()

	c := &Console{
		cmd:             cmd,
		ptmx:            ptmx,
		opts:            o,
		done:            make(chan error, 1),
		copyDone:        make(chan struct{}),
		spawnSelfSIGINT: selfSIGINTDisposition(),
		startedAt:       time.Now(),
	}

	// Background goroutine: read PTY output.
	go func() {
		defer close(c.copyDone)
		buf := make([]byte, 4096)
		var queryCarry []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(buf[:n])
				c.mu.Unlock()
				// Answer terminal capability queries the way a real terminal
				// emulator would. A raw PTY has nobody to reply, so libraries
				// like termenv (used by bubbletea via lipgloss) block on their
				// OSCTimeout (5s) at startup, once per spawned UI process.
				queryCarry = c.answerTerminalQueries(append(queryCarry, buf[:n]...))
			}
			if err != nil {
				break
			}
		}
	}()

	// Background goroutine: wait for command exit.
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.exitedAt = time.Now()
		c.mu.Unlock()
		c.done <- err
	}()

	t.Cleanup(func() { c.Close(t) })

	t.Logf("ptytest: started %q %v (pid %d, pty %s, %dx%d)",
		name, args, cmd.Process.Pid, ptmx.Name(), o.cols, o.rows)

	return c
}

// dsrCursorQuery is the Device Status Report sequence (ESC [ 6 n) a program
// sends to ask the terminal for the cursor position.
var dsrCursorQuery = []byte("\x1b[6n")

// answerTerminalQueries replies to terminal capability queries emitted by the
// program, mimicking a real terminal emulator. scan is the freshly read output
// (prefixed with any carry-over from the previous read so a query split across
// reads is still detected); it returns the trailing bytes to carry into the
// next call.
//
// We answer the cursor-position query (DSR). termenv — used by bubbletea via
// lipgloss to detect the terminal background — first writes an OSC background
// query and then a DSR, and reads the first response back. By answering the
// DSR (which every real terminal answers) it reads a non-OSC reply, concludes
// the terminal does not report its background and falls back to its default
// (dark), instead of blocking for the full 5s OSCTimeout. The reported
// position is unused by both termenv (which discards it) and bubbletea (which
// never reads cursor reports), so a fixed 1;1 is fine. This keeps the detected
// background — and therefore every rendered byte — identical to the timed-out
// fallback, only without the per-process 5s stall.
func (c *Console) answerTerminalQueries(scan []byte) (carry []byte) {
	idx := 0
	for {
		i := bytes.Index(scan[idx:], dsrCursorQuery)
		if i < 0 {
			break
		}
		idx += i + len(dsrCursorQuery)
		// The cursor position report: ESC [ row ; col R.
		if _, err := c.ptmx.Write([]byte("\x1b[1;1R")); err != nil {
			break
		}
	}
	// Retain the last few unmatched bytes in case a query straddles the
	// boundary with the next read.
	tail := scan[idx:]
	if maxLen := len(dsrCursorQuery) - 1; len(tail) > maxLen {
		tail = tail[len(tail)-maxLen:]
	}
	return append([]byte(nil), tail...)
}

// record appends an interaction step to the history.
func (c *Console) record(op, detail string, result interactionResult) {
	c.interactions = append(c.interactions, interaction{op: op, detail: detail, result: result})
}

// formatHistory returns a formatted summary of all recorded interaction steps.
func (c *Console) formatHistory() string {
	if len(c.interactions) == 0 {
		return "  (no interactions recorded)"
	}

	var b strings.Builder
	for i, ix := range c.interactions {
		line := fmt.Sprintf("  %d. %s(%q)", i+1, ix.op, ix.detail)
		switch ix.result {
		case resultOK:
			if ix.op == "WaitFor" {
				line += colorBoldGreen(" → matched")
			}
		case resultTimedOut:
			line = colorBoldRed(line + " → TIMED OUT")
		case resultExitNoMatch:
			line = colorBoldRed(line + " → FAILED (command exited without matching)")
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// formatDiagnostics builds a colored diagnostic message for WaitFor and
// WaitForExit failures, including the command, interaction history, current
// terminal screen, and (if non-empty) text seen since the last match.
func (c *Console) formatDiagnostics(content, rawContent string) string {
	var b strings.Builder

	if c.cmd != nil {
		fmt.Fprintf(&b, "%s\n%s", sectionHeader("command"), c.cmd.String())
		if c.cmd.Dir != "" {
			fmt.Fprintf(&b, "\n(dir: %s)", c.cmd.Dir)
		}
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "%s\n%s\n", sectionHeader("interaction history"), c.formatHistory())
	fmt.Fprintf(&b, "%s\n%s\n\n", sectionHeader("process exit"), c.processExitSummary())
	fmt.Fprintf(&b, "%s\n%s\n", sectionHeader("current terminal screen"), ProcessRawOutput(content))

	title := "text seen since last match"
	fmt.Fprintf(&b, "\n%s\n%s\n%s",
		sectionHeader(title),
		ProcessRawOutput(rawContent),
		sectionFooter(title))

	return b.String()
}

// processExitSummary returns best-effort process termination details for diagnostics.
func (c *Console) processExitSummary() string {
	runtime := c.processRuntime()

	// Prefer the direct wait result if it is already available.
	select {
	case err := <-c.done:
		// Put it back so WaitForExit/Close can still consume it.
		c.done <- err

		if err == nil {
			return fmt.Sprintf("exited (code=0), runtime=%s", runtime)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				return fmt.Sprintf("%s, runtime=%s", waitStatusDetails(ws), runtime)
			}
			return fmt.Sprintf("exited with non-zero status (code=%d), runtime=%s", exitErr.ExitCode(), runtime)
		}
		return fmt.Sprintf("wait error: %v, runtime=%s", err, runtime)
	default:
	}

	// If the wait result is not ready yet, fall back to ProcessState if present.
	if c.cmd != nil && c.cmd.ProcessState != nil {
		if ws, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			return fmt.Sprintf("%s, runtime=%s", waitStatusDetails(ws), runtime)
		}
		return fmt.Sprintf("process state available (code=%d), runtime=%s", c.cmd.ProcessState.ExitCode(), runtime)
	}

	return fmt.Sprintf("still running (no exit observed), runtime=%s", runtime)
}

func (c *Console) processRuntime() time.Duration {
	c.mu.RLock()
	started := c.startedAt
	exited := c.exitedAt
	c.mu.RUnlock()

	if started.IsZero() {
		return 0
	}
	if exited.IsZero() {
		return time.Since(started).Round(time.Millisecond)
	}
	return exited.Sub(started).Round(time.Millisecond)
}

// diagnosticContent returns the full raw terminal output and the portion seen
// since the last successful WaitFor match.
func (c *Console) diagnosticContent() (content string, rawSinceLastMatch string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	content = c.buf.String()
	if c.scanPos >= len(content) {
		return content, ""
	}
	return content, content[c.scanPos:]
}

// Send writes raw text to the PTY (as if typed by the user).
func (c *Console) Send(t *testing.T, s string) {
	t.Helper()

	c.record("Send", s, resultOK)
	t.Logf("ptytest: Send(%q)", s)
	_, err := io.WriteString(c.ptmx, s)
	require.NoError(t, err, "ptytest: Send failed")
}

// SendLine writes s followed by Enter.
func (c *Console) SendLine(t *testing.T, s string) {
	t.Helper()

	c.record("SendLine", s, resultOK)
	t.Logf("ptytest: SendLine(%q)", s)
	_, err := io.WriteString(c.ptmx, s+"\r")
	require.NoError(t, err, "ptytest: SendLine failed")
}

// SendKey sends a single control byte to the PTY.
func (c *Console) SendKey(t *testing.T, key byte) {
	t.Helper()

	keyName := fmt.Sprintf("0x%02x", key)
	switch key {
	case KeyEnter:
		keyName = "Enter"
	case KeyEscape:
		keyName = "Escape"
	case KeyBackspace:
		keyName = "Backspace"
	case KeyCtrlC:
		keyName = "Ctrl+C"
	case KeyCtrlD:
		keyName = "Ctrl+D"
	}
	t.Logf("ptytest: SendKey(%s)", keyName)
	c.record("SendKey", keyName, resultOK)

	_, err := c.ptmx.Write([]byte{key})
	require.NoError(t, err, "ptytest: SendKey failed")
}

// WaitFor blocks until the accumulated terminal output (from the current scan
// position) matches the given regexp pattern, or the default timeout expires.
// On timeout, the test is failed with diagnostic output.
// Returns the matched output.
func (c *Console) WaitFor(t *testing.T, pattern string) string {
	t.Helper()

	return c.WaitForTimeout(t, pattern, c.opts.timeout)
}

// WaitForTimeout is like WaitFor but with an explicit timeout.
func (c *Console) WaitForTimeout(t *testing.T, pattern string, timeout time.Duration) string {
	t.Helper()

	re, err := regexp.Compile(pattern)
	require.NoError(t, err, "ptytest: invalid WaitFor pattern %q", pattern)

	deadline := time.Now().Add(timeout)
	const pollInterval = 50 * time.Millisecond

	t.Logf("ptytest: WaitFor(%q, timeout=%s)", pattern, timeout)

	for {
		c.mu.RLock()
		content := c.buf.String()
		c.mu.RUnlock()

		// Only search from the current scan position.
		rawContent := content[c.scanPos:]
		searchContent := stripANSI(rawContent)
		loc := re.FindStringIndex(searchContent)
		if loc != nil {
			// Advance scan position past the match in the raw buffer.
			// We need to find the raw offset that corresponds to loc[1]
			// in the stripped content, since ANSI sequences make the raw
			// content longer than the stripped content.
			c.scanPos += rawOffsetForStripped(rawContent, loc[1])
			c.record("WaitFor", pattern, resultOK)
			c.captureSnapshot(content)
			t.Logf("ptytest: WaitFor(%q) matched", pattern)
			return searchContent[:loc[1]]
		}

		if time.Now().After(deadline) {
			c.record("WaitFor", pattern, resultTimedOut)
			require.FailNow(t,
				colorBoldRed(fmt.Sprintf("ptytest: WaitFor(%q) timed out after %s", pattern, timeout)),
				c.formatDiagnostics(content, rawContent))
		}

		// Check if the command has exited.
		select {
		case exitErr := <-c.done:
			// Put it back so WaitForExit can also read it.
			c.done <- exitErr

			// Wait for the copy goroutine to finish reading all output.
			<-c.copyDone

			c.mu.RLock()
			content = c.buf.String()
			c.mu.RUnlock()
			rawContent = content[c.scanPos:]
			searchContent = stripANSI(rawContent)
			loc = re.FindStringIndex(searchContent)
			if loc != nil {
				c.scanPos += rawOffsetForStripped(rawContent, loc[1])
				c.record("WaitFor", pattern, resultOK)
				c.captureSnapshot(content)
				t.Logf("ptytest: WaitFor(%q) matched (after exit)", pattern)
				return searchContent[:loc[1]]
			}

			c.record("WaitFor", pattern, resultExitNoMatch)
			require.FailNow(t,
				colorBoldRed(fmt.Sprintf("ptytest: WaitFor(%q) failed: command exited before matching", pattern)),
				c.formatDiagnostics(content, rawContent))
		default:
		}

		time.Sleep(pollInterval)
	}
}

// WaitForExit blocks until the command exits and all PTY output has been
// drained. Returns the exit error (nil on success).
func (c *Console) WaitForExit(t *testing.T) error {
	t.Helper()

	t.Logf("ptytest: WaitForExit()")

	select {
	case err := <-c.done:
		c.done <- err // put it back for Close
		// Wait for the copy goroutine to drain any remaining PTY output so
		// that RawOutput() returns the complete output after this call.
		<-c.copyDone

		// Capture a final snapshot now that all output has been drained.
		c.mu.RLock()
		content := c.buf.String()
		c.mu.RUnlock()
		c.captureSnapshot(content)

		return err
	case <-time.After(c.opts.timeout):
		c.logProcessDiagnostics(t)
		c.mu.RLock()
		content := c.buf.String()
		c.mu.RUnlock()
		require.FailNow(t,
			fmt.Sprintf("ptytest: WaitForExit timed out after %s", c.opts.timeout),
			c.formatDiagnostics(content, ""))
		return nil // unreachable
	}
}

// Output returns all terminal output captured so far, with ANSI escape
// sequences stripped.
func (c *Console) Output() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return stripANSI(c.buf.String())
}

// RawOutput returns all terminal output captured so far, including ANSI
// escape sequences.
func (c *Console) RawOutput() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.buf.String()
}

// captureSnapshot processes the current raw buffer through the terminal
// emulator and stores the result if snapshots are enabled.
func (c *Console) captureSnapshot(rawContent string) {
	if !c.opts.snapshots {
		return
	}
	screen := ProcessRawOutput(rawContent)
	c.snapshots = append(c.snapshots, screen)
}

// Snapshots returns the terminal screen states captured at each WaitFor match
// point and after WaitForExit. Consecutive duplicate snapshots are removed.
// Only populated when WithSnapshots() option was used.
func (c *Console) Snapshots() []string {
	if len(c.snapshots) == 0 {
		return nil
	}
	// Deduplicate consecutive identical snapshots.
	deduped := []string{c.snapshots[0]}
	for i := 1; i < len(c.snapshots); i++ {
		if c.snapshots[i] != c.snapshots[i-1] {
			deduped = append(deduped, c.snapshots[i])
		}
	}
	return deduped
}

// ResetSnapshots discards all snapshots captured so far.
// Useful to ignore preliminary interaction steps (e.g. waiting for a prompt
// before the meaningful flow begins) when WithSnapshots is enabled.
func (c *Console) ResetSnapshots() {
	c.snapshots = nil
}

// DiscardLastSnapshot removes the most recently captured snapshot.
// Useful when a WaitFor call captures a timing-sensitive intermediate state
// that should not appear in the golden file.
func (c *Console) DiscardLastSnapshot() {
	if len(c.snapshots) > 0 {
		c.snapshots = c.snapshots[:len(c.snapshots)-1]
	}
}

// RewriteLastSnapshot rewrites the most recently captured snapshot in place.
// If no snapshots were captured yet, it does nothing.
func (c *Console) RewriteLastSnapshot(rewrite func(string) string) {
	if len(c.snapshots) == 0 {
		return
	}
	c.snapshots[len(c.snapshots)-1] = rewrite(c.snapshots[len(c.snapshots)-1])
}

// Close terminates the command (if still running) and cleans up the PTY.
// It is safe to call multiple times. It is also called automatically on
// test cleanup.
func (c *Console) Close(t *testing.T) {
	t.Helper()
	t.Logf("ptytest: closing console for pid %d", c.cmd.Process.Pid)

	if c.closed {
		return
	}
	c.closed = true

	// Try graceful shutdown first: close the PTY (sends EOF/SIGHUP to child).
	c.ptmx.Close()

	select {
	case <-c.done:
		// Process already exited.
		t.Logf("ptytest: process %d exited gracefully after PTY close", c.cmd.Process.Pid)
	case <-time.After(5 * time.Second):
		// Force kill.
		t.Logf("ptytest: killing process %d (did not exit after PTY close)", c.cmd.Process.Pid)
		c.logProcessDiagnostics(t)
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

// LogProcessTree logs the process tree rooted at rootPID to the test log.
// label is a descriptive name used to identify the tree in the log.
// This is a standalone diagnostic helper, not tied to any Console, useful for
// diagnosing external processes (e.g. sshd) that may be hanging when a test fails.
func LogProcessTree(t *testing.T, label string, rootPID int) {
	t.Helper()

	var b strings.Builder
	fmt.Fprintf(&b, "process diagnostics for %s (pid %d):\n", label, rootPID)
	tree := processTree(rootPID)
	for _, pid := range tree {
		b.WriteString(procSummary(pid))
	}
	// If anything in the tree is stuck in uninterruptible sleep, the cause is
	// usually an I/O stall rather than the process itself; capture system-wide
	// I/O state to confirm. Gated so healthy teardowns stay quiet.
	if treeHasDState(tree) {
		b.WriteString(systemIODiagnostics())
	}
	t.Logf("%s", strings.TrimRight(b.String(), "\n"))
}

// treeHasDState reports whether any thread of any pid in the tree is in
// uninterruptible sleep (state "D").
func treeHasDState(tree []int) bool {
	for _, pid := range tree {
		taskDir := fmt.Sprintf("/proc/%d/task", pid)
		tids, err := os.ReadDir(taskDir)
		if err != nil {
			continue
		}
		for _, te := range tids {
			ts := readStatusFields(fmt.Sprintf("%s/%s/status", taskDir, te.Name()))
			if strings.HasPrefix(ts["State"], "D") {
				return true
			}
		}
	}
	return false
}

// systemIODiagnostics returns a system-wide snapshot useful for diagnosing an
// I/O stall: every uninterruptible (D-state) task on the host, disk usage, and
// any kernel hung-task warnings. It is best-effort and never fails.
func systemIODiagnostics() string {
	var b strings.Builder
	b.WriteString("  system I/O diagnostics (a thread in the tree is in uninterruptible sleep):\n")

	b.WriteString("    uninterruptible (D-state) tasks system-wide:\n")
	tasks := dStateTasks()
	if len(tasks) == 0 {
		b.WriteString("      none\n")
	}
	for _, line := range tasks {
		fmt.Fprintf(&b, "      %s\n", line)
	}

	if out, err := exec.Command("df", "-h").CombinedOutput(); err == nil {
		b.WriteString("    df -h:\n")
		for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
			fmt.Fprintf(&b, "      %s\n", line)
		}
	} else {
		fmt.Fprintf(&b, "    df -h: unavailable: %v\n", err)
	}

	// Kernel hung-task warnings ("task X blocked for more than N seconds") name
	// the stuck task and its filesystem. dmesg may be restricted; that is fine.
	if out, err := exec.Command("dmesg").CombinedOutput(); err == nil {
		var hung []string
		for line := range strings.SplitSeq(string(out), "\n") {
			if strings.Contains(line, "blocked for more than") || strings.Contains(line, "hung_task") {
				hung = append(hung, line)
			}
		}
		if len(hung) > 0 {
			b.WriteString("    kernel hung-task warnings (dmesg):\n")
			for _, line := range hung {
				fmt.Fprintf(&b, "      %s\n", line)
			}
		}
	} else {
		fmt.Fprintf(&b, "    dmesg: unavailable: %v\n", err)
	}

	return b.String()
}

// dStateTasks scans /proc for every thread in uninterruptible sleep (state "D")
// and returns a one-line summary (pid, tid, comm, state, wchan) for each.
func dStateTasks() []string {
	var out []string
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		taskDir := fmt.Sprintf("/proc/%d/task", pid)
		tasks, err := os.ReadDir(taskDir)
		if err != nil {
			continue
		}
		for _, te := range tasks {
			tid := te.Name()
			ts := readStatusFields(fmt.Sprintf("%s/%s/status", taskDir, tid))
			if !strings.HasPrefix(ts["State"], "D") {
				continue
			}
			comm := readProcFileTrim(fmt.Sprintf("%s/%s/comm", taskDir, tid))
			wchan := readProcFileTrim(fmt.Sprintf("%s/%s/wchan", taskDir, tid))
			out = append(out, fmt.Sprintf("pid %d tid %s (%s) state=%q wchan=%q",
				pid, tid, comm, ts["State"], wchan))
		}
	}
	return out
}

// logProcessDiagnostics logs the state of the spawned process tree and the PTY.
// It is meant to help debug cases where a process unexpectedly fails to exit
// (e.g. a missed or swallowed signal): the symptom we have seen is the native
// "sigints" tests timing out because Ctrl+C did not terminate the process.
//
// It reports, best-effort, the PTY foreground process group and line discipline,
// and for every process in the spawned tree (and each of its threads) the state,
// the kernel function it is blocked in (wchan), and the SIGINT disposition
// (blocked/ignored/caught/pending). A SIGINT that is pending but blocked, or a
// thread stuck in a blocking read, is the kind of signal we are looking for.
//
// It is Linux-specific and never fails the test: any field it cannot read is
// skipped. All output goes through t.Logf so it shows up in CI logs.
func (c *Console) logProcessDiagnostics(t *testing.T) {
	t.Helper()

	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	rootPID := c.cmd.Process.Pid

	var b strings.Builder
	fmt.Fprintf(&b, "ptytest: process diagnostics for command tree rooted at pid %d:\n", rootPID)
	fmt.Fprintf(&b, "  test process (pid %d) SIGINT disposition when this command was spawned: %s\n",
		os.Getpid(), c.spawnSelfSIGINT)

	// PTY foreground process group and line discipline.
	//
	// Avoid calling Fd() after Close() has started: with the race detector this
	// can race with the PTY reader goroutine tearing down the file descriptor.
	// In that case, skip PTY diagnostics and still report process-tree details.
	if !c.closed {
		fd := int(c.ptmx.Fd())
		if fgpgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err == nil {
			fmt.Fprintf(&b, "  pty: foreground process group (TIOCGPGRP) = %d\n", fgpgrp)
		} else {
			fmt.Fprintf(&b, "  pty: TIOCGPGRP unavailable: %v\n", err)
		}
		if tio, err := unix.IoctlGetTermios(fd, unix.TCGETS); err == nil {
			fmt.Fprintf(&b, "  pty: Lflag=%#x (ECHO=%v ICANON=%v ISIG=%v) VINTR=%#x\n",
				tio.Lflag, tio.Lflag&unix.ECHO != 0, tio.Lflag&unix.ICANON != 0,
				tio.Lflag&unix.ISIG != 0, tio.Cc[unix.VINTR])
		}
	}

	for _, pid := range processTree(rootPID) {
		b.WriteString(procSummary(pid))
	}

	t.Logf("%s", strings.TrimRight(b.String(), "\n"))
}

// processTree returns rootPID followed by all of its descendant PIDs, found by
// scanning /proc. Returns just rootPID if /proc cannot be read.
func processTree(rootPID int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []int{rootPID}
	}

	ppidOf := make(map[int]int)
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ppid, ok := readPPID(pid)
		if !ok {
			continue
		}
		ppidOf[pid] = ppid
		pids = append(pids, pid)
	}

	inTree := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for _, pid := range pids {
			if inTree[pid] || !inTree[ppidOf[pid]] {
				continue
			}
			inTree[pid] = true
			changed = true
		}
	}

	result := []int{rootPID}
	for _, pid := range pids {
		if pid != rootPID && inTree[pid] {
			result = append(result, pid)
		}
	}
	sort.Ints(result)
	return result
}

// readPPID returns the parent PID of pid from /proc/<pid>/stat. The comm field
// (field 2) may contain spaces and parentheses, so the remaining fields are
// parsed from after the final ')'.
func readPPID(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, false
	}
	// Fields after "<pid> (comm) ": state ppid pgrp session tty_nr tpgid ...
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// procSummary returns a human-readable, multi-line summary of a process and its
// threads, focused on what is needed to debug a missed/swallowed SIGINT.
func procSummary(pid int) string {
	var b strings.Builder

	status := readStatusFields(fmt.Sprintf("/proc/%d/status", pid))
	fmt.Fprintf(&b, "  pid %d (%s):\n", pid, readProcFileTrim(fmt.Sprintf("/proc/%d/comm", pid)))
	// SigIgn/SigCgt and the process-wide pending set (ShdPnd) are shared by all
	// threads, so report them once per process.
	fmt.Fprintf(&b, "    SIGINT: ignored=%v caught=%v process-pending=%v\n",
		sigSetHasSIGINT(status["SigIgn"]),
		sigSetHasSIGINT(status["SigCgt"]),
		sigSetHasSIGINT(status["ShdPnd"]))

	taskDir := fmt.Sprintf("/proc/%d/task", pid)
	tids, err := os.ReadDir(taskDir)
	if err != nil {
		return b.String()
	}
	blocked := false
	for _, te := range tids {
		tid := te.Name()
		ts := readStatusFields(fmt.Sprintf("%s/%s/status", taskDir, tid))
		comm := readProcFileTrim(fmt.Sprintf("%s/%s/comm", taskDir, tid))
		wchan := readProcFileTrim(fmt.Sprintf("%s/%s/wchan", taskDir, tid))
		// SigBlk and the per-thread pending set (SigPnd) are per-thread.
		fmt.Fprintf(&b, "    thread %s (%s): state=%q wchan=%q SIGINT[blocked=%v thread-pending=%v]\n",
			tid, comm, ts["State"], wchan,
			sigSetHasSIGINT(ts["SigBlk"]),
			sigSetHasSIGINT(ts["SigPnd"]))
		// For threads stuck in uninterruptible sleep, gather what we can about
		// the blocking call path and the file/fd involved.
		if strings.Contains(ts["State"], "D") {
			blocked = true
			// /proc/<tid>/syscall shows the in-progress syscall number and its
			// args (including fds/addresses). It is usually readable even when
			// /stack is not, so it is our best chance to identify the operation.
			if sc := readProcFileTrim(fmt.Sprintf("%s/%s/syscall", taskDir, tid)); sc != "" {
				fmt.Fprintf(&b, "      syscall: %s\n", sc)
			}
			// /proc/<tid>/stack requires CAP_SYS_ADMIN and is blocked under
			// kernel lockdown (common on Ubuntu kernels). Surface the read error
			// rather than silently omitting it, so an EPERM is not mistaken for
			// "the thread had no stack".
			stack, err := os.ReadFile(fmt.Sprintf("%s/%s/stack", taskDir, tid))
			switch {
			case err != nil:
				fmt.Fprintf(&b, "      kernel stack: unavailable: %v\n", err)
			case strings.TrimSpace(string(stack)) == "":
				fmt.Fprintf(&b, "      kernel stack: empty\n")
			default:
				fmt.Fprintf(&b, "      kernel stack:\n")
				for line := range strings.SplitSeq(strings.TrimSpace(string(stack)), "\n") {
					fmt.Fprintf(&b, "        %s\n", strings.TrimSpace(line))
				}
			}
		}
	}
	// When a thread is blocked in the kernel, the working directory and open
	// files of the process narrow down which file/filesystem is stalling.
	if blocked {
		if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			fmt.Fprintf(&b, "    cwd: %s\n", cwd)
		}
		b.WriteString(procOpenFiles(pid))
	}
	return b.String()
}

// procOpenFiles returns a summary of the open file descriptors of pid (each fd
// resolved to its target via /proc/<pid>/fd), which helps identify the file a
// blocked thread is operating on.
func procOpenFiles(pid int) string {
	var b strings.Builder
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		fmt.Fprintf(&b, "    open files: unavailable: %v\n", err)
		return b.String()
	}
	fmt.Fprintf(&b, "    open files:\n")
	for _, fd := range fds {
		target, err := os.Readlink(fmt.Sprintf("%s/%s", fdDir, fd.Name()))
		if err != nil {
			target = fmt.Sprintf("<%v>", err)
		}
		fmt.Fprintf(&b, "      fd %s -> %s\n", fd.Name(), target)
	}
	return b.String()
}

// readStatusFields parses a /proc/<pid>/status (or task/<tid>/status) file into
// a key->value map (values trimmed). Returns an empty map on error.
func readStatusFields(path string) map[string]string {
	out := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[key] = strings.TrimSpace(val)
	}
	return out
}

// readProcFileTrim reads a small /proc file and returns its trimmed contents,
// or "" on error.
func readProcFileTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// selfSIGINTDisposition returns a short description of the current (test)
// process's SIGINT disposition, read from /proc/self/status. A spawned child
// inherits SIG_IGN across exec, so this captures whether the test process was
// ignoring SIGINT at the moment it forked a command.
func selfSIGINTDisposition() string {
	status := readStatusFields("/proc/self/status")
	if len(status) == 0 {
		return "unknown"
	}
	return fmt.Sprintf("ignored=%v caught=%v",
		sigSetHasSIGINT(status["SigIgn"]), sigSetHasSIGINT(status["SigCgt"]))
}

// sigSetHasSIGINT reports whether SIGINT is present in a /proc status signal
// mask, which is a 64-bit hex bitmask with bit (sig-1) set for each signal.
func sigSetHasSIGINT(mask string) bool {
	v, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
	if err != nil {
		return false
	}
	return v&(1<<(uint(unix.SIGINT)-1)) != 0
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// rawOffsetForStripped returns the offset in raw (ANSI-containing) text that
// corresponds to strippedOffset bytes in the ANSI-stripped version of raw.
// This is needed because ANSI escape sequences make the raw text longer than
// the stripped text, so a position in stripped text doesn't directly correspond
// to the same position in raw text.
func rawOffsetForStripped(raw string, strippedOffset int) int {
	stripped := 0
	locs := ansiRegex.FindAllStringIndex(raw, -1)
	pos := 0
	for _, loc := range locs {
		// Count non-ANSI bytes before this escape sequence.
		plainLen := loc[0] - pos
		if stripped+plainLen >= strippedOffset {
			return pos + (strippedOffset - stripped)
		}
		stripped += plainLen
		pos = loc[1]
	}
	// Remaining text after last ANSI sequence.
	return pos + (strippedOffset - stripped)
}

// StripANSI removes ANSI escape sequences from s. Exported for use in tests.
func StripANSI(s string) string {
	return stripANSI(s)
}

// SanitizeOutput provides basic output sanitization compatible with the existing
// golden file format: strips ANSI, removes trailing whitespace from lines,
// and normalizes line endings.
func SanitizeOutput(s string) string {
	s = stripANSI(s)
	// Normalize \r\n to \n.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Remove trailing whitespace from each line.
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}
