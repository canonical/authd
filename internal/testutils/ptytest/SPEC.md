# ptytest — PTY-based Interactive Test Harness

## Problem Statement

The PAM integration tests currently use [charmbracelet/vhs](https://github.com/charmbracelet/vhs) to automate interactive terminal sessions. VHS is designed for creating terminal GIFs/videos, not for testing. This mismatch causes:

- **Heavyweight dependency**: VHS uses headless Chromium (via go-rod) to render a virtual terminal, making it slow to start, impossible to install in some CI environments (hence `AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS`), and resource-intensive.
- **Fragility**: Tests rely on timing-based synchronization (`Sleep`, `WaitTimeout`), leading to flaky tests (several are gated behind `AUTHD_SKIP_FLAKY_TESTS`).
- **Maintenance burden**: A 738-line regex-based preprocessor (`vhs-helpers_test.go`) translates custom commands (`Wait+Suffix`, `Wait+CLIPrompt`, `TypeUsername`, etc.) into VHS's DSL — a mini-language on top of a mini-language.
- **Debugging difficulty**: When a test fails, you get a sanitized terminal text dump with no easy way to see intermediate states or understand why a wait pattern didn't match.

## Solution

Replace VHS with `ptytest`, a lightweight, pure-Go PTY test harness that spawns commands in a pseudo-terminal and provides event-driven `Send`/`WaitFor` primitives.

## Design Goals

1. **Zero external dependencies** — uses only `golang.org/x/term` (already in go.mod) and the Go stdlib; no Chromium, no VHS binary.
2. **Event-driven, not timing-driven** — `WaitFor` blocks until a regex matches the terminal output, with a configurable timeout. No `Sleep`-based synchronization.
3. **Debuggable** — every `Send` and `WaitFor` is logged via `t.Log` with timestamps; on failure, the full terminal buffer is dumped.
4. **Compatible with golden files** — produces sanitized terminal output text that can be compared using the existing `golden.CheckOrUpdate` infrastructure.
5. **Incremental migration** — can coexist with VHS tests; tests can be converted one at a time.
6. **Thin API** — small surface area, easy to understand and maintain.

## API

```go
package ptytest

import (
    "os/exec"
    "regexp"
    "testing"
    "time"
)

// Option configures a PTY test session.
type Option func(*options)

// WithTimeout sets the default timeout for WaitFor operations.
// Default: 10s (respecting testutils.MultipliedSleepDuration).
func WithTimeout(d time.Duration) Option

// WithEnv sets environment variables for the spawned command.
func WithEnv(env []string) Option

// WithSize sets the terminal size (columns, rows). Default: 160x50.
func WithSize(cols, rows uint16) Option

// WithDir sets the working directory for the spawned command.
func WithDir(dir string) Option

// Console represents a running command in a PTY.
type Console struct { ... }

// Start spawns the command in a PTY and returns a Console.
// The command is automatically terminated when the test ends.
func Start(t *testing.T, name string, args []string, opts ...Option) *Console

// Send writes raw bytes to the PTY (as if typed by the user).
func (c *Console) Send(t *testing.T, s string)

// SendLine writes s followed by a newline.
func (c *Console) SendLine(t *testing.T, s string)

// SendKey sends a single control byte to the PTY.
// Predefined keys: KeyEscape, KeyCtrlC, KeyCtrlD, KeyEnter.
func (c *Console) SendKey(t *testing.T, key byte)

// WaitFor blocks until the accumulated terminal output (since the last
// WaitFor) matches the given regexp pattern, or the timeout expires.
// On timeout, the test is failed with the full terminal output dump.
// Returns the full output up to and including the match.
func (c *Console) WaitFor(t *testing.T, pattern string) string

// WaitForTimeout is like WaitFor but with an explicit timeout override.
func (c *Console) WaitForTimeout(t *testing.T, pattern string, timeout time.Duration) string

// WaitForExit blocks until the command exits. Returns the exit error (nil on success).
func (c *Console) WaitForExit(t *testing.T) error

// Output returns all terminal output captured so far.
func (c *Console) Output() string

// Close terminates the command (if still running) and cleans up.
// Called automatically on test cleanup — manual use is optional.
func (c *Console) Close(t *testing.T)
```

### Predefined Keys

```go
const (
    KeyEnter  = '\r'
    KeyEscape = '\x1b'
    KeyCtrlC  = '\x03'
    KeyCtrlD  = '\x04'
)
```

## Implementation Details

### PTY Management

Uses `golang.org/x/term` plus the raw `ioctl` / `openpty` POSIX syscalls via Go's `os/exec` + `syscall.Setsid` pattern. Since Go 1.21, `os/exec.Cmd` supports `SysProcAttr.Setctty` and file descriptor passing natively. We use the `TIOCSWINSZ` ioctl for terminal sizing.

Alternatively we can use a minimal fork/inline of the PTY open logic — the core is ~30 lines wrapping `posix_openpt` / `grantpt` / `unlockpt` / `ptsname`, or we use `/dev/ptmx` directly, which is the standard Linux approach.

### Output Capture

A background goroutine continuously reads from the PTY master fd into a `bytes.Buffer` (mutex-protected). `WaitFor` does a polling loop (every 50ms) checking the buffer against the regex, with context-aware timeout.

### ANSI Stripping

Terminal output contains ANSI escape codes (colors, cursor movement). The `WaitFor` regex operates on **ANSI-stripped** text. The raw output is preserved for debugging. An ANSI stripper is included (simple regex: `\x1b\[[0-9;]*[a-zA-Z]` plus OSC sequences).

### Golden File Compatibility

`Output()` returns ANSI-stripped text. Frame separators (the `─` lines currently used by VHS) are not added by default — the test author controls the output format. The existing `golden.CheckOrUpdate(t, console.Output())` continues to work.

## Migration Strategy

1. **Phase 1** (this PR): Implement `ptytest` package with tests.
2. **Phase 2**: Convert a few representative tests (e.g., `simple_auth`, `mfa_auth`, `sigint`) to validate the approach works end-to-end.
3. **Phase 3**: Convert remaining tests incrementally — both approaches coexist.
4. **Phase 4**: Remove VHS dependency, delete `vhs-helpers_test.go` and `testdata/tapes/`.

## Example: Converting `simple_auth`

### Before (VHS tape + Go test):

```
# simple_auth.tape
Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show
Hide
TypeUsername "${AUTHD_TEST_TAPE_USERNAME}"
Show
Hide
Enter
Wait+Screen /Select your provider/
Wait+Screen /2. ExampleBroker/
Show
Hide
Type "2"
Wait+CLIPrompt /Gimme your password/ /Press escape key to.../
Show
Hide
TypeCLIPassword "goodpass"
Show
Hide
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
```

### After (pure Go):

```go
func TestCLIAuthenticate_SimpleAuth(t *testing.T) {
    t.Parallel()

    socketPath, _ := sharedAuthd(t)
    clientPath := buildPAMRunner(t)
    username := vhsTestUserName(t, "simple")

    c := ptytest.Start(t, clientPath,
        []string{"login", "socket=" + socketPath},
        ptytest.WithEnv(cliEnv),
        ptytest.WithSize(160, 50),
    )

    c.WaitFor(t, `Username:`)
    c.SendLine(t, username)
    c.WaitFor(t, `Select your provider`)
    c.WaitFor(t, `2\. ExampleBroker`)
    c.SendLine(t, "2")
    c.WaitFor(t, `Gimme your password`)
    c.SendLine(t, "goodpass")
    c.WaitFor(t, regexp.QuoteMeta(pam_test.RunnerResultActionAuthenticate.String()))
    c.WaitFor(t, regexp.QuoteMeta(pam_test.RunnerResultActionAcctMgmt.String()))

    golden.CheckOrUpdate(t, c.Output())
}
```
