package user_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/services/user"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/db"
	userslocking "github.com/canonical/authd/internal/users/locking"
	userstestutils "github.com/canonical/authd/internal/users/testutils"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestNewService(t *testing.T) {
	t.Parallel()

	m, err := users.NewManager(users.DefaultConfig, t.TempDir())
	require.NoError(t, err, "Setup: could not create user manager")
	t.Cleanup(func() { _ = m.Stop() })

	b, err := brokers.NewManager(context.Background(), t.TempDir(), nil)
	require.NoError(t, err, "Setup: could not create broker manager")

	pm := permissions.New()
	s := user.NewService(context.Background(), m, b, &pm)

	require.NotNil(t, s, "NewService should return a service")
}

func TestGetUserByName(t *testing.T) {
	tests := map[string]struct {
		username string

		dbFile         string
		shouldPreCheck bool

		wantErr          bool
		wantErrNotExists bool
	}{
		"Return_existing_user":                {username: "user1"},
		"Return_existing_user_with_uppercase": {username: "USER1"},

		"Precheck_user_if_not_in_db": {username: "user-pre-check", shouldPreCheck: true},
		"Prechecked_user_with_upper_cases_in_username_has_same_id_as_lower_case": {username: "User-Pre-Check", shouldPreCheck: true},

		"Error_with_typed_GRPC_notfound_code_on_unexisting_user": {username: "does-not-exists", wantErr: true, wantErrNotExists: true},
		"Error_on_missing_name":                                  {wantErr: true},

		"Error_if_user_not_in_db_and_precheck_is_disabled":             {username: "user-pre-check", wantErr: true, wantErrNotExists: true},
		"Error_if_user_not_in_db_and_precheck_fails":                   {username: "does-not-exist", dbFile: "empty.db.yaml", shouldPreCheck: true, wantErr: true, wantErrNotExists: true},
		"Error_if_user_not_in_db_and_precheck_fails_for_existing_user": {username: "local-pre-check", dbFile: "empty.db.yaml", shouldPreCheck: true, wantErr: true, wantErrNotExists: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.shouldPreCheck {
				userslocking.Z_ForTests_OverrideLockingWithCleanup(t)
			}

			client, _ := newUserServiceClient(t, tc.dbFile)

			u, err := client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{Name: tc.username, ShouldPreCheck: tc.shouldPreCheck})
			requireExpectedResult(t, "GetUserByName", u, err, tc.wantErr, tc.wantErrNotExists)

			// Check that the user name is lowercase
			if u != nil {
				require.Equal(t, u.Name, strings.ToLower(u.Name), "User name should be lowercase")
			}

			if !tc.shouldPreCheck || tc.wantErr {
				return
			}

			_, err = client.GetUserByName(context.Background(), &authd.GetUserByNameRequest{Name: tc.username, ShouldPreCheck: false})
			require.Error(t, err, "GetUserByName should return an error, but did not")
		})
	}
}

func TestGetUserByID(t *testing.T) {
	tests := map[string]struct {
		uid uint32

		dbFile string

		wantErr          bool
		wantErrNotExists bool
	}{
		"Return_existing_user": {uid: 1111},

		"Error_with_typed_GRPC_notfound_code_on_unexisting_user": {uid: 4242, wantErr: true, wantErrNotExists: true},
		"Error_on_missing_uid": {wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client, _ := newUserServiceClient(t, tc.dbFile)

			got, err := client.GetUserByID(context.Background(), &authd.GetUserByIDRequest{Id: tc.uid})
			requireExpectedResult(t, "GetUserByID", got, err, tc.wantErr, tc.wantErrNotExists)
		})
	}
}

func TestGetGroupByName(t *testing.T) {
	tests := map[string]struct {
		groupname string

		dbFile string

		wantErr          bool
		wantErrNotExists bool
	}{
		"Return_existing_group":                {groupname: "group1"},
		"Return_existing_group_with_uppercase": {groupname: "GROUP1"},

		"Error_with_typed_GRPC_notfound_code_on_unexisting_user": {groupname: "does-not-exists", wantErr: true, wantErrNotExists: true},
		"Error_on_missing_name":                                  {wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client, _ := newUserServiceClient(t, tc.dbFile)

			group, err := client.GetGroupByName(context.Background(), &authd.GetGroupByNameRequest{Name: tc.groupname})
			requireExpectedResult(t, "GetGroupByName", group, err, tc.wantErr, tc.wantErrNotExists)

			// Check that the group name is lowercase
			if group != nil {
				require.Equal(t, group.Name, strings.ToLower(group.Name), "Group name should be lowercase")
			}
		})
	}
}

