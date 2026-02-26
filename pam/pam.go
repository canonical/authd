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
	// authenticationBrokerIDKey is the Key used to store the data in the
	// PAM module for the second stage authentication to select the default
	// broker for the current user.
	authenticationBrokerIDKey = "authd.authentication-broker-id"

	// alreadyAuthenticatedKey is the Key used to store in the library that
	// we've already authenticated with this module and so that we should not
	// do this again.
	alreadyAuthenticatedKey = "authd.already-authenticated-flag"

	// gdmServiceName is the name of the service that is loaded by GDM.
	// Keep this in sync with the service file installed by the package.
	gdmServiceName = "gdm-authd"

	// defaultConnectionTimeout is the default connection timeout.
	defaultConnectionTimeout = 2 * time.Second
)

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

func sendReturnMessageToPam(mTx pam.ModuleTransaction, retStatus adapter.PamReturnStatus) {
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

// initLogging initializes the logging given the passed parameters.
// It returns a function that should be called in order to reset the logging to
// the default and potentially close the opened resources.
func initLogging(mTx pam.ModuleTransaction, args map[string]string, flags pam.Flags) (func(), error) {
	log.SetLevel(log.InfoLevel)
	resetFunc := func() {}
	if args["debug"] == "true" {
		log.SetLevel(log.DebugLevel)
		resetFunc = func() { log.SetLevel(log.InfoLevel) }
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
		return resetFunc, nil
	}

	// Force logging to the journal because we're running as a PAM module and don't want to clutter the output of the
	// program that has loaded us.
	log.InitJournalHandler(true)

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

		response, err := c.GetPreviousBroker(context.TODO(), &authd.GPBRequest{Username: username})
		if err != nil {
			err = fmt.Errorf("could not get current available brokers: %w", err)
			if msgErr := showPamMessage(mTx, pam.ErrorMsg, err.Error()); msgErr != nil {
				log.Warningf(context.TODO(), "Impossible to show PAM message: %v", msgErr)
			}
			return fmt.Errorf("%w: %w", pam.ErrSystem, err)
		}

		if response.GetPreviousBroker() == brokers.LocalBrokerName {
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
	} else if !forceNativeClient && adapter.IsTerminalTTY(mTx) {
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

	if err := mTx.SetData(authenticationBrokerIDKey, nil); err != nil {
		return err
	}

	var exitStatus adapter.PamReturnStatus
	appState := adapter.NewUIModel(mTx, pamClientType, mode, conn, &exitStatus)
	teaOpts = append(teaOpts, tea.WithFilter(adapter.MsgFilter))
	p := tea.NewProgram(appState, teaOpts...)
	if _, err := p.Run(); err != nil {
		log.Errorf(context.TODO(), "Cancelled authentication: %v", err)
		return pam.ErrAbort
	}

	sendReturnMessageToPam(mTx, exitStatus)

	switch exitStatus := exitStatus.(type) {
	case adapter.PamSuccess:
		if err := mTx.SetData(authenticationBrokerIDKey, exitStatus.BrokerID); err != nil {
			return err
		}
		return nil

	case adapter.PamReturnError:
		return fmt.Errorf("%w: %s", exitStatus.Status(), exitStatus.Message())

	default:
		return fmt.Errorf("%w: unknown exit code: %#v", pam.ErrSystem, exitStatus)
	}
}

// AcctMgmt sets any used brokerID as default for the user.
func (h *pamModule) AcctMgmt(mTx pam.ModuleTransaction, flags pam.Flags, args []string) (err error) {
	parsedArgs, logArgsIssues := parseArgs(args)
	closeLogging, err := initLogging(mTx, parsedArgs, flags)
	defer closeLogging()
	defer func() {
		log.Debugf(context.TODO(), "AcctMgmt: exiting with error %v", err)
	}()
	if err != nil {
		return err
	}
	logArgsIssues()

	// We ignore AcctMgmt in case we're loading the module through the exec client
	serviceName, err := mTx.GetItem(pam.Service)
	if err != nil {
		log.Warningf(context.TODO(), "Impossible to get PAM service name: %v", err)
		return pam.ErrIgnore
	}
	if serviceName == gdmServiceName && !gdm.IsPamExtensionSupported(gdm.PamExtensionCustomJSON) {
		return pam.ErrIgnore
	}

	brokerData, err := mTx.GetData(authenticationBrokerIDKey)
	if err != nil && errors.Is(err, pam.ErrNoModuleData) {
		return pam.ErrIgnore
	}
	if brokerData == nil {
		// PAM can return no data without an error after that has been unset:
		// See: https://github.com/linux-pam/linux-pam/pull/780
		return pam.ErrIgnore
	}

	brokerIDUsedToAuthenticate, ok := brokerData.(string)
	if !ok {
		msg := fmt.Sprintf("broker data has an invalid type %#v", brokerData)
		log.Errorf(context.TODO(), msg)
		if err := showPamMessage(mTx, pam.ErrorMsg, msg); err != nil {
			log.Warningf(context.TODO(), "Impossible to show PAM message: %v", err)
		}

		return pam.ErrIgnore
	}

	// Only set the brokerID as default if we stored one after authentication.
	if brokerIDUsedToAuthenticate == "" {
		return pam.ErrIgnore
	}

	// Get current user for broker
	user, err := mTx.GetItem(pam.User)
	if err != nil {
		return err
	}

	if user == "" {
		if err := showPamMessage(mTx, pam.ErrorMsg, "Can't get user from PAM"); err != nil {
			log.Warningf(context.TODO(), "Impossible to show PAM message: %v", err)
		}
		return pam.ErrIgnore
	}

	client, closeConn, err := newClient(parsedArgs)
	if err != nil {
		log.Debugf(context.TODO(), "%s", err)
		return pam.ErrAuthinfoUnavail
	}
	defer closeConn()

	req := authd.SDBFURequest{
		BrokerId: brokerIDUsedToAuthenticate,
		Username: user,
	}
	if _, err := client.SetDefaultBrokerForUser(context.TODO(), &req); err != nil {
		msg := err.Error()
		if err := showPamMessage(mTx, pam.ErrorMsg, msg); err != nil {
			log.Warningf(context.TODO(), "Impossible to show PAM message: %v", err)
		}
		return pam.ErrIgnore
	}

	return nil
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
