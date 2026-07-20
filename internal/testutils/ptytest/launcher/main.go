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

var forwardedSignals = []os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
}

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

	// ptytest launcher is a test-only stand-in for a shell supervising a real
	// foreground PAM application (login, sshd, ...). Some test environments start
	// the test process with SIGINT ignored; because ignored dispositions survive
	// exec, the child command would then ignore Ctrl+C too. Registering the handler
	// before starting the child makes the child inherit a caught SIGINT, which exec
	// resets to the default disposition, matching a normally-launched foreground
	// command. If the launcher is terminated explicitly in tests, forward the same
	// termination signal to the child rather than translating it.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, forwardedSignals...)

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

	go func() {
		for sig := range sigChan {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

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
