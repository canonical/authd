// Package log is a temporary package until we forge our log structure.
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"sync"
	"sync/atomic"
)

type (
	// Level is the log level for the logs.
	Level = slog.Level

	// Handler is the log handler function.
	Handler = func(_ context.Context, _ Level, format string, args ...interface{})
)

var logLevelMu = sync.RWMutex{}
var logLevel = NoticeLevel

var hasCustomOutput atomic.Pointer[io.Writer]

const (
	// ErrorLevel level. Logs. Used for errors that should definitely be noted.
	// Commonly used for hooks to send errors to an error tracking service.
	ErrorLevel = slog.LevelError
	// WarnLevel level. Non-critical entries that deserve eyes.
	WarnLevel = slog.LevelWarn
	// NoticeLevel level. Normal but significant conditions. Conditions that are not error conditions, but that may
	// require special handling. slog doesn't have a Notice level, so we use the average between Info and Warn.
	NoticeLevel = (slog.LevelInfo + slog.LevelWarn) / 2
	// InfoLevel level. General operational entries about what's going on inside the application.
	InfoLevel = slog.LevelInfo
	// DebugLevel level. Usually only enabled when debugging. Very verbose logging.
	DebugLevel = slog.LevelDebug
)

func logFuncAdapter(slogFunc func(ctx context.Context, msg string, args ...interface{})) Handler {
	return func(ctx context.Context, _ Level, format string, args ...interface{}) {
		slogFunc(ctx, fmt.Sprintf(format, args...))
	}
}

var allLevels = []slog.Level{
	DebugLevel,
	InfoLevel,
	NoticeLevel,
	WarnLevel,
	ErrorLevel,
}

var defaultHandlers = map[Level]Handler{
	DebugLevel: logFuncAdapter(slog.DebugContext),
	InfoLevel:  logFuncAdapter(slog.InfoContext),
	// slog doesn't have a Notice level, so in the default handler, we use Warn instead.
	NoticeLevel: logFuncAdapter(slog.WarnContext),
	WarnLevel:   logFuncAdapter(slog.WarnContext),
	ErrorLevel:  logFuncAdapter(slog.ErrorContext),
}
var handlers = maps.Clone(defaultHandlers)
var handlersMu = sync.RWMutex{}

func init() {
	SetOutput(os.Stderr)
}

// GetLevel gets the standard logger level.
func GetLevel() Level {
	logLevelMu.RLock()
	defer logLevelMu.RUnlock()
	return logLevel
}

// IsLevelEnabled checks if the log level is greater than the level param.
func IsLevelEnabled(level Level) bool {
	return isLevelEnabled(context.Background(), level)
}

func isLevelEnabled(context context.Context, level Level) bool {
	return slog.Default().Enabled(context, level)
}

// SetLevel sets the standard logger level.
func SetLevel(level Level) (oldLevel Level) {
	logLevelMu.Lock()
	defer func() {
		logLevelMu.Unlock()
		if outPtr := hasCustomOutput.Load(); outPtr != nil {
			SetOutput(*outPtr)
		}
	}()
	logLevel = level
	return slog.SetLogLoggerLevel(level)
}

// SetOutput sets the log output.
func SetOutput(out io.Writer) {
	hasCustomOutput.Store(&out)
	slog.SetDefault(slog.New(NewSimpleHandler(out, GetLevel())))
}

// SetLevelHandler allows to define the default handler function for a given level.
func SetLevelHandler(level Level, handler Handler) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	if handler == nil {
		h, ok := defaultHandlers[level]
		if !ok {
			return
		}
		handler = h
	}
	handlers[level] = handler
}

// SetHandler allows to define the default handler function for all log levels.
func SetHandler(handler Handler) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	if handler == nil {
		handlers = maps.Clone(defaultHandlers)
		return
	}
	for _, level := range allLevels {
		handlers[level] = handler
	}
}

func log(context context.Context, level Level, args ...interface{}) {
	if !isLevelEnabled(context, level) {
		return
	}

	logf(context, level, fmt.Sprint(args...))
}

func logf(context context.Context, level Level, format string, args ...interface{}) {
	if !isLevelEnabled(context, level) {
		return
	}

	handlersMu.RLock()
	handler := handlers[level]
	handlersMu.RUnlock()

	handler(context, level, format, args...)
}

// Debug outputs messages with the level [DebugLevel] (when that is enabled) using the
// configured logging handler.
func Debug(context context.Context, args ...interface{}) {
	log(context, DebugLevel, args...)
}

// Debugf outputs messages with the level [DebugLevel] (when that is enabled) using the
// configured logging handler.
func Debugf(context context.Context, format string, args ...interface{}) {
	logf(context, DebugLevel, format, args...)
}

// Info outputs messages with the level [InfoLevel] (when that is enabled) using the
// configured logging handler.
func Info(context context.Context, args ...interface{}) {
	log(context, InfoLevel, args...)
}

// Infof outputs messages with the level [InfoLevel] (when that is enabled) using the
// configured logging handler.
func Infof(context context.Context, format string, args ...interface{}) {
	logf(context, InfoLevel, format, args...)
}

// Notice outputs messages with the level [NoticeLevel] (when that is enabled) using the
// configured logging handler.
func Notice(context context.Context, args ...interface{}) {
	log(context, NoticeLevel, args...)
}

// Noticef outputs messages with the level [NoticeLevel] (when that is enabled) using the
// configured logging handler.
func Noticef(context context.Context, format string, args ...interface{}) {
	logf(context, NoticeLevel, format, args...)
}

// Warning outputs messages with the level [WarningLevel] (when that is enabled) using the
// configured logging handler.
func Warning(context context.Context, args ...interface{}) {
	log(context, WarnLevel, args...)
}

// Warningf outputs messages with the level [WarningLevel] (when that is enabled) using the
// configured logging handler.
func Warningf(context context.Context, format string, args ...interface{}) {
	logf(context, WarnLevel, format, args...)
}

// Error outputs messages with the level [ErrorLevel] (when that is enabled) using the
// configured logging handler.
func Error(context context.Context, args ...interface{}) {
	log(context, ErrorLevel, args...)
}

// Errorf outputs messages with the level [ErrorLevel] (when that is enabled) using the
// configured logging handler.
func Errorf(context context.Context, format string, args ...interface{}) {
	logf(context, ErrorLevel, format, args...)
}
