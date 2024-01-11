// Package daemon handles the dbus daemon with systemd support.
package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/coreos/go-systemd/daemon"
	"github.com/ubuntu/decorate"
)

// Daemon is a grpc daemon with systemd support.
type Daemon struct {
	service Service

	systemdSdNotifier systemdSdNotifier
}

type options struct {
	// private member that we export for tests.
	systemdSdNotifier func(unsetEnvironment bool, state string) (bool, error)
}

type systemdSdNotifier func(unsetEnvironment bool, state string) (bool, error)

// Option is the function signature used to tweak the daemon creation.
type Option func(*options)

// Service is a server that can Serve and be Stopped by our daemon.
type Service interface {
	Addr() string
	Serve() error
	Stop() error
}

// New returns an new, initialized daemon server, which handles systemd activation.
// If systemd activation is used, it will override any socket passed here.
func New(ctx context.Context, service Service, args ...Option) (d *Daemon, err error) {
	defer decorate.OnError(&err, "can't create daemon")

	slog.Debug("Building new daemon")

	// Set default options.
	opts := options{
		systemdSdNotifier: daemon.SdNotify,
	}
	// Apply given args.
	for _, f := range args {
		f(&opts)
	}

	return &Daemon{
		service: service,

		systemdSdNotifier: opts.systemdSdNotifier,
	}, nil
}

// Serve signals systemd that we are ready to receive from the service.
func (d *Daemon) Serve(ctx context.Context) (err error) {
	defer decorate.OnError(&err, "error while serving")

	slog.Debug("Starting to serve requests")

	// Signal to systemd that we are ready.
	if sent, err := d.systemdSdNotifier(false, "READY=1"); err != nil {
		return fmt.Errorf("couldn't send ready notification to systemd: %v", err)
	} else if sent {
		slog.Debug("Ready state sent to systemd")
	}

	slog.Info(fmt.Sprintf("Serving requests as %v", d.service.Addr()))
	return d.service.Serve()
}

// Quit gracefully quits listening loop and stops the grpc server.
// It can drops any existing connexion is force is true.
func (d Daemon) Quit() {
	slog.Info("Stopping daemon requested.")
	_ = d.service.Stop()
}
