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
	pam.ModuleTransaction
}

func newModuleWrapper(serverAddress string) (pam.ModuleTransaction, func(), error) {
	mTx, closeFunc, err := dbusmodule.NewTransaction(context.TODO(), serverAddress)
	return &moduleWrapper{mTx}, closeFunc, err
}

// SimulateClientPanic forces the client to panic with the provided text.
func (m *moduleWrapper) CallUnhandledMethod() error {
	method := "com.ubuntu.authd.pam.UnhandledMethod"
	tx, _ := m.ModuleTransaction.(*dbusmodule.Transaction)
	return tx.BusObject().Call(method, dbus.FlagNoAutoStart).Err
}

// SimulateClientPanic forces the client to panic with the provided text.
func (m *moduleWrapper) SimulateClientPanic(text string) {
	panic(text)
}

// SimulateClientError forces the client to return a new Go error with no PAM type.
func (m *moduleWrapper) SimulateClientError(errorMsg string) error {
	return errors.New(errorMsg)
}

// SimulateClientSignal sends a signal to the child process.
func (m *moduleWrapper) SimulateClientSignal(sig syscall.Signal, shouldExit bool) {
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
