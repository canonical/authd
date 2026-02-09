// Package services mediates all the business logic of the application via a manager.
package services

import (
	"context"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/errmessages"
	"github.com/canonical/authd/internal/services/pam"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/services/user"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// Manager mediate the whole business logic of the application.
type Manager struct {
	userManager   *users.Manager
	brokerManager *brokers.Manager
	pamService    pam.Service
	userService   user.Service
}

// NewManager returns a new manager after creating all necessary items for our business logic.
func NewManager(ctx context.Context, dbDir, brokersConfPath string, configuredBrokers []string, usersConfig users.Config) (m Manager, err error) {
	log.Debug(ctx, "Building authd object")

	brokerManager, err := brokers.NewManager(ctx, brokersConfPath, configuredBrokers)
	if err != nil {
		return m, err
	}

	userManager, err := users.NewManager(usersConfig, dbDir)
	if err != nil {
		return m, err
	}

	permissionManager := permissions.New()

	userService := user.NewService(ctx, userManager, brokerManager, &permissionManager)
	pamService := pam.NewService(ctx, userManager, brokerManager, &permissionManager)

	return Manager{
		userManager:   userManager,
		brokerManager: brokerManager,
		userService:   userService,
		pamService:    pamService,
	}, nil
}

// RegisterGRPCServices returns a new grpc Server after registering both NSS and PAM services.
func (m Manager) RegisterGRPCServices(ctx context.Context) *grpc.Server {
	log.Debug(ctx, "Registering gRPC services")

	opts := []grpc.ServerOption{permissions.WithUnixPeerCreds(), grpc.ChainUnaryInterceptor(m.globalPermissions, errmessages.RedactErrorInterceptor)}
	grpcServer := grpc.NewServer(opts...)

	healthCheck := health.NewServer()
	healthgrpc.RegisterHealthServer(grpcServer, healthCheck)

	// We may provide status per each service, but for now we only care about the global state.
	// Also, we're serving by default because all the brokers have been initialized at this
	// point, so no need to start in NOT_SERVING mode and then update it accordingly.
	defer healthCheck.SetServingStatus(consts.ServiceName, healthgrpc.HealthCheckResponse_SERVING)

	authd.RegisterUserServiceServer(grpcServer, m.userService)
	authd.RegisterPAMServer(grpcServer, m.pamService)

	return grpcServer
}

// stop stops the underlying database.
func (m *Manager) stop() error {
	log.Debug(context.TODO(), "Closing gRPC manager and database")

	return m.userManager.Stop()
}
