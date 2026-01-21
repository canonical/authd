package main_test

import (
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

const (
	vhsWidth       = "Width"
	vhsHeight      = "Height"
	vhsFontFamily  = "FontFamily"
	vhsFontSize    = "FontSize"
	vhsPadding     = "Padding"
	vhsMargin      = "Margin"
	vhsShell       = "Shell"
	vhsWaitTimeout = "WaitTimeout"
	vhsWaitPattern = "WaitPattern"
	vhsTypingSpeed = "TypingSpeed"

	vhsCommandVariable    = "AUTHD_TEST_TAPE_COMMAND"
	vhsTapeSocketVariable = "AUTHD_TEST_TAPE_SOCKET"
	vhsTapeUserVariable   = "AUTHD_TEST_TAPE_USERNAME"

	vhsCommandFinalAuthWaitVariable         = "AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT"
	vhsCommandFinalChangeAuthokWaitVariable = "AUTHD_TEST_TAPE_COMMAND_PASSWD_FINAL_WAIT"

	vhsQuotedTextMatch = "[\"`](.*)[\"`]"
	vhsClearCommands   = `Hide
Type "clear"
Enter
Wait
Show`

	vhsFrameSeparator       = '─'
	vhsFrameSeparatorLength = 80

	authdWaitDefault = "AUTHD_WAIT_DEFAULT"

	authdSleepDefault                 = "AUTHD_SLEEP_DEFAULT"
	authdSleepLong                    = "AUTHD_SLEEP_LONG"
	authdSleepExampleBrokerMfaWait    = "AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT"
	authdSleepExampleBrokerQrcodeWait = "AUTHD_SLEEP_EXAMPLE_BROKER_QRCODE_WAIT"
	authdSleepQrCodeReselection       = "AUTHD_SLEEP_QRCODE_RESELECTION_WAIT"
)

type tapeSetting struct {
	Key   string
	Value any
}

type tapeData struct {
	Name           string
	Command        string
	OutputDir      string
	OutputFilename string
	Settings       map[string]any
	Env            map[string]string
	Variables      map[string]string

	sanitizeOutputOnce sync.Once
	sanitizedOutput    string
}

type vhsTestType int

const (
	vhsTestTypeCLI = iota
	vhsTestTypeNative
	vhsTestTypeSSH
)

func (tt vhsTestType) tapesPath(t *testing.T) string {
	t.Helper()

	switch tt {
	case vhsTestTypeCLI:
		return "cli"
	case vhsTestTypeNative:
		return "native"
	case vhsTestTypeSSH:
		return "ssh"
	default:
		t.Errorf("Unknown test type %d", tt)
		return ""
	}
}

var (
	defaultSleepValues = map[string]time.Duration{
		authdWaitDefault:  10 * time.Second,
		authdSleepDefault: 100 * time.Millisecond,
		authdSleepLong:    1 * time.Second,
		// Keep these in sync with example broker default wait times
		authdSleepExampleBrokerMfaWait:    4 * time.Second,
		authdSleepExampleBrokerQrcodeWait: 4 * time.Second,
		// Keep this bigger or equal of button's reselectionWaitTime
		authdSleepQrCodeReselection: 700 * time.Millisecond,
	}

	defaultConnectionTimeout = sleepDuration(3*time.Second) / time.Millisecond

	vhsSleepOrWaitRegex = regexp.MustCompile(
		`(?m)\$\{?(AUTHD_(?:SLEEP|WAIT)_[A-Z_]+)\}?(\s?([*/]+)\s?([\d.]+))?(.*)$`)

	vhsEmptyTrailingLinesRegex = regexp.MustCompile(`(?m)\s+\z`)
	vhsUnixTargetRegex         = regexp.MustCompile(fmt.Sprintf(`unix://%s/(\S*)\b`,
		regexp.QuoteMeta(os.TempDir())))
	vhsUserCheckRegex = regexp.MustCompile(`(?m)  (User:|(USER|LOGNAME)=).*$\n*[a-z0-9-"]+$`)
)

var (
	// vhsWaitRegex catches Wait(@timeout)? /Pattern/ commands to re-implement default VHS
	// Wait /Pattern/ command with full context on errors.
	vhsWaitRegex = regexp.MustCompile(`\bWait(\+Line)?(@\S+)?[\t ]+(/(.+)/|(.+))`)
	// vhsWaitLineRegex catches Wait(@timeout) commands to re-implement default Wait command
	// with full context on errors.
	vhsWaitLineRegex = regexp.MustCompile(`\bWait(\+Line)?(@\S+)?[\t ]*\n`)
	// vhsWaitSuffixRegex adds support for Wait+Suffix /Pattern/ command.
	// It allows allows to wait for a terminal output that ends with a content
	// that matches Pattern.
	vhsWaitSuffixRegex = regexp.MustCompile(`\bWait\+Suffix(@\S+)?[\t ]+(/(.*)/|(.*))`)
	// vhsWaitPromptRegex adds support for Wait+Prompt /Pattern/ command.
	// It allows to wait for a terminal output that ends with a prompt message in the form:
	// Pattern:
	// > .
	vhsWaitPromptRegex = regexp.MustCompile(`\bWait\+Prompt(@\S+)?[\t ]+(/(.*)/|(.*))`)
	// vhsWaitCLIPromptRegex adds support for Wait+CLIPrompt /Pattern1/ /Pattern2/ command.
	// It allows to wait for CLI a terminal output that ends with a prompt message in the form:
	// Pattern:
	// >
	// ([ Button text ]|error message)
	// (error message)?
	vhsWaitCLIPromptRegex = regexp.MustCompile(
		`\bWait\+CLIPrompt(@\S+)?[\t ]+/([^/]+)/([\t ]+/([^/]+)/)?([\t ]+/(.+)/)?`)
	// vhsWaitNthRegex adds support for Wait+Nth(N) /Pattern/ command, where N is the
	// number of values of the same content we want to match.
	// It allows to wait for the same content being repeated N times in the terminal.
	vhsWaitNthRegex = regexp.MustCompile(`\bWait\+Nth\((\d+)\)(@\S+)?[\t ]+(/(.*)/|(.*))`)

	// vhsTypeAndWaitUsername adds support for typing the username, waiting for it being printed.
	vhsTypeAndWaitUsername = regexp.MustCompile(`(.*)\bTypeUsername[\t ]+` + vhsQuotedTextMatch)
	// vhsTypeAndWaitVisiblePrompt adds support for typing some text in an "Echo On" prompt,
	// waiting for it being printed in the terminal.
	vhsTypeAndWaitVisiblePrompt = regexp.MustCompile(`(.*)\bTypeInPrompt(\+(Shell|PAM|CLI|SH))?[\t ]+` + vhsQuotedTextMatch)
	// vhsTypeAndWaitCLIPassword adds support for typing the CLI password, waiting for the expected output.
	vhsTypeAndWaitCLIPassword = regexp.MustCompile(`(.*)\bTypeCLIPassword(())?[\t ]+` + vhsQuotedTextMatch)

	// vhsClearTape clears the tape by clearing the terminal.
	vhsClearTape = regexp.MustCompile(`\bClearTerminal\b`)
)

func init() {
	if v := os.Getenv("AUTHD_TESTS_VHS_WAIT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			panic(err)
		}
		defaultSleepValues[authdWaitDefault] = d
	}
}

