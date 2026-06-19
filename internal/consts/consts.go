// Package consts defines the constants used by the project
package consts

import log "github.com/canonical/authd/log"

var (
	// Version is the version of the executable.
	Version = "Dev"
)

const (
	// DefaultLogLevel is the default logging level selected without any option.
	DefaultLogLevel = log.NoticeLevel

	// DefaultSocketPath is the default socket path.
	DefaultSocketPath = "/run/authd.sock"

	// DefaultBrokersConfPath is the default configuration directory for the brokers.
	DefaultBrokersConfPath = "/etc/authd/brokers.d/"

	// OldDBDir is the directory where the database was stored by default before 0.3.7.
	OldDBDir = "/var/cache/authd/"

	// DefaultDatabaseDir is the default directory for the database.
	DefaultDatabaseDir = "/var/lib/authd/"

	// DefaultConfigDir is the default configuration directory.
	DefaultConfigDir = "/etc/authd/"

	// DefaultDatabaseFileName is the default file name for the database.
	DefaultDatabaseFileName = "authd.sqlite3"

	// ServiceName is the authd service name for health check purposes.
	ServiceName = "com.ubuntu.authd"
)
