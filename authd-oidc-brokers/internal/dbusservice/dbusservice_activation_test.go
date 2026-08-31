//go:build !withlocalbus

package dbusservice_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/broker"
	"github.com/canonical/authd/authd-oidc-brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/dbusservice"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

func TestQueuedActivationCallDoesNotBlockInitialization(t *testing.T) {
	serviceDir := t.TempDir()
	activationDir := t.TempDir()
	pidPath := filepath.Join(activationDir, "activation.pid")
	activationScript := filepath.Join(activationDir, "activation.sh")
	//nolint:gosec // The activation helper must be executable by dbus-daemon.
	require.NoError(t, os.WriteFile(activationScript, []byte(fmt.Sprintf(`#!/bin/sh
printf '%%s' "$$" > %s
exec sleep 30
`, pidPath)), 0700), "Setup: writing the activation helper should not fail")

	serviceFile := filepath.Join(serviceDir, consts.DbusName+".service")
	require.NoError(t, os.WriteFile(serviceFile, []byte(fmt.Sprintf(`[D-BUS Service]
Name=%s
Exec=%s
`, consts.DbusName, activationScript)), 0600), "Setup: writing the activation file should not fail")

	busAddress, busCleanup, err := testutils.StartBusMockWithServiceDir(serviceDir)
	require.NoError(t, err, "Setup: starting the mock bus should not fail")
	defer busCleanup()
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", busAddress)

	client, err := testutils.GetSystemBusConnection(t)
	require.NoError(t, err, "Setup: connecting to the mock bus should not fail")
	defer client.Close()

	callDone := make(chan *dbus.Call, 1)
	client.Object(consts.DbusName, dbus.ObjectPath(consts.DbusObject)).Go(
		"com.ubuntu.authd.Broker3.NewSession",
		0,
		callDone,
		"user@example.com",
		"en",
		sessionmode.Login,
		"provider-id",
	)

	require.Eventually(t, func() bool {
		_, err := os.Stat(pidPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "D-Bus should start the activation helper")

	pidData, err := os.ReadFile(pidPath)
	require.NoError(t, err, "Setup: reading the activation helper PID should not fail")
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	require.NoError(t, err, "Setup: parsing the activation helper PID should not fail")
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	})

	configPath := filepath.Join(t.TempDir(), "broker.conf")
	require.NoError(t, os.WriteFile(configPath, []byte(`[oidc]
issuer = `+defaultIssuerURL+`
client_id = test-client-id

[flows]
entra_auth = false
`), 0600), "Setup: writing the broker config should not fail")
	dataDir := t.TempDir()

	serviceResult := make(chan struct {
		service *dbusservice.Service
		err     error
	}, 1)
	go func() {
		service, err := dbusservice.New(context.Background(), broker.Config{
			ConfigFile: configPath,
			DataDir:    dataDir,
		})
		serviceResult <- struct {
			service *dbusservice.Service
			err     error
		}{service: service, err: err}
	}()

	var service *dbusservice.Service
	select {
	case result := <-serviceResult:
		require.NoError(t, result.err, "The D-Bus service should initialize")
		require.NotNil(t, result.service, "The D-Bus service should be returned")
		service = result.service
	case <-time.After(5 * time.Second):
		t.Fatal("The D-Bus service initialization is blocked by the queued activation call")
	}
	defer func() { _ = service.Stop() }()

	select {
	case call := <-callDone:
		var sessionID, encryptionKey string
		require.NoError(t, call.Store(&sessionID, &encryptionKey), "The queued activation call should succeed")
		require.NotEmpty(t, sessionID, "The queued activation call should return a session ID")
		require.NotEmpty(t, encryptionKey, "The queued activation call should return an encryption key")
	case <-time.After(5 * time.Second):
		t.Fatal("The queued activation call did not complete")
	}
}

func TestQueuedActivationCallReceivesInitializationFailure(t *testing.T) {
	serviceDir := t.TempDir()
	activationDir := t.TempDir()
	pidPath := filepath.Join(activationDir, "activation.pid")
	activationScript := filepath.Join(activationDir, "activation.sh")
	//nolint:gosec // The activation helper must be executable by dbus-daemon.
	require.NoError(t, os.WriteFile(activationScript, []byte(fmt.Sprintf(`#!/bin/sh
printf '%%s' "$$" > %s
exec sleep 30
`, pidPath)), 0700), "Setup: writing the activation helper should not fail")

	serviceFile := filepath.Join(serviceDir, consts.DbusName+".service")
	require.NoError(t, os.WriteFile(serviceFile, []byte(fmt.Sprintf(`[D-BUS Service]
Name=%s
Exec=%s
`, consts.DbusName, activationScript)), 0600), "Setup: writing the activation file should not fail")

	busAddress, busCleanup, err := testutils.StartBusMockWithServiceDir(serviceDir)
	require.NoError(t, err, "Setup: starting the mock bus should not fail")
	defer busCleanup()
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", busAddress)

	client, err := testutils.GetSystemBusConnection(t)
	require.NoError(t, err, "Setup: connecting to the mock bus should not fail")
	defer client.Close()

	callDone := make(chan *dbus.Call, 1)
	client.Object(consts.DbusName, dbus.ObjectPath(consts.DbusObject)).Go(
		"com.ubuntu.authd.Broker3.NewSession",
		0,
		callDone,
		"user@example.com",
		"en",
		sessionmode.Login,
		"provider-id",
	)

	require.Eventually(t, func() bool {
		_, err := os.Stat(pidPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "D-Bus should start the activation helper")

	pidData, err := os.ReadFile(pidPath)
	require.NoError(t, err, "Setup: reading the activation helper PID should not fail")
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	require.NoError(t, err, "Setup: parsing the activation helper PID should not fail")
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	})

	configPath := filepath.Join(t.TempDir(), "broker.conf")
	require.NoError(t, os.WriteFile(configPath, []byte("[invalid]\n"), 0600),
		"Setup: writing the invalid broker config should not fail")

	serviceResult := make(chan error, 1)
	go func() {
		_, err := dbusservice.New(context.Background(), broker.Config{
			ConfigFile: configPath,
			DataDir:    t.TempDir(),
		})
		serviceResult <- err
	}()

	select {
	case err := <-serviceResult:
		require.Error(t, err, "The broker service should fail initialization")
	case <-time.After(5 * time.Second):
		t.Fatal("The broker service initialization did not fail")
	}

	select {
	case call := <-callDone:
		var dbusErr dbus.Error
		require.ErrorAs(t, call.Err, &dbusErr)
		t.Logf("received D-Bus error %s: %s", dbusErr.Name, dbusErr.Error())
		switch dbusErr.Name {
		case "com.ubuntu.authd.BrokerUnavailable":
			require.Contains(t, dbusErr.Error(), "unknown section")
		case "org.freedesktop.DBus.Error.NoReply":
			// The service may disconnect before the queued handler sends its
			// response. authd handles this as broker unavailability too.
		default:
			require.Failf(t, "unexpected D-Bus error", "got %s: %s", dbusErr.Name, dbusErr.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("The queued activation call did not complete")
	}
}