func newTapeData(tapeName string, outputDir string, settings ...tapeSetting) *tapeData {
	m := map[string]any{
		vhsWidth:  800,
		vhsHeight: 500,
		// TODO: Ideally, we should use Ubuntu Mono. However, the github runner is still on Jammy, which does not have it.
		// We should update this to use Ubuntu Mono once the runner is updated.
		vhsFontFamily:  "Monospace",
		vhsFontSize:    13,
		vhsPadding:     0,
		vhsMargin:      0,
		vhsShell:       "bash",
		vhsWaitTimeout: defaultSleepValues[authdWaitDefault],
		vhsTypingSpeed: 5 * time.Millisecond,
	}
	for _, s := range settings {
		m[s.Key] = s.Value
	}
	return &tapeData{
		Name:           tapeName,
		OutputDir:      outputDir,
		OutputFilename: tapeName + ".txt",
		Settings:       m,
		Env:            make(map[string]string),
	}
}

type clientOptions struct {
	PamUser        string
	PamEnv         []string
	PamServiceName string
	PamTimeout     string
	Term           string
	SessionType    string
}

func (td *tapeData) AddClientOptions(t *testing.T, opts clientOptions) {
	t.Helper()

	logFile := prepareFileLogging(t, "authd-pam-test-client.log")
	td.Env[pam_test.RunnerEnvLogFile] = logFile
	td.Env[pam_test.RunnerEnvTestName] = t.Name()

	if opts.PamUser != "" {
		td.Env[pam_test.RunnerEnvUser] = opts.PamUser
	}
	if opts.PamEnv != nil {
		td.Env[pam_test.RunnerEnvEnvs] = strings.Join(opts.PamEnv, ";")
	}
	if opts.PamServiceName != "" {
		td.Env[pam_test.RunnerEnvService] = opts.PamServiceName
	}
	if opts.PamTimeout != "" {
		td.Env[pam_test.RunnerEnvConnectionTimeout] = opts.PamTimeout
	}
	if _, ok := td.Env[pam_test.RunnerEnvConnectionTimeout]; !ok {
		td.Env[pam_test.RunnerEnvConnectionTimeout] = fmt.Sprintf("%d", defaultConnectionTimeout)
	}
	if _, ok := td.Env["SHELL"]; !ok {
		shell, ok := td.Settings[vhsShell].(string)
		require.True(t, ok, "Setup: %s is of invalid type: %T", shell, td.Settings[vhsShell])
		shellPath, err := exec.LookPath(shell)
		require.NoError(t, err, "Shell %s not found: %s", shell, err)
		td.Env["SHELL"] = shellPath
	}
	if opts.Term != "" {
		td.Env["AUTHD_PAM_CLI_TERM"] = opts.Term
	}
	if opts.SessionType != "" {
		td.Env["XDG_SESSION_TYPE"] = opts.SessionType
	}
}

