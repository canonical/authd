//go:build !withlocalbus

package dbusservice_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/authd/brokers/internal/broker"
	"github.com/canonical/authd/brokers/internal/broker/sessionmode"
	"github.com/canonical/authd/brokers/internal/consts"
	"github.com/canonical/authd/brokers/internal/dbusservice"
	"github.com/canonical/authd/brokers/internal/testutils"
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
		"",
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

func TestPrivilegedMethodsOverSystemBusCheckCallerUID(t *testing.T) {
	cleanup, err := testutils.StartSystemBusMock()
	require.NoError(t, err, "Setup: starting the mock bus should not fail")
	defer cleanup()

	configPath := filepath.Join(t.TempDir(), "broker.conf")
	require.NoError(t, os.WriteFile(configPath, []byte(`[oidc]
issuer = `+defaultIssuerURL+`
client_id = test-client-id

[flows]
entra_auth = false
`), 0600), "Setup: writing the broker config should not fail")

	dataDir := t.TempDir()
	service, err := dbusservice.New(context.Background(), broker.Config{
		ConfigFile: configPath,
		DataDir:    dataDir,
	})
	require.NoError(t, err, "Setup: starting the D-Bus service should not fail")
	defer func() { _ = service.Stop() }()

	client, err := testutils.GetSystemBusConnection(t)
	require.NoError(t, err, "Setup: connecting to the mock bus should not fail")
	defer client.Close()

	const username = "user@example.com"
	const providerID = "provider-id"
	tests := []struct {
		name             string
		method           string
		args             []any
		deletedCacheKeys []string
		wantErrorName    string
	}{
		{
			name: "v3 accepts provider ID only from root", method: "com.ubuntu.authd.Broker3.NewSession",
			args: []any{username, "en", sessionmode.Login, providerID},
		},
		{
			name: "v1 deletes username cache", method: "com.ubuntu.authd.Broker.DeleteUser",
			args: []any{username}, deletedCacheKeys: []string{username},
		},
		{
			name: "v2 deletes username cache", method: "com.ubuntu.authd.Broker2.DeleteUser",
			args: []any{username}, deletedCacheKeys: []string{username},
		},
		{
			name: "v3 deletes username and provider ID caches", method: "com.ubuntu.authd.Broker3.DeleteUser",
			args: []any{username, providerID}, deletedCacheKeys: []string{username, providerID},
		},
		{
			name: "v3 deletes provider ID cache without username", method: "com.ubuntu.authd.Broker3.DeleteUser",
			args: []any{"", providerID}, deletedCacheKeys: []string{providerID},
		},
		{
			name: "v1 rejects invalid username", method: "com.ubuntu.authd.Broker.DeleteUser",
			args: []any{"invalid/../user"}, wantErrorName: "org.freedesktop.DBus.Error.Failed",
		},
		{
			name: "v2 rejects invalid username", method: "com.ubuntu.authd.Broker2.DeleteUser",
			args: []any{"invalid/../user"}, wantErrorName: "org.freedesktop.DBus.Error.Failed",
		},
		{
			name: "v3 rejects invalid username", method: "com.ubuntu.authd.Broker3.DeleteUser",
			args: []any{"invalid/../user", ""}, wantErrorName: "org.freedesktop.DBus.Error.Failed",
		},
		{
			name: "v3 encodes provider ID path characters", method: "com.ubuntu.authd.Broker3.DeleteUser",
			args: []any{username, "invalid/../provider-id"}, deletedCacheKeys: []string{username},
		},
	}
	issuerDir := filepath.Join(dataDir, strings.ReplaceAll(strings.TrimPrefix(defaultIssuerURL, "http://"), ":", "_"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, cacheKey := range []string{username, providerID} {
				cacheDir := filepath.Join(issuerDir, cacheKey)
				require.NoError(t, os.MkdirAll(cacheDir, 0700), "Setup: creating the cache directory should not fail")
				require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "password"), []byte("cached-password"), 0600),
					"Setup: writing cached data should not fail")
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			call := client.Object(consts.DbusName, dbus.ObjectPath(consts.DbusObject)).CallWithContext(ctx, tc.method, 0, tc.args...)
			wantErrorName := tc.wantErrorName
			if os.Geteuid() != 0 {
				wantErrorName = "org.freedesktop.DBus.Error.AccessDenied"
			}
			if wantErrorName == "" {
				require.NoError(t, call.Err, "A root caller should be allowed to delete cached data")
			} else {
				var dbusErr dbus.Error
				require.ErrorAs(t, call.Err, &dbusErr)
				require.Equal(t, wantErrorName, dbusErr.Name)
			}

			for _, cacheKey := range []string{username, providerID} {
				cacheDir := filepath.Join(issuerDir, cacheKey)
				if os.Geteuid() == 0 && slices.Contains(tc.deletedCacheKeys, cacheKey) {
					require.NoDirExists(t, cacheDir, "The requested cache directory should be deleted")
					continue
				}
				data, err := os.ReadFile(filepath.Join(cacheDir, "password"))
				require.NoError(t, err, "Unrequested caches and caches from denied or invalid calls must be preserved")
				require.Equal(t, "cached-password", string(data))
			}
		})
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
		"",
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
