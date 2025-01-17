// Package consts defines the constants used by the project.
package consts

import (
	"github.com/ubuntu/authd/log"
)

var (
	// Version is the version of the executable.
	Version = "Dev"
)

const (
	// TEXTDOMAIN is the gettext domain for l10n.
	TEXTDOMAIN = "authd-oidc"

	// DefaultLevelLog is the default logging level selected without any option.
	DefaultLevelLog = log.WarnLevel
)