func (td *tapeData) RunVHS(t *testing.T, testType vhsTestType, cliEnv []string) {
	t.Helper()

	cmd := exec.Command("env", "vhs")

	var outBuf testutils.SyncBuffer
	// Write stdout/stderr both to our stdout/stderr and to the buffer
	cmd.Stdout = io.MultiWriter(t.Output(), &outBuf)
	cmd.Stderr = io.MultiWriter(t.Output(), &outBuf)

	cmd.Env = append(testutils.AppendCovEnv(cmd.Env), cliEnv...)
	cmd.Dir = td.OutputDir

	cmd.Env = append(cmd.Env,
		// If vhs is installed with "go install", we need to add GOPATH to PATH.
		pathEnvWithGoBin(t),
		// vhs uses rod, which downloads chromium to $HOME/.cache/rod,
		// so $HOME needs to be set to avoid that it downloads it every time.
		// TODO: Set XDG_CACHE_HOME instead once https://github.com/go-rod/rod/pull/1213 was merged
		"HOME="+os.Getenv("HOME"),
		// Avoid VHS printing "Host your GIF on vhs.charm.sh: vhs publish <file>.gif"
		"VHS_PUBLISH=false",
	)

	// Force VHS to use color because it makes it a lot easier to find the error
	// message in the VHS output. The only case where we don't want color is when
	// building a Debian package, because when viewing the logs of a launchpad build
	// in the browser, ANSI colors are not rendered.
	if !testutils.IsDebianPackageBuild() {
		cmd.Env = append(cmd.Env, "CLICOLOR_FORCE=1")
	}

	u, err := user.Current()
	require.NoError(t, err, "Setup: getting current user")
	if u.Name == "root" || os.Getenv("SCHROOT_CHROOT_NAME") != "" {
		cmd.Env = append(cmd.Env, "VHS_NO_SANDBOX=1")
	}

	// Set a temporary HOME directory to avoid interference with the user's home directory.
	td.Env["HOME"] = t.TempDir()

	// Move some of the environment specific-variables from the tape to the launched process
	if e, ok := td.Env[pam_test.RunnerEnvLogFile]; ok {
		delete(td.Env, pam_test.RunnerEnvLogFile)
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", pam_test.RunnerEnvLogFile, e))
	}

	var raceLog string
	if testutils.IsRace() {
		raceLog = filepath.Join(t.TempDir(), "gorace.log")
		cmd.Env = append(cmd.Env, fmt.Sprintf("GORACE=log_path=%s exitcode=0", raceLog))
	}

	if testutils.IsAsan() {
		if asanOptions := os.Getenv("ASAN_OPTIONS"); asanOptions != "" {
			cmd.Env = append(cmd.Env, fmt.Sprintf("ASAN_OPTIONS=%s", asanOptions))
		}
		if lsanOptions := os.Getenv("LSAN_OPTIONS"); lsanOptions != "" {
			cmd.Env = append(cmd.Env, fmt.Sprintf("LSAN_OPTIONS=%s", lsanOptions))
		}
	}

	tapePath := td.PrepareTape(t, testType)
	testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, tapePath, filepath.Join(td.OutputDir, td.OutputFilename))
	cmd.Args = append(cmd.Args, tapePath)

	err = testlog.RunWithTiming(t, fmt.Sprintf("VHS tape %q", td.Name), cmd, testlog.DoNotSetStdoutAndStderr())
	if raceLog != "" {
		checkDataRaces(t, raceLog)
	}

	sanitizedOutputFilename := strings.TrimSuffix(td.OutputFilename, ".txt") + ".sanitized.txt"
	testutils.MaybeSaveBytesAsArtifactOnCleanup(t, []byte(td.SanitizedOutput(t)), sanitizedOutputFilename)

	isSSHError := func() bool {
		out := outBuf.String()
		const sshConnectionResetByPeer = "Connection reset by peer"
		const sshConnectionClosed = "Connection closed by"
		return strings.Contains(out, sshConnectionResetByPeer) ||
			strings.Contains(out, sshConnectionClosed)
	}
	if err != nil && testType == vhsTestTypeSSH && isSSHError() {
		t.Logf("SSH Connection failed on tape %q: %v", td.Name, err)
		// We've sometimes (but rarely) seen SSH connection errors which were resolved on retry, so we retry once.
		// If it fails again, something might actually be broken.
		//nolint:gosec // G204 it's a test and we explicitly set the parameters before.
		newCmd := exec.Command(cmd.Args[0], cmd.Args[1:]...)
		newCmd.Dir = cmd.Dir
		newCmd.Env = slices.Clone(cmd.Env)
		err = testlog.RunWithTiming(t, fmt.Sprintf("VHS tape %q (second try)", td.Name), newCmd, testlog.DoNotSetStdoutAndStderr())
	}
	require.NoError(t, err, "Failed to run tape %q, see vhs output above", td.Name)
}

