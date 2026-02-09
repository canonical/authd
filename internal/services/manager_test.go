package services_test

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNewManager(t *testing.T) {
	tests := map[string]struct {
		dbDir string

		systemBusSocket string

		wantErr bool
	}{
		"Successfully_create_the_manager": {},

		"Error_when_can_not_create_db":             {dbDir: "doesnotexist", wantErr: true},
		"Error_when_can_not_create_broker_manager": {systemBusSocket: "doesnotexist", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.dbDir == "" {
				tc.dbDir = t.TempDir()
			}
			if tc.systemBusSocket != "" {
				t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", tc.systemBusSocket)
			}

			m, err := services.NewManager(context.Background(), tc.dbDir, t.TempDir(), nil, users.DefaultConfig)
			if tc.wantErr {
				require.Error(t, err, "NewManager should have returned an error, but did not")
				return
			}
			defer require.NoError(t, m.Stop(), "Teardown: Stop should not have returned an error, but did")

			require.NoError(t, err, "NewManager should not have returned an error, but did")
		})
	}
}

func TestRegisterGRPCServices(t *testing.T) {
	t.Parallel()

	m, err := services.NewManager(context.Background(), t.TempDir(), t.TempDir(), nil, users.DefaultConfig)
	require.NoError(t, err, "Setup: could not create manager for the test")
	defer require.NoError(t, m.Stop(), "Teardown: Stop should not have returned an error, but did")

	got := m.RegisterGRPCServices(context.Background()).GetServiceInfo()
	// Make the content of the golden file deterministic by sorting the methods by name.
	for _, info := range got {
		slices.SortFunc(info.Methods, func(a, b grpc.MethodInfo) int {
			return cmp.Compare(a.Name, b.Name)
		})
	}
	golden.CheckOrUpdateYAML(t, got)
}

func TestAccessAuthorization(t *testing.T) {
	t.Parallel()

	m, err := services.NewManager(context.Background(), t.TempDir(), t.TempDir(), nil, users.DefaultConfig)
	require.NoError(t, err, "Setup: could not create manager for the test")
	defer require.NoError(t, m.Stop(), "Teardown: Stop should not have returned an error, but did")

	grpcServer := m.RegisterGRPCServices(context.Background())

	// socket path is limited in length.
	tmpDir, err := os.MkdirTemp("", "authd-socket-dir")
	require.NoError(t, err, "Setup: could not setup temporary socket dir path")
	defer os.RemoveAll(tmpDir)
	socketPath := filepath.Join(tmpDir, "authd.sock")
	lis, err := net.Listen("unix", socketPath)
	require.NoError(t, err, "Setup: could not create unix socket")
	defer lis.Close()

	serverDone := make(chan (error))
	go func() { serverDone <- grpcServer.Serve(lis) }()
	defer func() {
		grpcServer.Stop()
		require.NoError(t, <-serverDone, "gRPC server should not return an error from serving")
	}()

	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(errmessages.FormatErrorMessage))
	require.NoError(t, err, "Setup: could not dial the server")

	// Global authorization for PAM is always denied for non root user.
	pamClient := authd.NewPAMClient(conn)
	_, err = pamClient.AvailableBrokers(context.Background(), &authd.Empty{})
	require.Error(t, err, "PAM calls are not allowed to any random user")

	// Global authorization for the user service is always granted for non root user.
	userServiceClient := authd.NewUserServiceClient(conn)
	_, err = userServiceClient.GetUserByName(context.Background(), &authd.GetUserByNameRequest{Name: ""})
	require.Error(t, err, "Expected a gRPC error from the server")

	err = conn.Close()
	require.NoError(t, err, "Teardown: could not close the client connection")
}

func TestMain(m *testing.M) {
	// Start system bus mock.
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
