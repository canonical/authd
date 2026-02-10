// Package log provides logging functions for authctl.
package log

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"
)

var useColor = sync.OnceValue(func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	return term.IsTerminal(int(os.Stderr.Fd()))
})

// Info prints a message to stderr.
func Info(a ...any) {
	fmt.Fprintln(os.Stderr, fmt.Sprint(a...))
}

// Infof prints a formatted message to stderr.
func Infof(format string, args ...any) {
	Info(fmt.Sprintf(format, args...))
}

// Notice prints a message to stderr in bold.
func Notice(a ...any) {
	if !useColor() {
		fmt.Fprintln(os.Stderr, fmt.Sprint(a...))
		return
	}
	fmt.Fprintln(os.Stderr, "\033[0;1;39m"+fmt.Sprint(a...)+"\033[0m")
}

// Noticef prints a formatted message to stderr in bold.
func Noticef(format string, args ...any) {
	Notice(fmt.Sprintf(format, args...))
}

// Warning prints a message to stderr in yellow.
func Warning(a ...any) {
	if !useColor() {
		fmt.Fprintln(os.Stderr, fmt.Sprint(a...))
		return
	}
	fmt.Fprintln(os.Stderr, "\033[0;1;38:5:185m"+fmt.Sprint(a...)+"\033[0m")
}

// Warningf prints a formatted message to stderr in yellow.
func Warningf(format string, args ...any) {
	Warning(fmt.Sprintf(format, args...))
}

// Error prints a message to stderr in red.
func Error(a ...any) {
	if !useColor() {
		fmt.Fprintln(os.Stderr, fmt.Sprint(a...))
		return
	}
	fmt.Fprintln(os.Stderr, "\033[1;31m"+fmt.Sprint(a...)+"\033[0m")
}

// Errorf prints a formatted message to stderr in red.
func Errorf(format string, args ...any) {
	Error(fmt.Sprintf(format, args...))
}
