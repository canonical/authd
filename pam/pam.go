// Package main is the package for the PAM library.
package main

import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/grpcutils"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/adapter"
	"github.com/canonical/authd/pam/internal/gdm"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coreos/go-systemd/v22/journal"
	"github.com/msteinert/pam/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// pamModule is the structure that implements the pam.ModuleHandler interface
// that is called during pam operations.
type pamModule struct {
}

const (

	// alreadyAuthenticatedKey is the Key used to store in the library that
	// we've already authenticated with this module and so that we should not
	// do this again.
	alreadyAuthenticatedKey = "authd.already-authenticated-flag"

	// loggingInitializedKey indicates logging was already initialized.
	loggingInitializedKey = "authd.logging-initialized-flag"

	// gdmServiceName is the name of the service that is loaded by GDM.
	// Keep this in sync with the service file installed by the package.
	gdmServiceName = "gdm-authd"

	// defaultConnectionTimeout is the default connection timeout.
	defaultConnectionTimeout = 2 * time.Second
)

// reportAuthtok is called after PAM_AUTHTOK is set. It is a no-op by default;
// the pam_debug build overrides it to print the token so it appears in golden
// files.
var reportAuthtok = func(authtok string) {}

// reportOldAuthtok is called after PAM_OLDAUTHTOK is set. Like reportAuthtok it
// is a no-op by default and overridden by the pam_debug build.
var reportOldAuthtok = func(oldAuthtok string) {}

var supportedArgs = []string{
	"debug",               // When this is set to "true", then debug logging is enabled.
	"logfile",             // The path of the file that will be used for logging.
	"disable_journal",     // Disable logging on systemd journal (this is implicit when `logfile` is set).
	"socket",              // The authd socket to connect to.
	"connection_timeout",  // The timeout on connecting to authd socket in milliseconds (defaults to 2 seconds).
	"force_native_client", // Use native PAM client instead of custom UIs.
	"force_reauth",        // Whether the authentication should be performed again even if it has been already completed.
}

// parseArgs parses the PAM arguments and returns a map of them and a function that logs the parsing issues.
// Such function should be called once the logger is setup, as the arguments may change the logging behavior.
func parseArgs(args []string) (map[string]string, func()) {
	parsed := make(map[string]string)
	var warnings []string

	for _, arg := range args {
		opt, value, _ := strings.Cut(arg, "=")
		parsed[opt] = value

		if !slices.Contains(supportedArgs, opt) {
			warnings = append(warnings,
				fmt.Sprintf("Provided argument %q is not supported and will be ignored", arg))
		}
	}

	return parsed, func() {
		for _, warn := range warnings {
			log.Warning(context.TODO(), warn)
		}
	}
}

func showPamMessage(mTx pam.ModuleTransaction, style pam.Style, msg string) error {
	switch style {
	case pam.TextInfo, pam.ErrorMsg:
	default:
		return fmt.Errorf("message style not supported: %v", style)
	}
	if _, err := mTx.StartStringConv(style, msg); err != nil {
		log.Errorf(context.TODO(), "Failed sending message to pam: %v", err)
		return err
	}
	return nil
}

func sendReturnMessageToPam(mTx pam.ModuleTransaction, retStatus adapter.PamReturnValue) {
	msg := retStatus.Message()
	if msg == "" {
		return
	}

	style := pam.ErrorMsg
	switch rs := retStatus.(type) {
	case adapter.PamSuccess:
		style = pam.TextInfo
	case adapter.PamReturnError:
		if rs.Status() == pam.ErrIgnore {
			style = pam.TextInfo
		}
	}

	if err := showPamMessage(mTx, style, msg); err != nil {
		log.Warningf(context.TODO(), "Impossible to send PAM message: %v", err)
	}
}

func shouldSendAuthMessage(clientType adapter.PamClientType, msg string, isSuccess bool) bool {
	if msg == "" {
		return false
	}

	if isSuccess {
		// Native clients (SSH, non-TTY) already display the success message
		// via the native model's sendInfo path; skip the PAM-conversation echo
		// to avoid printing it twice.
		return clientType != adapter.Native
	}

	return true
}

