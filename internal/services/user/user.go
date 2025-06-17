// Package user provides the gRPC service for user and group management.
package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service is the implementation of the gRPC user service.
type Service struct {
	userManager       *users.Manager
	brokerManager     *brokers.Manager
	permissionManager *permissions.Manager

	authd.UnimplementedUserServiceServer
}

// NewService returns a new gRPC user service.
func NewService(ctx context.Context, userManager *users.Manager, brokerManager *brokers.Manager, permissionManager *permissions.Manager) Service {
	log.Debug(ctx, "Building new gRPC user service")

	return Service{
		userManager:       userManager,
		brokerManager:     brokerManager,
		permissionManager: permissionManager,
	}
}

// GetUserByName returns the user entry for the given username.
func (s Service) GetUserByName(ctx context.Context, req *authd.GetUserByNameRequest) (*authd.User, error) {
	// authd usernames are lowercase
	name := strings.ToLower(req.GetName())
	if name == "" {
		log.Warningf(ctx, "GetUserByName: no user name provided")
		return nil, status.Error(codes.InvalidArgument, "no user name provided")
	}

	user, err := s.userManager.UserByName(name)
	if err == nil {
		return userToProtobuf(user), nil
	}

	if !errors.Is(err, users.NoDataFoundError{}) {
		log.Errorf(context.Background(), "GetUserByName: %v", err)
		return nil, grpcError(err)
	}

	if !req.GetShouldPreCheck() {
		// The user was not found in the database and pre-check is not requested.
		// It often happens that NSS requests are sent for users that are not in the authd database, so to avoid
		// spamming the logs, we only log this at debug level.
		log.Debugf(context.Background(), "GetUserByName: %v", err)
		return nil, grpcError(err)
	}

	// If the user is not found in the database, we check if it exists in at least one broker.
	user, err = s.userPreCheck(ctx, name)
	if errors.Is(err, errUserNotPermitted) {
		err := fmt.Errorf("user %q is unknown and not authorized to log in via SSH for the first time by any configured broker", name)
		log.Warningf(context.Background(), "GetUserByName: %v", err)
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		log.Errorf(context.Background(), "GetUserByName: %v", err)
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return userToProtobuf(user), nil
}

// GetUserByID returns the user entry for the given user ID.
func (s Service) GetUserByID(ctx context.Context, req *authd.GetUserByIDRequest) (*authd.User, error) {
	if req.GetId() == 0 {
		log.Warningf(ctx, "GetUserByID: no user ID provided")
		return nil, status.Error(codes.InvalidArgument, "no user ID provided")
	}

	u, err := s.userManager.UserByID(req.Id)
	if errors.Is(err, users.NoDataFoundError{}) {
		// Only log this at debug level, see GetUserByName for details.
		log.Debugf(context.Background(), "GetUserByID: %v", err)
		return nil, grpcError(err)
	}
	if err != nil {
		log.Errorf(context.Background(), "GetUserByID: %v", err)
		return nil, grpcError(err)
	}

	return userToProtobuf(u), nil
}

// ListUsers returns all authd users.
func (s Service) ListUsers(ctx context.Context, req *authd.Empty) (*authd.Users, error) {
	allUsers, err := s.userManager.AllUsers()
	if err != nil {
		log.Errorf(context.Background(), "ListUsers: %v", err)
		return nil, grpcError(err)
	}

	var res authd.Users
	for _, u := range allUsers {
		res.Users = append(res.Users, userToProtobuf(u))
	}

	return &res, nil
}

// LockUser marks a user as locked.
func (s Service) LockUser(ctx context.Context, req *authd.LockUserRequest) (*authd.Empty, error) {
	if err := s.permissionManager.CheckRequestIsFromRoot(ctx); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// authd uses lowercase usernames.
	name := strings.ToLower(req.GetName())

	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "no user name provided")
	}

	if err := s.userManager.LockUser(name); err != nil {
		return nil, grpcError(err)
	}

	return &authd.Empty{}, nil
}

// UnlockUser marks a user as unlocked.
func (s Service) UnlockUser(ctx context.Context, req *authd.UnlockUserRequest) (*authd.Empty, error) {
	if err := s.permissionManager.CheckRequestIsFromRoot(ctx); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// authd uses lowercase usernames.
	name := strings.ToLower(req.GetName())

	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "no user name provided")
	}

	if err := s.userManager.UnlockUser(name); err != nil {
		return nil, grpcError(err)
	}

	return &authd.Empty{}, nil
}

// GetGroupByName returns the group entry for the given group name.
func (s Service) GetGroupByName(ctx context.Context, req *authd.GetGroupByNameRequest) (*authd.Group, error) {
	// authd uses lowercase group names.
	name := strings.ToLower(req.GetName())

	if name == "" {
		log.Warningf(ctx, "GetGroupByName: no group name provided")
		return nil, status.Error(codes.InvalidArgument, "no group name provided")
	}

	g, err := s.userManager.GroupByName(name)
	if errors.Is(err, users.NoDataFoundError{}) {
		// Only log this at debug level, see GetUserByName for details
		log.Debugf(context.Background(), "GetGroupByName: %v", err)
		return nil, grpcError(err)
	}
	if err != nil {
		log.Errorf(context.Background(), "GetGroupByName: %v", err)
		return nil, grpcError(err)
	}

	return groupToProtobuf(g), nil
}

