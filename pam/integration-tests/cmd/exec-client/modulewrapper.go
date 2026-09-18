// TiCS: disabled // This is a test helper.

//go:build pam_tests_exec_client

package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/dbusmodule"
	"github.com/godbus/dbus/v5"
	"github.com/msteinert/pam/v2"
)

type moduleWrapper struct {
	dbusmodule.Transaction
}

// Statically Ensure that [moduleWrapper] implements [pam.ModuleTransaction].
var _ pam.ModuleTransaction = &moduleWrapper{}

func newModuleWrapper(serverAddress string) (moduleWrapper, func(), error) {
	mTx, closeFunc, err := dbusmodule.NewTransaction(serverAddress)
	return moduleWrapper{mTx}, closeFunc, err
}

// SimulateClientPanic forces the client to panic with the provided text.
func (m moduleWrapper) CallUnhandledMethod() error {
	method := "com.ubuntu.authd.pam.UnhandledMethod"
	return m.BusObject().Call(method, dbus.FlagNoAutoStart).Err
}

// SimulateClientPanic forces the client to panic with the provided text.
func (m moduleWrapper) SimulateClientPanic(text string) {
	panic(text)
}

// SimulateClientError forces the client to return a new Go error with no PAM type.
func (m moduleWrapper) SimulateClientError(errorMsg string) error {
	return errors.New(errorMsg)
}

// SimulateClientSignal sends a signal to the child process.
func (m moduleWrapper) SimulateClientSignal(sig syscall.Signal, shouldExit bool) {
	pid := os.Getpid()
	log.Debugf(context.Background(), "Sending signal %v to self pid (%v)",
		sig, pid)

	if err := syscall.Kill(pid, sig); err != nil {
		log.Errorf(context.Background(), "Sending signal %v failed: %v", sig, err)
		return
	}

	if shouldExit {
		// The program is expected to exit once the signal is sent, so let's wait
		<-time.After(24 * time.Hour)
	}
}

// SimulateClientSignalAfterDelay sends a signal to the child process after the
// given amount of milliseconds, without blocking the caller, so that the client
// can be killed while it's waiting for another call to be completed.
func (m moduleWrapper) SimulateClientSignalAfterDelay(sig syscall.Signal, delayMs int) {
	time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		m.SimulateClientSignal(sig, false)
	})
}