// initLogging initializes the logging given the passed parameters.
// It returns a function that should be called in order to reset the logging to
// the default and potentially close the opened resources.
func initLogging(mTx pam.ModuleTransaction, args map[string]string, flags pam.Flags) (func(), error) {
	alreadyInitialized, err := mTx.GetData(loggingInitializedKey)
	if err != nil && !errors.Is(err, pam.ErrNoModuleData) {
		return func() {}, err
	}
	if initialized, ok := alreadyInitialized.(bool); ok && initialized {
		return func() {}, nil
	}

	log.SetLevel(log.InfoLevel)
	resetFunc := func() { _ = mTx.SetData(loggingInitializedKey, nil) }

	if args["debug"] == "true" {
		baseResetFunc := resetFunc
		log.SetLevel(log.DebugLevel)
		resetFunc = func() {
			log.SetLevel(log.InfoLevel)
			baseResetFunc()
		}
	}

	isSilent := flags&pam.Silent != 0
	if isSilent {
		// If PAM required us to be silent, let's use an empty log handler.
		baseResetFunc := resetFunc
		log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {})
		resetFunc = func() {
			baseResetFunc()
			log.SetHandler(nil)
		}
	}

	if out, ok := args["logfile"]; ok && out != "" {
		f, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
		if err != nil {
			return resetFunc, err
		}
		log.SetOutput(f)
		if isSilent {
			// We're silent on PAM side, but we want to still log to a file
			log.SetHandler(nil)
		}
		if err := mTx.SetData(loggingInitializedKey, true); err != nil {
			resetFunc()
			log.SetOutput(os.Stderr)
			f.Close()
			return func() {}, err
		}
		return func() {
			resetFunc()
			log.SetOutput(os.Stderr)
			f.Close()
		}, nil
	}

	disableTerminalLogging := func() {
		if log.IsLevelEnabled(log.DebugLevel) {
			return
		}
		if adapter.IsTerminalTTY(mTx) {
			return
		}
		log.SetLevel(log.WarnLevel)
	}

	if !journal.Enabled() || args["disable_journal"] == "true" {
		disableTerminalLogging()
		if err := mTx.SetData(loggingInitializedKey, true); err != nil {
			resetFunc()
			return func() {}, err
		}
		return resetFunc, nil
	}

	// Force logging to the journal because we're running as a PAM module and don't want to clutter the output of the
	// program that has loaded us.
	log.InitJournalHandler(true)
	if err := mTx.SetData(loggingInitializedKey, true); err != nil {
		resetFunc()
		log.SetHandler(nil)
		return func() {}, err
	}

	return func() {
		resetFunc()
		log.SetHandler(nil)
	}, nil
}

// Authenticate is the method that is invoked during pam_authenticate request.
func (h *pamModule) Authenticate(mTx pam.ModuleTransaction, flags pam.Flags, args []string) error {
	// Do not try to start authentication again if we've been already through this.
	// Since PAM modules can be stacked, so we may suffer reentry that is fine but it should
	// be explicitly allowed.
	parsedArgs, logArgsIssues := parseArgs(args)
	alreadyAuth, err := mTx.GetData(alreadyAuthenticatedKey)
	if alreadyAuth != nil && err == nil && parsedArgs["force_reauth"] != "true" {
		return pam.ErrIgnore
	}
	if err != nil && !errors.Is(err, pam.ErrNoModuleData) {
		return err
	}

	err = h.handleAuthRequest(authd.SessionMode_LOGIN, mTx, flags, parsedArgs, logArgsIssues)
	if err != nil && !errors.Is(err, pam.ErrIgnore) {
		return err
	}
	if err := mTx.SetData(alreadyAuthenticatedKey, true); err != nil {
		return err
	}
	return err
}

// ChangeAuthTok is the method that is invoked during pam_sm_chauthtok request.
func (h *pamModule) ChangeAuthTok(mTx pam.ModuleTransaction, flags pam.Flags, args []string) error {
	parsedArgs, logArgsIssues := parseArgs(args)

	err := h.handleAuthRequest(authd.SessionMode_CHANGE_PASSWORD, mTx, flags, parsedArgs, logArgsIssues)
	if errors.Is(err, pam.ErrPermDenied) {
		return pam.ErrAuthtokRecovery
	}
	return err
}