func (td *tapeData) String() string {
	str := fmt.Sprintf("Output %q\n", td.OutputFilename)
	for s, v := range td.Settings {
		switch vv := v.(type) {
		case time.Duration:
			v = fmt.Sprintf("%dms", sleepDuration(vv).Milliseconds())
		case string:
			if s == vhsWaitPattern {
				// VHS wait pattern can be a regex, so don't quote it by default.
				break
			}
			v = fmt.Sprintf("%q", vv)
		}
		str += fmt.Sprintf(`Set %s %v`+"\n", s, v)
	}
	for s, v := range td.Env {
		str += fmt.Sprintf(`Env %s %q`+"\n", s, v)
	}
	return str
}

func checkDataRaces(t *testing.T, raceLog string) {
	t.Helper()

	if !testutils.IsRace() {
		return
	}

	var raceLogs []string
	err := filepath.Walk(filepath.Dir(raceLog),
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !strings.HasPrefix(info.Name(), filepath.Base(raceLog)) {
				return nil
			}
			t.Logf("Found data race %s", info.Name())
			raceLogs = append(raceLogs, filepath.Join(filepath.Dir(raceLog), info.Name()))
			return nil
		})

	require.NoError(t, err, "TearDown: Check for races")
	testutils.MaybeSaveFilesAsArtifactsOnCleanup(t, raceLogs...)
	for _, raceLog := range raceLogs {
		checkDataRace(t, raceLog)
	}
}

func checkDataRace(t *testing.T, raceLog string) {
	t.Helper()

	content, err := os.ReadFile(raceLog)
	require.NoError(t, err, "TearDown: Error reading race log %q", raceLog)

	out := string(content)
	if strings.TrimSpace(out) == "" {
		return
	}

	t.Fatalf("Got a GO Race on vhs child:\n%s", out)
}

func (td *tapeData) SanitizedOutput(t *testing.T) string {
	t.Helper()

	td.sanitizeOutputOnce.Do(func() {
		td.sanitizedOutput = td.sanitizeOutput(t)
	})

	return td.sanitizedOutput
}