func TestGetGroupByID(t *testing.T) {
	tests := map[string]struct {
		gid uint32

		dbFile string

		wantErr          bool
		wantErrNotExists bool
	}{
		"Return_existing_group": {gid: 11111},

		"Error_with_typed_GRPC_notfound_code_on_unexisting_user": {gid: 4242, wantErr: true, wantErrNotExists: true},
		"Error_on_missing_uid": {wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client, _ := newUserServiceClient(t, tc.dbFile)

			got, err := client.GetGroupByID(context.Background(), &authd.GetGroupByIDRequest{Id: tc.gid})
			requireExpectedResult(t, "GetGroupByID", got, err, tc.wantErr, tc.wantErrNotExists)
		})
	}
}

func TestListUsers(t *testing.T) {
	tests := map[string]struct {
		dbFile string

		wantErr bool
	}{
		"Return_all_users": {},
		"Return_no_users":  {dbFile: "empty.db.yaml"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.dbFile == "" {
				tc.dbFile = "default.db.yaml"
			}

			client, _ := newUserServiceClient(t, tc.dbFile)

			resp, err := client.ListUsers(context.Background(), &authd.Empty{})
			requireExpectedListResult(t, "ListUsers", resp.GetUsers(), err, tc.wantErr)
		})
	}
}

func TestListGroups(t *testing.T) {
	tests := map[string]struct {
		dbFile string

		wantErr bool
	}{
		"Return_all_groups": {},
		"Return_no_groups":  {dbFile: "empty.db.yaml"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.dbFile == "" {
				tc.dbFile = "default.db.yaml"
			}

			client, _ := newUserServiceClient(t, tc.dbFile)

			resp, err := client.ListGroups(context.Background(), &authd.Empty{})
			if tc.wantErr {
				require.Error(t, err, "ListGroups should return an error")
				s, ok := status.FromError(err)
				require.True(t, ok, "ListGroups should return a gRPC error")
				require.NotEqual(t, codes.NotFound, s.Code(), "ListGroups should not return NotFound error even with empty list")
				return
			}

			golden.CheckOrUpdateYAML(t, resp)
		})
	}
}

func TestLockUser(t *testing.T) {
	tests := map[string]struct {
		sourceDB string

		username           string
		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_lock_user":                {username: "user1"},
		"Successfully_lock_user_with_uppercase": {username: "USER1"},

		"Error_when_username_is_empty":   {wantErr: true},
		"Error_when_user_does_not_exist": {username: "doesnotexist", wantErr: true},
		"Error_when_not_root":            {username: "notroot", currentUserNotRoot: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client, m := newUserServiceClient(t, tc.sourceDB)

			_, err := client.LockUser(context.Background(), &authd.LockUserRequest{Name: tc.username})
			if tc.wantErr {
				require.Error(t, err, "LockUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "LockUser should not return an error, but did")

			dbContent, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Setup: failed to dump database for comparing")
			golden.CheckOrUpdate(t, dbContent)
		})
	}
}

func TestUnlockUser(t *testing.T) {
	tests := map[string]struct {
		sourceDB string

		username           string
		currentUserNotRoot bool

		wantErr bool
	}{
		"Successfully_unlock_user":                {username: "user1"},
		"Successfully_unlock_user_with_uppercase": {username: "USER1"},

		"Error_when_username_is_empty":   {wantErr: true},
		"Error_when_user_does_not_exist": {username: "doesnotexist", wantErr: true},
		"Error_when_not_root":            {username: "notroot", currentUserNotRoot: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.sourceDB == "" {
				tc.sourceDB = "locked-user.db.yaml"
			}

			client, m := newUserServiceClient(t, tc.sourceDB)

			_, err := client.UnlockUser(context.Background(), &authd.UnlockUserRequest{Name: tc.username})
			if tc.wantErr {
				require.Error(t, err, "UnlockUser should return an error, but did not")
				return
			}
			require.NoError(t, err, "UnlockUser should not return an error, but did")

			dbContent, err := db.Z_ForTests_DumpNormalizedYAML(userstestutils.GetManagerDB(m))
			require.NoError(t, err, "Setup: failed to dump database for comparing")
			golden.CheckOrUpdate(t, dbContent)
		})
	}
}

