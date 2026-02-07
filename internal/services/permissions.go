package services

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (m Manager) globalPermissions(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if strings.HasPrefix(info.FullMethod, "/authd.PAM/") {
		if err := m.pamService.CheckGlobalAccess(ctx, info.FullMethod); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	} else if strings.HasPrefix(info.FullMethod, "/authd.NSS/") {
		if err := m.userService.CheckGlobalAccess(ctx, info.FullMethod); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	return handler(ctx, req)
}