func (td *tapeData) sanitizeOutput(t *testing.T) string {
	t.Helper()

	outPath := filepath.Join(td.OutputDir, td.OutputFilename)
	out, err := os.ReadFile(outPath)
	require.NoError(t, err, "Could not read output file of tape %q (%s)", td.Name, outPath)
	s := string(out)

	// We need to format the output a little bit, since the txt file can have some noise at the beginning.
	command := "> " + td.Command
	maxCommandLen := 0
	splitTmp := strings.Split(s, "\n")
	for _, str := range splitTmp {
		maxCommandLen = max(maxCommandLen, utf8.RuneCountInString(str))
	}
	if len(command) > maxCommandLen {
		command = command[:maxCommandLen]
	}
	for i, str := range splitTmp {
		if strings.Contains(str, command) {
			s = strings.Join(splitTmp[i:], "\n")
			break
		}
	}

	s = permissions.Z_ForTests_IdempotentPermissionError(s)

	// Remove consecutive equal frames from vhs tapes.
	framesSeparator := strings.Repeat(string(vhsFrameSeparator), vhsFrameSeparatorLength)
	frames := slices.Compact(strings.Split(s, framesSeparator))
	// Drop all the empty lines before each page separator, to remove the clutter.
	for i, f := range frames {
		frames[i] = vhsEmptyTrailingLinesRegex.ReplaceAllString(f, "\n")
	}
	s = strings.Join(frames, framesSeparator)

	// Drop all the socket references.
	s = vhsUnixTargetRegex.ReplaceAllLiteralString(s, "unix:///authd/test_socket.sock")

	// Username may be split in multiple lines, so fix this not to break further checks.
	s = vhsUserCheckRegex.ReplaceAllStringFunc(s, func(s string) string {
		return strings.ReplaceAll(s, "\n", "")
	})

	return s
}

func (td *tapeData) PrepareTape(t *testing.T, testType vhsTestType) string {
	t.Helper()

	currentDir, err := os.Getwd()
	require.NoError(t, err, "Setup: Could not get current directory for the tests")

	tape, err := os.ReadFile(filepath.Join(
		currentDir, "testdata", "tapes", testType.tapesPath(t), td.Name+".tape"))
	require.NoError(t, err, "Setup: read tape file %s", td.Name)

	tapeString := evaluateTapeVariables(t, string(tape), td, testType)

	tapeLines := strings.Split(tapeString, "\n")
	var lastCommand string
	for i := len(tapeLines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(tapeLines[i])
		if len(s) == 0 || s[0] == '#' {
			continue
		}
		if idx := strings.Index(s, "#"); idx > 0 {
			s = strings.TrimSpace(s[:idx])
		}
		lastCommand = s
		break
	}
	require.Equal(t, "Show", lastCommand,
		"Setup: Tape %q must terminate with a `Show` command", td.Name)

	tape = []byte(strings.Join([]string{
		td.String(),
		tapeString,
		// Note that not sleeping enough may lead to a system hang, so keep it
		// in mind if tests are failing in CI with with error code 143.
		fmt.Sprintf("Sleep %dms",
			sleepDuration(defaultSleepValues[authdSleepDefault]).Milliseconds()),
	}, "\n"))

	tapePath := filepath.Join(td.OutputDir, td.Name+".tape")
	err = os.WriteFile(tapePath, tape, 0600)
	require.NoError(t, err, "Setup: write tape file")

	return tapePath
}

