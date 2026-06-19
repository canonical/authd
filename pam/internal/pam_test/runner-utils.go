package pam_test

import (
	"errors"
	"fmt"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/msteinert/pam/v2"
)

const (
	// RunnerEnvLogFile is the environment variable used by the test client to set the log file.
	RunnerEnvLogFile = "AUTHD_PAM_RUNNER_LOG_FILE"
	// RunnerEnvSupportsConversation is the environment variable used by the test client to set whether it supports PAM conversations.
	RunnerEnvSupportsConversation = "AUTHD_PAM_RUNNER_SUPPORTS_CONVERSATION"
	// RunnerEnvExecModule is the environment variable used by the test client to set the exec module library path.
	RunnerEnvExecModule = "AUTHD_PAM_RUNNER_EXEC_MODULE"
	// RunnerEnvExecChildPath is the environment variable used by the test client to get the PAM exec child client application.
	RunnerEnvExecChildPath = "AUTHD_PAM_RUNNER_EXEC_CHILD_PATH"
	// RunnerEnvTestName is the environment variable used by the test client to set the test name.
	RunnerEnvTestName = "AUTHD_PAM_RUNNER_TEST_NAME"
	// RunnerEnvUser is the environment variable used by the test client to set the PAM user to use.
	RunnerEnvUser = "AUTHD_PAM_RUNNER_USER"
	// RunnerEnvConnectionTimeout is the environment variable used by the test client to set the PAM connection timeout.
	RunnerEnvConnectionTimeout = "AUTHD_PAM_CONNECTION_TIMEOUT"
	// RunnerEnvEnvs is the environment variable used by the test client to set the PAM child environment variables.
	RunnerEnvEnvs = "AUTHD_PAM_RUNNER_ENVS"
	// RunnerEnvService is the environment variable used by the test client to set the PAM service name.
	RunnerEnvService = "AUTHD_PAM_RUNNER_SERVICE"
)

// RunnerAction is the type for Pam Runner actions.
type RunnerAction authd.SessionMode

const (
	// RunnerActionLogin is the runner action for login operation.
	RunnerActionLogin = RunnerAction(authd.SessionMode_LOGIN)
	// RunnerActionPasswd is the runner action for passwd operation.
	RunnerActionPasswd = RunnerAction(authd.SessionMode_CHANGE_PASSWORD)
)

// RunnerActionFromString gets the [RunnerAction] from its string representation.
func RunnerActionFromString(action string) RunnerAction {
	switch action {
	case RunnerActionLogin.String():
		return RunnerActionLogin
	case RunnerActionPasswd.String():
		return RunnerActionPasswd
	default:
		panic("Unknown PAM operation: " + action)
	}
}

func (action RunnerAction) String() string {
	switch action {
	case RunnerActionLogin:
		return "login"
	case RunnerActionPasswd:
		return "passwd"
	default:
		panic(fmt.Sprintf("Invalid PAM operation %d", action))
	}
}

// Result returns the [RunnerResultAction] for the provided [RunnerAction].
func (action RunnerAction) Result() RunnerResultAction {
	switch action {
	case RunnerActionLogin:
		return RunnerResultActionAuthenticate
	case RunnerActionPasswd:
		return RunnerResultActionChangeAuthTok
	default:
		panic(fmt.Sprintf("Invalid PAM operation %d", action))
	}
}

// RunnerResultAction is the type for Pam Runner actions results.
type RunnerResultAction int

const (
	// RunnerResultActionAuthenticate is the string for Authentication action.
	RunnerResultActionAuthenticate RunnerResultAction = iota
	// RunnerResultActionChangeAuthTok is the string for ChangeAuthTok action.
	RunnerResultActionChangeAuthTok
	// RunnerResultActionAcctMgmt is the string for the AcctMgmt action.
	RunnerResultActionAcctMgmt
)

func (result RunnerResultAction) String() string {
	switch result {
	case RunnerResultActionAuthenticate:
		return "PAM Authenticate()"
	case RunnerResultActionChangeAuthTok:
		return "PAM ChangeAuthTok()"
	case RunnerResultActionAcctMgmt:
		return "PAM AcctMgmt()"
	default:
		panic(fmt.Sprintf("Invalid PAM result %d", result))
	}
}

// Message returns the result message for the [PamResultMessage] that the runner writes.
func (result RunnerResultAction) Message(user string) string {
	if user == "" {
		return result.String()
	}
	return fmt.Sprintf("%s\n  User: %q", result, user)
}

// MessageWithError returns the result message for the [PamResultMessage] that the runner writes,
// including the error message or the exit state.
func (result RunnerResultAction) MessageWithError(user string, err error) string {
	resultStr := "success"

	var pamErr pam.Error
	if errors.As(err, &pamErr) {
		pamErr = ErrorTest(pamErr).ToPamError()
		err = fmt.Errorf("PAM exit code: %d\n    %s", pamErr, pamErr)
	}

	if err != nil {
		resultStr = fmt.Sprintf("error: %s", err)
	}

	return fmt.Sprintf("%s\n  Result: %s", result.Message(user), resultStr)
}