// newUserServiceClient returns a new gRPC client for the CLI service.
func newUserServiceClient(t *testing.T, dbFile string) (client authd.UserServiceClient, userManager *users.Manager) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "authd-socket-dir")
	require.NoError(t, err, "Setup: could not setup temporary socket dir path")
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "authd.sock")

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err, "Setup: could not create unix socket")

	dbDir := t.TempDir()
	if dbFile != "" {
		err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", dbFile), dbDir)
		require.NoError(t, err, "Setup: could not create database from testdata")
	}

	userManager = newUserManagerForTests(t, dbFile)
	brokerManager := newBrokersManagerForTests(t)
	permissionsManager := permissions.New(permissions.Z_ForTests_WithCurrentUserAsRoot())
	service := user.NewService(context.Background(), userManager, brokerManager, &permissionsManager)

	grpcServer := grpc.NewServer(permissions.WithUnixPeerCreds(), grpc.ChainUnaryInterceptor(enableCheckGlobalAccess(service), errmessages.RedactErrorInterceptor))
	authd.RegisterUserServiceServer(grpcServer, service)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		<-done
	})

	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Setup: Could not connect to gRPC server")

	t.Cleanup(func() { _ = conn.Close() }) // We don't care about the error on cleanup

	return authd.NewUserServiceClient(conn), userManager
}

func enableCheckGlobalAccess(s user.Service) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := s.CheckGlobalAccess(ctx, info.FullMethod); err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// newUserManagerForTests returns a user manager object cleaned up with the test ends.
func newUserManagerForTests(t *testing.T, dbFile string) *users.Manager {
	t.Helper()

	dbDir := t.TempDir()
	if dbFile == "" {
		dbFile = "default.db.yaml"
	}
	err := db.Z_ForTests_CreateDBFromYAML(filepath.Join("testdata", dbFile), dbDir)
	require.NoError(t, err, "Setup: could not create database from testdata")

	managerOpts := []users.Option{
		users.WithIDGenerator(&users.IDGeneratorMock{
			UIDsToGenerate: []uint32{1234},
		}),
	}

	m, err := users.NewManager(users.DefaultConfig, dbDir, managerOpts...)
	require.NoError(t, err, "Setup: could not create user manager")

	t.Cleanup(func() { _ = m.Stop() })
	return m
}

// newBrokersManagerForTests returns a new broker manager with a broker mock for tests, it's cleaned when the test ends.
func newBrokersManagerForTests(t *testing.T) *brokers.Manager {
	t.Helper()

	cfg, cleanup, err := testutils.StartBusBrokerMock(t.TempDir(), "BrokerMock")
	require.NoError(t, err, "Setup: could not start bus broker mock")
	t.Cleanup(cleanup)

	m, err := brokers.NewManager(context.Background(), filepath.Dir(cfg), nil)
	require.NoError(t, err, "Setup: could not create broker manager")
	t.Cleanup(m.Stop)

	return m
}

// requireExpectedResult asserts expected results from a get request and checks or updates the golden file.
func requireExpectedResult[T authd.User | authd.Group](t *testing.T, funcName string, got *T, err error, wantErr, wantErrNotExists bool) {
	t.Helper()

	if wantErr {
		require.Error(t, err, fmt.Sprintf("%s should return an error but did not", funcName))
		s, ok := status.FromError(err)
		require.True(t, ok, "The error is always a gRPC error")
		if wantErrNotExists {
			require.Equal(t, codes.NotFound.String(), s.Code().String())
			return
		}
		require.NotEqual(t, codes.NotFound.String(), s.Code().String())
		return
	}
	require.NoError(t, err, fmt.Sprintf("%s should not return an error, but did", funcName))

	golden.CheckOrUpdateYAML(t, got)
}

// requireExpectedResult asserts expected results from a list request and checks or updates the golden file.
func requireExpectedListResult[T authd.User | authd.Group](t *testing.T, funcName string, got []*T, err error, wantErr bool) {
	t.Helper()

	if wantErr {
		require.Error(t, err, fmt.Sprintf("%s should return an error but did not", funcName))
		s, ok := status.FromError(err)
		require.True(t, ok, "The error is always a gRPC error")
		require.NotEqual(t, codes.NotFound, s.Code(), fmt.Sprintf("%s should never return NotFound error even with empty list", funcName))
		return
	}
	require.NoError(t, err, fmt.Sprintf("%s should not return an error, but did", funcName))

	golden.CheckOrUpdateYAML(t, got)
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Println("Error starting system bus mock:", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
