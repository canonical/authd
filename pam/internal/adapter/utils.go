package adapter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/msteinert/pam/v2"
	"golang.org/x/sys/unix"
)

var (
	isSSHSessionValue bool
	isSSHSessionOnce  sync.Once

	isTerminalTTYValue bool
	isTerminalTTYOnce  sync.Once
)

// convertTo converts an interface I value to T. It will panic (progamming error) if this is not the case.
func convertTo[T any, I any](elem I) T {
	//nolint:forcetypeassert // if the conversion do not pass, this is a programmer error. Assert it hard.
	return any(elem).(T)
}

// TeaHeadlessOptions gets the options to run a bubbletea program in headless mode.
func TeaHeadlessOptions() ([]tea.ProgramOption, error) {
	// Explicitly set the output to something so that the program
	// won't try to init some terminal fancy things that also appear
	// to be racy...
	// See: https://github.com/charmbracelet/bubbletea/issues/910
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, errors.Join(err, pam.ErrSystem)
	}
	return []tea.ProgramOption{
		tea.WithInput(nil),
		tea.WithoutRenderer(),
		tea.WithoutSignals(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
		tea.WithOutput(devNull),
	}, nil
}

func isSSHSessionFunc(mTx pam.ModuleTransaction) bool {
	service, _ := mTx.GetItem(pam.Service)
	if service == "sshd" {
		return true
	}

	envs, err := mTx.GetEnvList()
	if err != nil {
		return false
	}
	if _, ok := envs["SSH_CONNECTION"]; ok {
		return true
	}
	if _, ok := envs["SSH_AUTH_INFO_0"]; ok {
		return true
	}
	return false
}

// isSSHSession checks if the module transaction is currently handling a SSH session.
func isSSHSession(mTx pam.ModuleTransaction) bool {
	isSSHSessionOnce.Do(func() { isSSHSessionValue = isSSHSessionFunc(mTx) })
	return isSSHSessionValue
}

// GetPamIO returns the input and output files to use to interact with the
// user, preferring the PAM tty when it is set and can be opened.
//
// When a PAM tty is used, it is returned for both input and output, so that
// the interface can still work when the standard streams are redirected.
// Otherwise stdin is used for input and stdout for output, since stdin is not
// guaranteed to be writable.
func GetPamIO(mTx pam.ModuleTransaction) (input, output *os.File, cleanup func()) {
	pamTTYPath, err := mTx.GetItem(pam.Tty)
	if err != nil || pamTTYPath == "" {
		log.Debugf(context.Background(), "No PAM TTY set")
		return os.Stdin, os.Stdout, func() {}
	}

	log.Debugf(context.Background(), "PAM TTY is %q", pamTTYPath)
	tty, err := os.OpenFile(pamTTYPath, os.O_RDWR, 0600)
	if err != nil {
		log.Warningf(context.Background(), "Failed to open PAM TTY %q: %s", pamTTYPath, err)
		return os.Stdin, os.Stdout, func() {}
	}

	// We check the fd could be passed to x/term to decide if we can use it
	if tty.Fd() > math.MaxInt {
		log.Warningf(context.Background(), "Unexpected large PAM TTY fd: %d", tty.Fd())
		tty.Close()
		return os.Stdin, os.Stdout, func() {}
	}

	return tty, tty, func() { tty.Close() }
}

// IsTerminalTTY returns whether the [pam.Tty] or the standard streams are
// terminals that can be used for the interactive interface.
func IsTerminalTTY(mTx pam.ModuleTransaction) bool {
	isTerminalTTYOnce.Do(func() {
		input, output, cleanup := GetPamIO(mTx)
		defer cleanup()

		pamTTY, err := mTx.GetItem(pam.Tty)
		if err == nil && pamTTY != "" && input == os.Stdin {
			// PAM_TTY is set but we could not open it, so we can't use the
			// interactive interface on the terminal.
			return
		}

		// Both the input and the output are used by the interface, so they
		// must be terminals we can use. For a PAM tty they are the same file.
		if !isTTYUsable(input) {
			return
		}
		if input != output && !isTTYUsable(output) {
			return
		}

		isTerminalTTYValue = true
	})
	return isTerminalTTYValue
}

// isTTYUsable returns whether the given file is a terminal that can be used for
// the interactive interface, without us getting stopped when switching it to
// raw mode.
func isTTYUsable(tty *os.File) bool {
	if !term.IsTerminal(tty.Fd()) {
		return false
	}

	// MakeRaw sends SIGTTOU when the tty is our controlling terminal but we
	// are not in its foreground pgrp. ENOTTY means the tty is not our
	// controlling terminal, which is safe to use for a PAM tty we opened
	// ourselves, but not for the standard streams we're supposed to be
	// attached to.
	fd := int(tty.Fd())
	foregroundPgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	switch {
	case err == nil:
		if foregroundPgrp != unix.Getpgrp() {
			log.Debugf(context.Background(),
				"Tty %q (FD: %v) is in the background (%d != %d), can't use it",
				tty.Name(), fd, foregroundPgrp, unix.Getpgrp())
			return false
		}
	case !errors.Is(err, unix.ENOTTY) || tty == os.Stdin || tty == os.Stdout:
		log.Warningf(context.Background(),
			"Failed to get the foreground process group of tty %q (FD: %v): %s",
			tty.Name(), fd, err)
		return false
	default:
		log.Debugf(context.Background(), "TTY %q (FD: %v) is not our controlling terminal",
			tty.Name(), fd)
	}

	oldState, err := term.MakeRaw(tty.Fd())
	if err != nil {
		log.Warningf(context.Background(), "Failed to set terminal to raw mode: %s", err)
		return false
	}

	if err := term.Restore(tty.Fd(), oldState); err != nil {
		log.Warningf(context.Background(), "Failed to restore terminal state: %s", err)
	}

	return true
}

