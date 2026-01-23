//go:build !withmo || windows

// Package po allows embedding po files in project in development mode or windows.
package po

import "embed"

// Files containing po files
//
//go:embed *.po
var Files embed.FS
