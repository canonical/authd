// Package consts defines the constants used by the project.
package consts

import "log/slog"

var (
	// Version is the version of the executable.
	Version = "Dev"
)

const (
	// TEXTDOMAIN is the gettext domain for l10n.
	TEXTDOMAIN = "oidc-broker"

	// DefaultLevelLog is the default logging level selected without any option.
	DefaultLevelLog = slog.LevelWarn

	// DefaultBrokersConfPath is the default configuration directory for the brokers.
	DefaultBrokersConfPath = "/etc/authd/brokers.d/"
)
