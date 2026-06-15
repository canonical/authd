// TiCS: disabled // This is a test helper.

//go:build withpamrunner

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/msteinert/pam/v2"
	"golang.org/x/term"
)

// Simulating pam on the CLI for manual testing.
func main() {
	logFile := os.Getenv(pam_test.RunnerEnvLogFile)
	outputFile := os.Getenv(pam_test.RunnerEnvOutputFile)
	supportsConversation := os.Getenv(pam_test.RunnerEnvSupportsConversation) != ""
	execModule := os.Getenv(pam_test.RunnerEnvExecModule)
	execChildPath := os.Getenv(pam_test.RunnerEnvExecChildPath)
	testName := os.Getenv(pam_test.RunnerEnvTestName)
	pamUser := os.Getenv(pam_test.RunnerEnvUser)
	pamEnvs := os.Getenv(pam_test.RunnerEnvEnvs)
	pamTty := os.Getenv(pam_test.RunnerEnvTty)
	pamService := os.Getenv(pam_test.RunnerEnvService)
	timeoutDuration := os.Getenv(pam_test.RunnerEnvConnectionTimeout)

	tmpDir, err := os.MkdirTemp(os.TempDir(), "pam-cli-tester-")
	if err != nil {
		log.Fatalf("Can't create temporary dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if _, err := os.Stat(execModule); err != nil {
		execModule, err = buildExecModule(tmpDir)
		if err != nil {
			log.Fatalf("Module build failed: %v", err)
		}
	}

	if _, err := os.Stat(execChildPath); err != nil {
		execChildPath, err = buildExecChild(tmpDir)
		if err != nil {
			log.Fatalf("Client build failed: %v", err)
		}
	}

	defaultArgs := []string{
		execChildPath,
		"debug=true",
		"connection_timeout=" + timeoutDuration,
	}
	if logFile != "" {
		defaultArgs = append(defaultArgs, "logfile="+logFile)
		defaultArgs = append(defaultArgs, "--exec-debug", "--exec-log", logFile)
	}

	if outputFile == "" {
		outputFile = os.Stdout.Name()
	}

	output, err := os.OpenFile(outputFile, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if err != nil {
		log.Fatalf("Can't open output file %s: %v", outputFile, err)
	}
	defer output.Close()

	for _, env := range []string{
		"GOCOVERDIR",
		"GORACE",
		"ASAN_OPTIONS",
		"LSAN_OPTIONS",
	} {
		if _, ok := os.LookupEnv(env); !ok {
			continue
		}
		defaultArgs = append(defaultArgs, "--exec-env", env)
	}

	if len(os.Args) < 2 {
		log.Fatalf("Not enough arguments")
	}

	action, args := os.Args[1], os.Args[2:]
	args = append(defaultArgs, args...)

	if pamService == "" {
		pamService = "authd-pam-runner-service"
	}
	serviceFile, err := pam_test.CreateService(tmpDir, pamService, []pam_test.ServiceLine{
		{Action: pam_test.Auth, Control: pam_test.SufficientRequisite, Module: execModule, Args: args},
		{Action: pam_test.Auth, Control: pam_test.Sufficient, Module: pam_test.Ignore.String()},
		{Action: pam_test.Account, Control: pam_test.SufficientRequisite, Module: execModule, Args: args},
		{Action: pam_test.Account, Control: pam_test.Sufficient, Module: pam_test.Ignore.String()},
		{Action: pam_test.Password, Control: pam_test.SufficientRequisite, Module: execModule, Args: args},
		{Action: pam_test.Password, Control: pam_test.Sufficient, Module: pam_test.Ignore.String()},
	})
	if err != nil {
		log.Fatalf("Can't create service file %s: %v", serviceFile, err)
	}

	reader, readerFile, pamTTYFile, cleanup := openReader(pamTty)
	defer cleanup()

	writer := io.Writer(output)
	if pamTTYFile != nil && !sameFile(output, pamTTYFile) {
		writer = io.MultiWriter(output, pamTTYFile)
	}

	conversationHandler := pam.ConversationFunc(func(s1 pam.Style, s2 string) (string, error) {
		return noConversationHandler(writer, s1, s2)
	})
	if supportsConversation {
		conversationHandler = pam.ConversationFunc(func(s1 pam.Style, s2 string) (string, error) {
			return simpleConversationHandler(writer, reader, readerFile, s1, s2)
		})
	}

	tx, err := pam.StartConfDir(filepath.Base(serviceFile), pamUser,
		conversationHandler, filepath.Dir(serviceFile))
	if err != nil {
		log.Fatalf("Impossible to start transaction %v: %v", execChildPath, err)
	}
	defer tx.End()

	err = tx.PutEnv("AUTHD_PAM_CLI_TEST_NAME=" + testName)
	if err != nil {
		log.Fatalf("Impossible to set environment: %v", err)
	}

	if pamTty != "" {
		if err := tx.SetItem(pam.Tty, pamTty); err != nil {
			log.Fatalf("Impossible to set PAM_TTY environment: %v", err)
		}
	}

	if pamEnvs != "" {
		for _, env := range strings.Split(pamEnvs, ";") {
			err = tx.PutEnv(env)
			if err != nil {
				log.Fatalf("Impossible to set environment: %v", err)
			}
		}
	}

	var pamFunc func(pam.Flags) error
	runnerAction := pam_test.RunnerActionFromString(action)
	switch runnerAction {
	case pam_test.RunnerActionLogin:
		pamFunc = tx.Authenticate
	case pam_test.RunnerActionPasswd:
		pamFunc = tx.ChangeAuthTok
	default:
		panic("Unknown PAM operation: " + action)
	}

	pamFlags := pam.Silent
	pamRes := pamFunc(pamFlags)
	user, _ := tx.GetItem(pam.User)

	printPamResult(writer, runnerAction.Result(), user, pamRes)
}

func noConversationHandler(output io.Writer, style pam.Style, msg string) (string, error) {
	switch style {
	case pam.TextInfo:
		fmt.Fprintf(output, "PAM Info Message: %s\n", msg)
	case pam.ErrorMsg:
		fmt.Fprintf(output, "PAM Error Message: %s\n", msg)
	default:
		return "", fmt.Errorf("PAM style %d not implemented", style)
	}
	return "", nil
}

func simpleConversationHandler(output io.Writer, reader *bufio.Reader, readerFile *os.File, style pam.Style, msg string) (string, error) {
	switch style {
	case pam.TextInfo:
		fmt.Fprintln(output, msg)
	case pam.ErrorMsg:
		return noConversationHandler(output, style, msg)
	case pam.PromptEchoOn:
		fmt.Fprint(output, msg)
		line, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("PAM Prompt error: %v", err)
			return "", err
		}
		return strings.TrimRight(line, "\n"), nil
	case pam.PromptEchoOff:
		fmt.Fprint(output, msg)
		input, err := term.ReadPassword(int(readerFile.Fd()))
		fmt.Fprint(output, "\n")
		if err != nil {
			log.Fatalf("PAM Password Prompt error: %v", err)
			return "", err
		}
		return string(input), nil
	default:
		return "", fmt.Errorf("PAM style %d not implemented", style)
	}
	return "", nil
}

func openReader(pamTTY string) (*bufio.Reader, *os.File, *os.File, func()) {
	if pamTTY == "" {
		return bufio.NewReader(os.Stdin), os.Stdin, nil, func() {}
	}

	pamTTYFile, err := os.OpenFile(pamTTY, os.O_RDWR, 0)
	if err != nil {
		log.Printf("Can't open PAM_TTY %q, falling back to stdin: %v", pamTTY, err)
		return bufio.NewReader(os.Stdin), os.Stdin, nil, func() {}
	}

	return bufio.NewReader(pamTTYFile), pamTTYFile, pamTTYFile, func() { pamTTYFile.Close() }
}

func printPamResult(output io.Writer, resultAction pam_test.RunnerResultAction, user string, result error) {
	if user == "" {
		user = "<unset>"
	}
	fmt.Fprintln(output, resultAction.MessageWithError(user, result))
}

func sameFile(a, b *os.File) bool {
	ai, err := a.Stat()
	if err != nil {
		return false
	}
	bi, err := b.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func getPkgConfigFlags(args []string) ([]string, error) {
	out, err := exec.Command("pkg-config", args...).CombinedOutput()
	if err != nil {
		fmt.Errorf("can't get pkg-config dependencies: %w: %s", err, out)
	}
	return strings.Split(strings.TrimSpace(string(out)), " "), nil
}

func buildExecModule(path string) (string, error) {
	execModule := filepath.Join(path, "pam_exec.so")
	deps, err := getPkgConfigFlags([]string{"--cflags", "--libs", "gio-2.0", "gio-unix-2.0"})
	if err != nil {
		return "", err
	}
	cmd := exec.Command("cc", "pam/go-exec/module.c", "-o", execModule,
		"-shared", "-fPIC")
	cmd.Args = append(cmd.Args, deps...)
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("can't compile exec module %s: %w\n%s", execModule, err, out)
	}

	return execModule, nil
}

func buildExecChild(path string) (string, error) {
	cliPath := filepath.Join(path, "exec-child")
	cmd := exec.Command("go", "build", "-C", "pam", "-o", cliPath)
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("can't compile child %s: %v\n%s", cliPath, err, out)
	}
	return cliPath, nil
}

// projectRoot returns the absolute path to the project root.
func projectRoot() string {
	// p is the path to the current file, in this case -> {PROJECT_ROOT}/internal/testutils/path.go
	_, p, _, _ := runtime.Caller(0)

	// Walk up the tree to get the path of the project root
	l := strings.Split(p, "/")

	// Ignores the last 4 elements -> ./pam/tools/pam-runner/pam-runner.go
	l = l[:len(l)-4]

	// strings.Split removes the first "/" that indicated an AbsPath, so we append it back in the final string.
	return "/" + filepath.Join(l...)
}
