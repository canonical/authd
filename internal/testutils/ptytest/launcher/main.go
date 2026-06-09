// package main is a helper binary for ptytest tests that launches a child process.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/canonical/authd/internal/testutils"
)

const (
	controlFD = 3
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "ptytest-launcher: missing command")
		os.Exit(2)
	}

	control := os.NewFile(controlFD, "ptytest-control")
	if control == nil {
		fmt.Fprintln(os.Stderr, "ptytest-launcher: missing control fd")
		os.Exit(2)
	}
	defer control.Close()

	//nolint:gosec // G204 we control the arguments in the tests explicitly.
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(control, "error %s\n", err)
		fmt.Fprintf(os.Stderr, "ptytest-launcher: failed to start %q: %v\n", os.Args[1], err)
		os.Exit(127)
	}

	fmt.Fprintf(control, "pid %d\n", cmd.Process.Pid)

	// If the user sends Ctrl+C through the PTY, the terminal delivers SIGINT to
	// the whole foreground process group. A real shell remains alive while the
	// foreground command handles it, so do the same after the child has started
	// and inherited the default signal disposition.
	signal.Ignore(os.Interrupt)

	err := cmd.Wait()
	if err == nil {
		os.Exit(0)
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		fmt.Fprintf(os.Stderr, "ptytest-launcher: wait failed: %v\n", err)
		os.Exit(127)
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		os.Exit(exitErr.ExitCode())
	}

	if status.Signaled() {
		// Keep the controlling TTY alive briefly after the foreground command is
		// killed, matching the old VHS setup where a shell stayed alive and child
		// processes had time to observe IPC disconnects before terminal hangup.
		time.Sleep(testutils.MultipliedSleepDuration(1 * time.Second))
		os.Exit(128 + int(status.Signal()))
	}

	os.Exit(status.ExitStatus())
}
