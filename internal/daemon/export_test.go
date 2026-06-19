package daemon

import "net"

func WithSystemdActivationListener(f func() ([]net.Listener, error)) func(o *options) {
	return func(o *options) {
		o.systemdActivationListener = f
	}
}

func WithSystemdSdNotifier(f func(unsetEnvironment bool, state string) (bool, error)) func(o *options) {
	return func(o *options) {
		o.systemdSdNotifier = f
	}
}

func (d Daemon) SelectedSocketAddr() string {
	return d.lis.Addr().String()
}
