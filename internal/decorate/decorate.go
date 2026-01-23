// Package decorate contains various helpers to decorate errors with fewer lines of code in functions.
package decorate

import (
	"context"
	"fmt"

	"github.com/canonical/authd/log"
)

// OnError prefixes any error with format/args.
func OnError(err *error, format string, args ...interface{}) {
	if *err != nil {
		s := fmt.Sprintf(format, args...)
		*err = fmt.Errorf("%s: %w", s, *err)
	}
}

// LogOnError logs only any errors without failing.
func LogOnError(err error) {
	LogOnErrorContext(context.Background(), err)
}

// LogOnErrorContext logs any errors without failing. It takes a context.
func LogOnErrorContext(ctx context.Context, err error) {
	if err != nil {
		log.Warning(ctx, err)
	}
}

// LogFuncOnError logs only any errors returned by f without failing.
func LogFuncOnError(f func() error) {
	LogFuncOnErrorContext(context.Background(), f)
}

// LogFuncOnErrorContext logs only error returned by f without failing. It takes a context.
func LogFuncOnErrorContext(ctx context.Context, f func() error) {
	if err := f(); err != nil {
		log.Warning(ctx, err)
	}
}