// IsDumbTerminal returns whether the TERM environment variable is set to "dumb".
// Dumb terminals do not support escape sequences and cannot render the TUI.
func IsDumbTerminal() bool {
	return os.Getenv("TERM") == "dumb"
}

func maybeSendPamError(err error) tea.Cmd {
	if err == nil {
		return nil
	}

	var errPam pam.Error
	if errors.As(err, &errPam) {
		return sendEvent(pamError{status: errPam, msg: err.Error()})
	}
	return sendEvent(pamError{status: pam.ErrSystem, msg: err.Error()})
}

var debugMessageFormatter = defaultSafeMessageFormatter

func defaultSafeMessageFormatter(msg tea.Msg) string {
	switch msg := msg.(type) {
	case newPasswordCheck:
		return fmt.Sprintf("%#v",
			newPasswordCheck{password: "***********", ctx: msg.ctx})
	case newPasswordCheckResult:
		return fmt.Sprintf("%#v",
			newPasswordCheckResult{password: "***********", msg: msg.msg, ctx: msg.ctx})
	case isAuthenticatedRequested:
		switch item := msg.item.(type) {
		case *authd.IARequest_AuthenticationData_Secret:
			return fmt.Sprintf(`%T{%T{Secret:"***********"}}`, msg, item)
		case *authd.IARequest_AuthenticationData_Wait:
			return fmt.Sprintf("%T{%T{Wait:%q}}", msg, item, item.Wait)
		case *authd.IARequest_AuthenticationData_Skip:
			return fmt.Sprintf("%T{%T{Skip:%q}}", msg, item, item.Skip)
		default:
			return fmt.Sprintf("%T{%T{}}", msg, item)
		}
	case isAuthenticatedRequestedSend:
		return fmt.Sprintf("%T{%s}", msg,
			defaultSafeMessageFormatter(msg.isAuthenticatedRequested))
	case UILayoutReceived:
		return fmt.Sprintf("%T{%#v}", msg, msg.layout)
	case ChangeStage:
		return fmt.Sprintf("%T{Stage:%q}", msg, msg.Stage)
	case StageChanged:
		return fmt.Sprintf("%T{Stage:%q}", msg, msg.Stage)
	case nativeStageChangeRequest:
		return fmt.Sprintf("%T{Stage:%q}", msg, msg.Stage)
	case tea.KeyMsg:
		if msg.Type != tea.KeyRunes {
			return fmt.Sprintf("%T{%s}", msg, msg)
		}
	case nil:
		return ""
	default:
		return fmt.Sprintf("%#v", msg)
	}

	return ""
}

func testMessageFormatter(msg tea.Msg) string {
	switch msg := msg.(type) {
	case newPasswordCheck:
	case newPasswordCheckResult:
	case isAuthenticatedRequested:
		switch item := msg.item.(type) {
		case *authd.IARequest_AuthenticationData_Secret:
			return fmt.Sprintf(`%T{%T{Secret:%q}}`, msg, item, item.Secret)
		default:
			return defaultSafeMessageFormatter(msg)
		}
	case isAuthenticatedRequestedSend:
		return fmt.Sprintf("%T{%s}", msg,
			testMessageFormatter(msg.isAuthenticatedRequested))
	case tea.KeyMsg:
		return fmt.Sprintf("%T{%s}", msg, msg)
	default:
		return defaultSafeMessageFormatter(msg)
	}

	return fmt.Sprintf("%#v", msg)
}

func safeMessageDebug(msg tea.Msg, formatAndArgs ...any) {
	safeMessageDebugWithPrefix("", msg, formatAndArgs...)
}

func safeMessageDebugWithPrefix(prefix string, msg tea.Msg, formatAndArgs ...any) {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return
	}

	m := debugMessageFormatter(msg)
	if m == "" {
		return
	}
	if prefix != "" {
		m = fmt.Sprintf("%s: %s", prefix, m)
	}

	if len(formatAndArgs) == 0 {
		log.Debug(context.Background(), m)
		return
	}

	format, ok := formatAndArgs[0].(string)
	if !ok || !strings.Contains(format, "%") {
		log.Debug(context.Background(), append([]any{m, ", "}, formatAndArgs...)...)
		return
	}

	args := formatAndArgs[1:]
	log.Debugf(context.Background(), "%s, %s", m, fmt.Sprintf(format, args...))
}

func goBackLabel(previousStage proto.Stage) string {
	switch previousStage {
	case proto.Stage_authModeSelection:
		return "go back to select the authentication flow"
	case proto.Stage_brokerSelection:
		return "go back to choose the provider"
	case proto.Stage_challenge:
		return "go back to authentication"
	case proto.Stage_userSelection:
		return "go back to user selection"
	default:
		return ""
	}
}

// labeledField is a label-value pair used by [formatAlignedFields].
type labeledField struct{ label, value string }

// formatAlignedFields pads labels so that all values start at the same column.
//
// NOTE: This is not RTL-friendly and should be adjusted when adding RTL
// language support.
func formatAlignedFields(fields []labeledField) []string {
	maxLen := 0
	for _, f := range fields {
		if n := utf8.RuneCountInString(f.label); n > maxLen {
			maxLen = n
		}
	}

	out := make([]string, 0, len(fields))
	for _, f := range fields {
		padding := strings.Repeat(" ", maxLen-utf8.RuneCountInString(f.label)+1)
		out = append(out, f.label+":"+padding+f.value)
	}
	return out
}
