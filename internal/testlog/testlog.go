// Package testlog provides utilities for logging test output.
package testlog

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	isVerboseOnce sync.Once
	isVerbose     bool
)

type runWithTimingOptions struct {
	doNotSetStdoutAndStderr         bool
	onlyPrintStdoutAndStderrOnError bool
}

// RunWithTimingOption is a function that configures the RunWithTiming function.
type RunWithTimingOption func(options *runWithTimingOptions)

// DoNotSetStdoutAndStderr prevents RunWithTiming from setting the stdout and stderr of the given command.
// By default, RunWithTiming sets stdout and stderr to the test's output.
func DoNotSetStdoutAndStderr() RunWithTimingOption {
	return func(options *runWithTimingOptions) {
		options.doNotSetStdoutAndStderr = true
	}
}

// OnlyPrintStdoutAndStderrOnError makes RunWithTiming only print the stdout and stderr of the given command if it fails.
func OnlyPrintStdoutAndStderrOnError() RunWithTimingOption {
	return func(options *runWithTimingOptions) {
		options.onlyPrintStdoutAndStderrOnError = true
	}
}

// RunWithTiming runs the given command while logging its duration with the provided message.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func RunWithTiming(t *testing.T, msg string, cmd *exec.Cmd, options ...RunWithTimingOption) error {
	if t != nil {
		t.Helper()
	}

	w := testOutput(t)

	opts := runWithTimingOptions{}
	for _, f := range options {
		f(&opts)
	}

	if opts.doNotSetStdoutAndStderr && opts.onlyPrintStdoutAndStderrOnError {
		panic("onlyPrintStdoutAndStderrOnError and doNotSetStdoutAndStderr cannot be used together")
	}

	if !opts.doNotSetStdoutAndStderr && !opts.onlyPrintStdoutAndStderrOnError {
		cmd.Stdout = w
		cmd.Stderr = w
	}

	LogCommand(t, msg, cmd)
	start := time.Now()

	var err error
	var out []byte
	if opts.onlyPrintStdoutAndStderrOnError {
		out, err = cmd.CombinedOutput()
		if err != nil {
			_, _ = w.Write(out)
		}
	} else {
		err = cmd.Run()
	}
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintln(w, redSeparatorf("%s failed in %.3fs with %v", msg, duration.Seconds(), err)+"\n")
	} else {
		fmt.Fprintln(w, separatorf("%s finished in %.3fs", msg, duration.Seconds())+"\n")
	}

	return err
}

// LogCommand logs the given command to stderr.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func LogCommand(t *testing.T, msg string, cmd *exec.Cmd) {
	if t != nil {
		t.Helper()
	}
	w := testOutput(t)

	sep := "----------------------------------------"
	fmt.Fprintf(w, "\n"+separator(msg)+"command: %s\n%s\nenvironment: %s\n%s\n", cmd.String(), sep, cmd.Env, sep)
}

// LogStartSeparatorf logs a separator to stderr with the given formatted message.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func LogStartSeparatorf(t *testing.T, s string, args ...any) {
	if t != nil {
		t.Helper()
	}
	w := testOutput(t)

	fmt.Fprintln(w, "\n"+separatorf(s, args...))
}

// LogStartSeparator logs a separator to stderr with the given message.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func LogStartSeparator(t *testing.T, args ...any) {
	if t != nil {
		t.Helper()
	}
	w := testOutput(t)

	fmt.Fprintln(w, "\n"+separator(args...))
}

// LogEndSeparatorf logs a separator to stderr with the given formatted message.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func LogEndSeparatorf(t *testing.T, s string, args ...any) {
	if t != nil {
		t.Helper()
	}
	w := testOutput(t)

	fmt.Fprintln(w, separatorf(s, args...)+"\n")
}

// LogEndSeparator logs a separator to stderr with the given message.
//
//nolint:thelper // we do call t.Helper() if t is not nil
func LogEndSeparator(t *testing.T, args ...any) {
	if t != nil {
		t.Helper()
	}
	w := testOutput(t)

	fmt.Fprintln(w, separator(args...)+"\n")
}

// separatorf returns a formatted separator string for logging purposes.
func separatorf(s string, args ...any) string {
	return highCyan("===== " + fmt.Sprintf(s, args...) + " =====\n")
}

// separator returns a separator string for logging purposes.
func separator(args ...any) string {
	return highCyan("===== " + fmt.Sprint(args...) + " =====\n")
}

func redSeparatorf(s string, args ...any) string {
	return highRed("===== " + fmt.Sprintf(s, args...) + " =====\n")
}

// highCyan returns a string with the given text in high-intensity cyan color for terminal output.
func highCyan(s string) string {
	return fmt.Sprintf("\033[1;36m%s\033[0m", s)
}

// highRed returns a string with the given text in high-intensity red color for terminal output.
func highRed(s string) string {
	return fmt.Sprintf("\033[1;31m%s\033[0m", s)
}

// testOutput returns the appropriate output writer for the test.
//
//nolint:thelper // we're not using t in any way that requires the helper annotation
func testOutput(t *testing.T) io.Writer {
	if t != nil {
		return &syncWriter{w: t.Output()}
	}
	if verbose() {
		return os.Stderr
	}
	return io.Discard
}

// verbose returns whether verbose mode is enabled.
// testing.Verbose() should be used instead when possible, this function is only
// needed because testing.Verbose() panics when called in a TestMain function.
func verbose() bool {
	isVerboseOnce.Do(func() {
		for _, arg := range os.Args {
			value, ok := strings.CutPrefix(arg, "-test.v=")
			if !ok {
				continue
			}
			isVerbose = value == "true"
		}
	})
	return isVerbose
}

// syncWriter is a writer that synchronizes writes to its underlying writer.
type syncWriter struct {
	w  io.Writer
	mu sync.Mutex
}

// Write writes to the underlying writer while synchronizing access.
func (s *syncWriter) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.Write(p)
}