// GetGroupByID returns the group entry for the given group ID.
func (s Service) GetGroupByID(ctx context.Context, req *authd.GetGroupByIDRequest) (*authd.Group, error) {
	if req.GetId() == 0 {
		log.Warningf(ctx, "GetGroupByID: no group ID provided")
		return nil, status.Error(codes.InvalidArgument, "no group ID provided")
	}

	g, err := s.userManager.GroupByID(req.GetId())
	if errors.Is(err, users.NoDataFoundError{}) {
		// Only log this at debug level, see GetUserByName for details
		log.Debugf(context.Background(), "GetGroupByID: %v", err)
		return nil, grpcError(err)
	}
	if err != nil {
		log.Errorf(context.Background(), "GetGroupByID: %v", err)
		return nil, grpcError(err)
	}

	return groupToProtobuf(g), nil
}

// ListGroups returns all authd groups.
func (s Service) ListGroups(ctx context.Context, req *authd.Empty) (*authd.Groups, error) {
	allGroups, err := s.userManager.AllGroups()
	if err != nil {
		log.Errorf(context.Background(), "ListGroups: %v", err)
		return nil, grpcError(err)
	}

	var res authd.Groups
	for _, g := range allGroups {
		res.Groups = append(res.Groups, groupToProtobuf(g))
	}

	return &res, nil
}

// SetUserID sets the UID of a user.
func (s Service) SetUserID(ctx context.Context, req *authd.SetUserIDRequest) (*authd.SetUserIDResponse, error) {
	if err := s.permissionManager.CheckRequestIsFromRoot(ctx); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// authd uses lowercase usernames.
	name := strings.ToLower(req.GetName())

	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "no user name provided")
	}

	warnings, err := s.userManager.SetUserID(name, req.GetId())
	if err != nil {
		log.Errorf(ctx, "SetUserID: %v", err)
		return nil, grpcError(err)
	}

	return &authd.SetUserIDResponse{Warnings: warnings}, nil
}

// userToProtobuf converts a types.UserEntry to authd.User.
func userToProtobuf(u types.UserEntry) *authd.User {
	return &authd.User{
		Name:    u.Name,
		Uid:     u.UID,
		Gid:     u.GID,
		Gecos:   u.Gecos,
		Homedir: u.Dir,
		Shell:   u.Shell,
	}
}

// groupToProtobuf converts a types.GroupEntry to authd.Group.
func groupToProtobuf(g types.GroupEntry) *authd.Group {
	return &authd.Group{
		Name:    g.Name,
		Gid:     g.GID,
		Members: g.Users,
		Passwd:  g.Passwd,
	}
}

var errUserNotPermitted = errors.New("user not permitted to log in via SSH for the first time")

// userPreCheck checks if the user is permitted to log in via SSH for the first time.
// It returns a types.UserEntry with a unique UID if the user is permitted to log in.
// If the user is not permitted to log in by any broker, errUserNotPermitted is returned.
func (s Service) userPreCheck(ctx context.Context, username string) (types.UserEntry, error) {
	// Check if any broker permits the user to log in via SSH for the first time.
	var userinfo string
	var err error
	for _, b := range s.brokerManager.AvailableBrokers() {
		// The local broker is not a real broker, so we skip it.
		if b.ID == brokers.LocalBrokerName {
			continue
		}

		userinfo, err = b.UserPreCheck(ctx, username)
		if err != nil {
			// An unexpected error occurred while checking the user.
			log.Errorf(ctx, "UserPreCheck: %v", err)
			continue
		}
		if userinfo == "" {
			// The broker does not permit the user to log in via SSH for the first time.
			// This is an expected error, so we only log it at debug level.
			log.Debugf(ctx, "UserPreCheck: %v", err)
			continue
		}
		break
	}

	if err != nil || userinfo == "" {
		// No broker permits the user to log in via SSH for the first time.
		return types.UserEntry{}, errUserNotPermitted
	}

	var u types.UserEntry
	if err := json.Unmarshal([]byte(userinfo), &u); err != nil {
		return types.UserEntry{}, fmt.Errorf("user data from broker invalid: %v", err)
	}

	// Register a temporary user with a unique UID. If the user authenticates successfully, the user will be added to
	// the database with the same UID.
	u.UID, err = s.userManager.RegisterUserPreAuth(u.Name)
	if err != nil {
		return types.UserEntry{}, fmt.Errorf("failed to add temporary record for user %q: %v", username, err)
	}
	// The UID is also the GID of the user private group (see https://wiki.debian.org/UserPrivateGroups#UPGs)
	u.GID = u.UID

	return u, nil
}

// grpcError converts a data not found to proper GRPC status code.
// The NSS module uses this status code to determine the NSS status it should return.
func grpcError(err error) error {
	if errors.Is(err, users.NoDataFoundError{}) {
		return status.Error(codes.NotFound, err.Error())
	}

	return err
}
