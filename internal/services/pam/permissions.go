package pam

import "context"

// CheckGlobalAccess denies all requests not coming from the root user.
func (s Service) CheckGlobalAccess(ctx context.Context, method string) error {
	return s.permissionManager.CheckRequestIsFromRoot(ctx)
}
