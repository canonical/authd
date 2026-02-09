package adapter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/msteinert/pam/v2"
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

// GetPamTTY returns the file to that is used by PAM tty or stdin.
func GetPamTTY(mTx pam.ModuleTransaction) (tty *os.File, cleanup func()) {
	var err error
	defer func() {
		if err != nil {
			log.Warningf(context.TODO(), "Failed to open PAM TTY: %s", err)
		}
		if tty == nil {
			tty = os.Stdin
		}
		if cleanup == nil {
			cleanup = func() {}
		}
	}()

	var pamTTY string
	pamTTY, err = mTx.GetItem(pam.Tty)
	if err != nil {
		return nil, nil
	}

	if pamTTY == "" {
		return nil, nil
	}

	tty, err = os.OpenFile(pamTTY, os.O_RDWR, 0600)
	if err != nil {
		return nil, nil
	}
	cleanup = func() { tty.Close() }

	// We check the fd could be passed to x/term to decide if we should fallback to stdin
	if tty.Fd() > math.MaxInt {
		err = fmt.Errorf("unexpected large PAM TTY fd: %d", tty.Fd())
		return nil, cleanup
	}

	return tty, cleanup
}

// IsTerminalTTY returns whether the [pam.Tty] or the [os.Stdin] is a terminal TTY.
func IsTerminalTTY(mTx pam.ModuleTransaction) bool {
	isTerminalTTYOnce.Do(func() {
		tty, cleanup := GetPamTTY(mTx)
		defer cleanup()
		isTerminalTTYValue = term.IsTerminal(tty.Fd())
	})
	return isTerminalTTYValue
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
		return "go back to select the authentication method"
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