func (h *pamModule) handleAuthRequest(mode authd.SessionMode, mTx pam.ModuleTransaction, flags pam.Flags, parsedArgs map[string]string, logArgsIssues func()) (err error) {
	// Initialize localization
	// TODO

	var pamClientType adapter.PamClientType
	var teaOpts []tea.ProgramOption

	closeLogging, err := initLogging(mTx, parsedArgs, flags)
	defer func() {
		log.Debugf(context.TODO(), "%s: exiting with error %v", mode, err)

		// Wait a moment, before resetting as we may still receive bubbletea
		// events that we could log in the wrong place.
		<-time.After(time.Millisecond * 30)
		closeLogging()
	}()
	if err != nil {
		return err
	}
	logArgsIssues()

	if mode == authd.SessionMode_CHANGE_PASSWORD && flags&pam.PrelimCheck != 0 {
		log.Debug(context.TODO(), "ChangeAuthTok, preliminary check")
		c, closeConn, err := newClient(parsedArgs)
		if err != nil {
			log.Debugf(context.TODO(), "%s", err)
			return fmt.Errorf("%w: %w", pam.ErrTryAgain, err)
		}
		defer closeConn()

		username, err := mTx.GetItem(pam.User)
		if err != nil || username == "" {
			return err
		}

		response, err := c.GetBroker(context.TODO(), &authd.GBRequest{Username: username})
		if err != nil {
			err = fmt.Errorf("could not get current available brokers: %w", err)
			if msgErr := showPamMessage(mTx, pam.ErrorMsg, err.Error()); msgErr != nil {
				log.Warningf(context.TODO(), "Impossible to show PAM message: %v", msgErr)
			}
			return fmt.Errorf("%w: %w", pam.ErrSystem, err)
		}

		if response.GetBroker() == brokers.LocalBrokerName {
			return pam.ErrIgnore
		}
		return nil
	}

	if mode == authd.SessionMode_CHANGE_PASSWORD {
		log.Debugf(context.TODO(), "ChangeAuthTok, password update phase: %d",
			flags&pam.UpdateAuthtok)
	}

	serviceName, err := mTx.GetItem(pam.Service)
	if err != nil {
		log.Warningf(context.TODO(), "Impossible to get PAM service name: %v", err)
	}
	if serviceName == gdmServiceName && !gdm.IsPamExtensionSupported(gdm.PamExtensionCustomJSON) {
		log.Debug(context.TODO(), "GDM service running without JSON extension, skipping...")
		return pam.ErrIgnore
	}

	forceNativeClient := parsedArgs["force_native_client"] == "true"
	if !forceNativeClient && gdm.IsPamExtensionSupported(gdm.PamExtensionCustomJSON) {
		pamClientType = adapter.Gdm
		modeOpts, err := adapter.TeaHeadlessOptions()
		if err != nil {
			return fmt.Errorf("%w: can't create tea options: %w", pam.ErrSystem, err)
		}
		teaOpts = append(teaOpts, modeOpts...)
	} else if !forceNativeClient && adapter.IsTerminalTTY(mTx) && !adapter.IsDumbTerminal() {
		pamClientType = adapter.InteractiveTerminal
		tty, cleanup := adapter.GetPamTTY(mTx)
		defer cleanup()
		teaOpts = append(teaOpts, tea.WithInput(tty))
	} else {
		pamClientType = adapter.Native
		modeOpts, err := adapter.TeaHeadlessOptions()
		if err != nil {
			return fmt.Errorf("%w: can't create tea options: %w", pam.ErrSystem, err)
		}
		teaOpts = append(teaOpts, modeOpts...)
	}

	conn, closeConn, err := newClientConnection(parsedArgs)
	if err != nil {
		if err := showPamMessage(mTx, pam.ErrorMsg, err.Error()); err != nil {
			log.Warningf(context.TODO(), "Impossible to show PAM message: %v", err)
		}
		return fmt.Errorf("%w: %w", pam.ErrAuthinfoUnavail, err)
	}
	defer closeConn()

	var pamReturnValue adapter.PamReturnValue
	appState := adapter.NewUIModel(mTx, pamClientType, mode, conn, &pamReturnValue)
	teaOpts = append(teaOpts, tea.WithFilter(adapter.MsgFilter))
	p := tea.NewProgram(appState, teaOpts...)
	if _, err := p.Run(); err != nil {
		log.Errorf(context.TODO(), "Cancelled authentication: %v", err)
		return pam.ErrAbort
	}

	switch returnValue := pamReturnValue.(type) {
	case adapter.PamSuccess:
		if shouldSendAuthMessage(pamClientType, returnValue.Message(), true) {
			sendReturnMessageToPam(mTx, returnValue)
		}
		if returnValue.AuthTok != "" {
			if err := mTx.SetItem(pam.Authtok, returnValue.AuthTok); err != nil {
				return err
			}
			reportAuthtok(returnValue.AuthTok)
		}
		if returnValue.OldAuthTok != "" {
			if err := mTx.SetItem(pam.Oldauthtok, returnValue.OldAuthTok); err != nil {
				return err
			}
			reportOldAuthtok(returnValue.OldAuthTok)
		}
		return nil

	case adapter.PamReturnError:
		if shouldSendAuthMessage(pamClientType, returnValue.Message(), false) {
			sendReturnMessageToPam(mTx, returnValue)
		}
		return fmt.Errorf("%w: %s", returnValue.Status(), returnValue.Message())

	default:
		// Preserve the previous behavior of showing any message associated with
		// unexpected exit statuses before returning the system error.
		sendReturnMessageToPam(mTx, returnValue)
		return fmt.Errorf("%w: unknown exit code: %#v", pam.ErrSystem, returnValue)
	}
}