func evaluateTapeVariables(t *testing.T, tapeString string, td *tapeData, testType vhsTestType) string {
	t.Helper()

	for _, m := range vhsSleepOrWaitRegex.FindAllStringSubmatch(tapeString, -1) {
		fullMatch, sleepKind, op, arg, rest := m[0], m[1], m[3], m[4], m[5]
		sleep, ok := defaultSleepValues[sleepKind]
		require.True(t, ok, "Setup: unknown sleep kind: %q", sleepKind)

		// We don't need to support math that is complex enough to use proper parsers as go.ast
		if arg != "" {
			parsedArg, err := strconv.ParseFloat(arg, 32)
			require.NoError(t, err, "Setup: Cannot parse expression %q: %q is not a float", fullMatch, arg)

			switch op {
			case "*":
				sleep = time.Duration(math.Round(float64(sleep) * parsedArg))
			case "/":
				require.NotZero(t, parsedArg, "Setup: Division by zero")
				sleep = time.Duration(math.Round(float64(sleep) / parsedArg))
			default:
				require.Empty(t, op, "Setup: Unhandled operator %q", op)
			}
		}

		replaceRegex := regexp.MustCompile(fmt.Sprintf(`(?m)%s$`, regexp.QuoteMeta(fullMatch)))
		tapeString = replaceRegex.ReplaceAllString(tapeString,
			fmt.Sprintf("%dms%s", sleepDuration(sleep).Milliseconds(), rest))
	}

	if td.Command == "" {
		require.NotContains(t, tapeString, fmt.Sprintf("${%s}", vhsCommandVariable),
			"Setup: Tape contains %q but it's not defined", vhsCommandVariable)
	}

	variables := maps.Clone(td.Variables)
	if variables == nil {
		variables = make(map[string]string)
	}

	if td.Command != "" {
		variables[vhsCommandVariable] = td.Command
	}

	addOptionalVariable := func(name, value string) {
		if _, ok := variables[name]; ok {
			return
		}
		if !strings.Contains(tapeString, fmt.Sprintf("${%s}", name)) {
			return
		}
		variables[name] = value
	}

	addOptionalVariable(vhsCommandFinalAuthWaitVariable,
		finalWaitCommands(testType, authd.SessionMode_LOGIN))
	addOptionalVariable(vhsCommandFinalChangeAuthokWaitVariable,
		finalWaitCommands(testType, authd.SessionMode_CHANGE_PASSWORD))

	for k, v := range variables {
		variable := fmt.Sprintf("${%s}", k)
		require.Contains(t, tapeString, variable,
			"Setup: Tape does not contain %q\n%s", variable, tapeString)
		tapeString = strings.ReplaceAll(tapeString, variable, v)
	}

	multiLineValueRegex := func(value string) string {
		// If the value is very long it may be split in multiple lines, so that
		// we need to use a regex to match it.
		const maxLength = 80
		if len(value) <= maxLength {
			value = regexp.QuoteMeta(value)
			// See https://github.com/charmbracelet/vhs/issues/592
			return strings.ReplaceAll(value, "/", `\x{2F}`)
		}
		valueRegex := regexp.QuoteMeta(value[:maxLength])
		for i := maxLength; i < len(value); i++ {
			valueRegex += regexp.QuoteMeta(string(value[i])) + `\n?`
		}
		// See https://github.com/charmbracelet/vhs/issues/592
		return strings.ReplaceAll(valueRegex, "/", `\x{2F}`)
	}

	for _, m := range vhsTypeAndWaitUsername.FindAllStringSubmatch(tapeString, -1) {
		fullMatch, prefix, username := m[0], m[1], m[2]
		commands := []string{
			`Wait /Username:[^\n]*\n/`,
			fmt.Sprintf("Type `%s`", username),
			fmt.Sprintf(`Wait /Username: %s\n/`, multiLineValueRegex(username)),
		}
		tapeString = strings.ReplaceAll(tapeString, fullMatch,
			prefix+strings.Join(commands, "\n"+prefix))
	}

	waitForPromptText := func(matches []string, style pam.Style) {
		fullMatch, prefix, waitCtx, promptValue := matches[0], matches[1], matches[3], matches[4]
		visibleValue := promptValue
		if style == pam.PromptEchoOff {
			visibleValue = strings.Repeat("*", len(promptValue))
		}
		initialWait := `Wait+Screen /\n>[ \t]*\n/`
		prePromptRegex := `:\n`
		psChar := ">"
		switch waitCtx {
		case "Shell":
			initialWait = "Wait"
			prePromptRegex = `(^|\n)`
		case "SH":
			psChar = `\$`
			initialWait = fmt.Sprintf("Wait /%s/", psChar)
			prePromptRegex = `(^|\n)`
		}
		prePromptRegex += psChar
		commands := []string{
			initialWait,
			fmt.Sprintf("Type `%s`", promptValue),
			fmt.Sprintf(`Wait+Suffix /%s %s(\n[^>].+)*/`, prePromptRegex, multiLineValueRegex(visibleValue)),
		}
		tapeString = strings.ReplaceAll(tapeString, fullMatch,
			prefix+strings.Join(commands, "\n"+prefix))
	}

	for _, m := range vhsTypeAndWaitCLIPassword.FindAllStringSubmatch(tapeString, -1) {
		waitForPromptText(m, pam.PromptEchoOff)
	}
	for _, m := range vhsTypeAndWaitVisiblePrompt.FindAllStringSubmatch(tapeString, -1) {
		waitForPromptText(m, pam.PromptEchoOn)
	}

	waitPattern := `/(^|\n)>/`
	if wp, ok := td.Settings[vhsWaitPattern]; ok {
		waitPattern, ok = wp.(string)
		require.True(t, ok, "Setup: %s must be a string", vhsWaitPattern)
	}

	tapeString = vhsWaitRegex.ReplaceAllString(tapeString,
		`Wait+Suffix$2 /(^|[\n]+)[^\n]*$4$5[^\n]*/`)
	tapeString = vhsWaitLineRegex.ReplaceAllString(tapeString,
		fmt.Sprintf("Wait+Suffix$2 %s\n", waitPattern))
	tapeString = vhsWaitCLIPromptRegex.ReplaceAllString(tapeString,
		`Wait+Suffix$1 /$2:\n>[\n]+[ ]*$4[\n]*[\n]+$6/`)
	tapeString = vhsWaitPromptRegex.ReplaceAllString(tapeString,
		`Wait+Suffix$1 /$3$4:\n>/`)
	tapeString = vhsWaitSuffixRegex.ReplaceAllString(tapeString,
		`Wait+Screen$1 /$3$4[\n]*\z/`)
	tapeString = vhsWaitNthRegex.ReplaceAllString(tapeString,
		`Wait+Screen$2 /($4$5(.|\n)+){$1}/`)
	tapeString = vhsClearTape.ReplaceAllLiteralString(tapeString, vhsClearCommands)

	return tapeString
}

