package pam_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Action represents a PAM action to perform.
type Action int

const (
	// Account is the account.
	Account Action = iota + 1
	// Auth is the auth.
	Auth
	// Password is the password.
	Password
	// Session is the session.
	Session
	// Include allows to include system services.
	Include
)

// String is the method to stringify an to their PAM config file representation.
func (a Action) String() string {
	switch a {
	case Account:
		return "account"
	case Auth:
		return "auth"
	case Password:
		return "password"
	case Session:
		return "session"
	case Include:
		return "@include"
	default:
		return ""
	}
}

// Actions is a map with all the available Actions by their name.
var Actions = map[string]Action{
	Account.String():  Account,
	Auth.String():     Auth,
	Password.String(): Password,
	Session.String():  Session,
	Include.String():  Include,
}

// Control represents how a PAM module should controlled in PAM service file.
type Control int

const (
	// Required implies that the module is required.
	Required Control = iota + 1
	// Requisite implies that the module is requisite.
	Requisite
	// Sufficient implies that the module is sufficient.
	Sufficient
	// SufficientRequisite implies that the module is sufficient but we'll die on any error.
	SufficientRequisite
	// Optional implies that the module is optional.
	Optional

	lastControl
)

var (
	customControls   map[Control]string
	customControlsMu sync.RWMutex
)

// NewControl allows to create a new custom control for the given string.
func NewControl(controlValue string) Control {
	customControlsMu.Lock()
	defer customControlsMu.Unlock()

	if customControls == nil {
		customControls = make(map[Control]string)
	}

	for control, value := range customControls {
		if value == controlValue {
			return control
		}
	}

	control := Control(int(lastControl) + len(customControls))
	customControls[control] = controlValue
	return control
}

// String is the method to stringify a control to their PAM config file representation.
func (c Control) String() string {
	switch c {
	case Required:
		return "required"
	case Requisite:
		return "requisite"
	case Sufficient:
		return "sufficient"
	case Optional:
		return "optional"
	case SufficientRequisite:
		return "[success=done new_authtok_reqd=done ignore=ignore default=die]"
	default:
		customControlsMu.RLock()
		defer customControlsMu.RUnlock()
		return customControls[c]
	}
}

// ServiceLine is the representation of a PAM module service file line.
type ServiceLine struct {
	Action  Action
	Control Control
	Module  string
	Args    []string
}

// CreateService creates a service file and returns its path.
func CreateService(path string, serviceName string, services []ServiceLine) (string, error) {
	serviceFile := filepath.Join(path, strings.ToLower(serviceName))
	contents := make([]string, 0, len(services))

	for _, s := range services {
		contents = append(contents, strings.TrimRight(strings.Join([]string{
			s.Action.String(), s.Control.String(), s.Module, strings.Join(escapeArgs(s.Args), " "),
		}, "\t"), "\t"))
	}

	if err := os.WriteFile(serviceFile,
		[]byte(strings.Join(contents, "\n")), 0600); err != nil {
		return "", fmt.Errorf("can't create service file %v: %w", serviceFile, err)
	}

	return serviceFile, nil
}

func escapeArgs(args []string) []string {
	args = slices.Clone(args)
	for idx, arg := range args {
		if !strings.Contains(arg, " ") && !strings.Contains(arg, "[") && !strings.Contains(arg, "]") {
			continue
		}
		args[idx] = fmt.Sprintf("[%s]", strings.ReplaceAll(arg, "]", "\\]"))
	}
	return args
}

// FallBackModule is a type to represent the module that should be used as fallback.
type FallBackModule int

const (
	// NoFallback add no fallback module.
	NoFallback FallBackModule = iota + 1
	// Permit uses a module that always permits.
	Permit
	// Deny uses a module that always denys.
	Deny
	// Ignore uses a module that we use as ignore return value.
	Ignore
)

func (a FallBackModule) String() string {
	switch a {
	case Permit:
		return "pam_permit.so"
	case Deny:
		return "pam_deny.so"
	case Ignore:
		// We use incomplete error for this, keep this in sync with [pam_test.ErrIgnore].
		return "pam_debug.so auth=incomplete cred=incomplete acct=incomplete " +
			"prechauthtok=incomplete chauthtok=incomplete " +
			"open_session=incomplete close_session=incomplete"
	default:
		return ""
	}
}