// AcctMgmt is ignored because broker selection is now handled server-side during IsAuthenticated.
func (h *pamModule) AcctMgmt(_ pam.ModuleTransaction, _ pam.Flags, _ []string) error {
	return pam.ErrIgnore
}

func newClientConnection(args map[string]string) (conn *grpc.ClientConn, closeConn func(), err error) {
	conn, err = grpc.NewClient("unix://"+getSocketPath(args),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(errmessages.FormatErrorMessage))
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to authd: %v", err)
	}

	cleanup := func() { conn.Close() }

	timeout := defaultConnectionTimeout
	if ct, ok := args["connection_timeout"]; ok {
		t, err := strconv.Atoi(ct)
		if err != nil {
			log.Warningf(context.Background(), "Impossible to parse connection timeout %q, using default!", ct)
		}
		if t > 0 {
			timeout = time.Duration(t) * time.Millisecond
		}
	}

	// Block until the daemon is started and ready to accept connections.
	if err := grpcutils.WaitForConnection(context.Background(), conn, timeout); err != nil {
		cleanup()
		return nil, nil, err
	}

	return conn, cleanup, err
}

// newClient returns a new GRPC client ready to emit requests.
func newClient(args map[string]string) (client authd.PAMClient, closeConn func(), err error) {
	conn, closeConn, err := newClientConnection(args)
	if err != nil {
		return nil, nil, err
	}
	return authd.NewPAMClient(conn), closeConn, nil
}

// getSocketPath returns the socket path to connect to which can be overridden manually.
func getSocketPath(args map[string]string) string {
	if val, ok := args["socket"]; ok {
		return val
	}
	return consts.DefaultSocketPath
}

// SetCred is the method that is invoked during pam_setcred request.
func (h *pamModule) SetCred(pam.ModuleTransaction, pam.Flags, []string) error {
	return pam.ErrIgnore
}

// OpenSession is the method that is invoked during pam_open_session request.
func (h *pamModule) OpenSession(pam.ModuleTransaction, pam.Flags, []string) error {
	return pam.ErrIgnore
}

// CloseSession is the method that is invoked during pam_close_session request.
func (h *pamModule) CloseSession(pam.ModuleTransaction, pam.Flags, []string) error {
	return pam.ErrIgnore
}