func finalWaitCommands(testType vhsTestType, sessionMode authd.SessionMode) string {
	finalWaitDuration := sleepDuration(defaultSleepValues[authdWaitDefault])
	var firstResult, secondResult pam_test.RunnerResultAction
	switch testType {
	case vhsTestTypeSSH:
		finalWaitDuration = sshDefaultFinalWaitTimeout
		firstResult = pam_test.RunnerResultActionAuthenticate
		secondResult = pam_test.RunnerResultActionAcctMgmt

	default:
		firstResult = pam_test.RunnerResultActionAuthenticate
		if sessionMode == authd.SessionMode_CHANGE_PASSWORD {
			firstResult = pam_test.RunnerResultActionChangeAuthTok
		}
		secondResult = pam_test.RunnerResultActionAcctMgmt
	}

	return fmt.Sprintf(`Wait+Screen /%s[^\n]*/
Wait+Screen /%s[^\n]*/
Wait@%dms`,
		regexp.QuoteMeta(firstResult.String()),
		regexp.QuoteMeta(secondResult.String()),
		sleepDuration(finalWaitDuration).Milliseconds(),
	)
}

func requireRunnerResultForUser(t *testing.T, sessionMode authd.SessionMode, user, goldenContent string) {
	t.Helper()

	// Only check the last 50 lines of the golden file, because that's where
	// the result is printed, while printing the full output on failure is too much.
	goldenLines := strings.Split(goldenContent, "\n")
	goldenContent = strings.Join(goldenLines[max(0, len(goldenLines)-50):], "\n")

	// authd uses lowercase usernames
	user = strings.ToLower(user)

	require.Contains(t, goldenContent, pam_test.RunnerAction(sessionMode).Result().Message(user),
		"Golden file does not include required value, consider increasing the terminal size:\n%s",
		goldenContent)
	require.Contains(t, goldenContent, pam_test.RunnerResultActionAcctMgmt.Message(user),
		"Golden file does not include required value, consider increasing the terminal size:\n%s",
		goldenContent)
}

func requireRunnerResult(t *testing.T, sessionMode authd.SessionMode, goldenContent string) {
	t.Helper()

	requireRunnerResultForUser(t, sessionMode, "", goldenContent)
}

func vhsTestUserNameFull(t *testing.T, userPrefix string, namePrefix string) string {
	t.Helper()

	require.NotEmpty(t, userPrefix, "Setup: user prefix needs to be set", t.Name())
	if userPrefix[len(userPrefix)-1] != '-' {
		userPrefix += "-"
	}
	if namePrefix != "" && namePrefix[len(namePrefix)-1] != '-' {
		namePrefix += "-"
	}
	return userPrefix + namePrefix + strings.ReplaceAll(
		strings.ToLower(filepath.Base(t.Name())), "_", "-")
}

func vhsTestUserName(t *testing.T, prefix string) string {
	t.Helper()

	require.NotEmpty(t, prefix, "Setup: user prefix needs to be set", t.Name())
	return vhsTestUserNameFull(t, examplebroker.UserIntegrationPrefix, prefix)
}
