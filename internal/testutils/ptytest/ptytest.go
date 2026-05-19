// Package ptytest provides a PTY-based test harness for interactive terminal
// testing. It spawns commands in a pseudo-terminal and provides event-driven
// Send/WaitFor primitives.
package ptytest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/canonical/authd/internal/testutils"
	"github.com/stretchr/testify/require"
)

// Predefined key constants for SendKey.
const (
	KeyEnter  = '\r'
	KeyEscape = '\x1b'
	KeyCtrlC  = '\x03'
	KeyCtrlD  = '\x04'
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

// options holds configuration for a Console.
type options struct {
	env     []string
	dir     string
	cols    uint16
	rows    uint16
	timeout time.Duration
}

// defaultOptions returns the default configuration.
func defaultOptions() options {
	return options{
		cols:    160,
		rows:    50,
		timeout: testutils.MultipliedSleepDuration(10 * time.Second),
	}
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
		cmd:      cmd,
		ptmx:     ptmx,
		opts:     o,
		done:     make(chan error, 1),
		copyDone: make(chan struct{}),
	}

	// Background goroutine: read PTY output.
	go func() {
		defer close(c.copyDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Background goroutine: wait for command exit.
	go func() {
		c.done <- cmd.Wait()
	}()

	t.Cleanup(func() { c.Close(t) })

	t.Logf("ptytest: started %q %v (pid %d, pty %s, %dx%d)",
		name, args, cmd.Process.Pid, ptmx.Name(), o.cols, o.rows)

	return c
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
	fmt.Fprintf(&b, "%s\n%s\n", sectionHeader("current terminal screen"), ProcessRawOutput(content))

	title := "text seen since last match"
	fmt.Fprintf(&b, "\n%s\n%s\n%s",
		sectionHeader(title),
		ProcessRawOutput(rawContent),
		sectionFooter(title))

	return b.String()
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

// WaitForExit blocks until the command exits. Returns the exit error (nil on success).
func (c *Console) WaitForExit(t *testing.T) error {
	t.Helper()

	t.Logf("ptytest: WaitForExit()")

	select {
	case err := <-c.done:
		c.done <- err // put it back for Close
		return err
	case <-time.After(c.opts.timeout):
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

// Close terminates the command (if still running) and cleans up the PTY.
// It is safe to call multiple times. It is also called automatically on
// test cleanup.
func (c *Console) Close(t *testing.T) {
	t.Helper()

	if c.closed {
		return
	}
	c.closed = true

	// Try graceful shutdown first: close the PTY (sends EOF/SIGHUP to child).
	c.ptmx.Close()

	select {
	case <-c.done:
		// Process already exited.
	case <-time.After(5 * time.Second):
		// Force kill.
		t.Logf("ptytest: killing process %d (did not exit after PTY close)", c.cmd.Process.Pid)
		_ = c.cmd.Process.Kill()
		<-c.done
	}
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
