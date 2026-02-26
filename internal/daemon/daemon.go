// Package daemon handles the GRPC daemon with systemd support.
package daemon

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/log"
	"github.com/coreos/go-systemd/v22/activation"
	"github.com/coreos/go-systemd/v22/daemon"
	"google.golang.org/grpc"
)

// Daemon is a grpc daemon with systemd support.
type Daemon struct {
	grpcServer *grpc.Server
	lis        net.Listener

	systemdSdNotifier systemdSdNotifier
}

type options struct {
	socketPath string

	// private member that we export for tests.
	systemdActivationListener func() ([]net.Listener, error)
	systemdSdNotifier         func(unsetEnvironment bool, state string) (bool, error)
}

type systemdSdNotifier func(unsetEnvironment bool, state string) (bool, error)

// Option is the function signature used to tweak the daemon creation.
type Option func(*options)

// WithSocketPath uses a manual socket path instead of socket activation.
func WithSocketPath(p string) func(o *options) {
	return func(o *options) {
		o.socketPath = p
	}
}

// GRPCServiceRegisterer is a function that the daemon will call everytime we want to build a new GRPC object.
type GRPCServiceRegisterer func(context.Context) *grpc.Server

// New returns an new, initialized daemon server, which handles systemd activation.
// If systemd activation is used, it will override any socket passed here.
func New(ctx context.Context, registerGRPCService GRPCServiceRegisterer, args ...Option) (d *Daemon, err error) {
	defer decorate.OnError(&err /*i18n.G(*/, "can't create daemon") //)

	log.Debug(ctx, "Building new daemon")

	// Set default options.
	opts := options{
		socketPath: "",

		systemdActivationListener: activation.Listeners,
		systemdSdNotifier:         daemon.SdNotify,
	}
	// Apply given args.
	for _, f := range args {
		f(&opts)
	}

	// systemd socket activation or local creation
	var lis net.Listener

	if opts.socketPath != "" {
		log.Debugf(ctx, "Listening on %s", opts.socketPath)

		// manual socket
		// TODO: if socket exists, remove
		lis, err = net.Listen("unix", opts.socketPath)
		if err != nil {
			return nil, err
		}

		//nolint:gosec // We want everyone to be able to write to our socket and we will filter permissions
		if err = os.Chmod(opts.socketPath, 0666); err != nil {
			return nil, fmt.Errorf("could not change socket permission: %v", err)
		}
	} else {
		log.Debug(ctx, "Use socket activation")

		// systemd activation
		listeners, err := opts.systemdActivationListener()
		if err != nil {
			return d, err
		}

		if len(listeners) != 1 {
			return nil, fmt.Errorf( /*i18n.G(*/ "unexpected number of systemd socket activation (%d != 1)" /*)*/, len(listeners))
		}
		lis = listeners[0]
	}

	// Ensure selected socket exists.
	if _, err := os.Stat(lis.Addr().String()); err != nil {
		return nil, fmt.Errorf("%s can’t be accessed: %v", lis.Addr().String(), err)
	}

	return &Daemon{
		grpcServer: registerGRPCService(ctx),
		lis:        lis,

		systemdSdNotifier: opts.systemdSdNotifier,
	}, nil
}

// Serve listens on a tcp socket and starts serving GRPC requests on it.
func (d *Daemon) Serve(ctx context.Context) (err error) {
	defer decorate.OnError(&err /*i18n.G(*/, "error while serving") //)

	log.Debugf(ctx, "Starting to serve requests on %s", d.lis.Addr())

	// Signal to systemd that we are ready.
	if sent, err := d.systemdSdNotifier(false, "READY=1"); err != nil {
		return fmt.Errorf( /*i18n.G(*/ "couldn't send ready notification to systemd: %v" /*)*/, err)
	} else if sent {
		log.Debug(context.Background(), "Ready state sent to systemd")
	}

	log.Infof(ctx, "Serving gRPC requests on %v", d.lis.Addr())
	if err := d.grpcServer.Serve(d.lis); err != nil {
		return fmt.Errorf("gRPC error: %v", err)
	}
	return nil
}

// Quit gracefully quits listening loop and stops the grpc server.
// It can drops any existing connexion is force is true.
func (d Daemon) Quit(ctx context.Context, force bool) {
	if force {
		d.grpcServer.Stop()
		return
	}

	log.Info(ctx, "Waiting for pending requests to finish")
	d.grpcServer.GracefulStop()
	log.Info(ctx, "gRPC server gracefully stopped")
}
