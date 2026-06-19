package user

import (
	"context"
)

// CheckGlobalAccess always return access, then individual calls are filtered.
func (s Service) CheckGlobalAccess(ctx context.Context, method string) error {
	return nil
}
