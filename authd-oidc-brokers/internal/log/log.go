// Package log configures the logging utilities for the project.
package log

import (
	"log/slog"
	"os"
)

var globalLevel = &slog.LevelVar{}

func init() {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: globalLevel})
	slog.SetDefault(slog.New(h))
	globalLevel.Set(slog.LevelWarn)
}

// SetLevel change global handler log level.
func SetLevel(l slog.Level) {
	globalLevel.Set(l)
}
