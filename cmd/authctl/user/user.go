// Package user provides utilities for managing user operations.
package user

import (
	"context"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// UserCmd is a command to perform user-related operations.
var UserCmd = &cobra.Command{
	Use:   "user",
	Short: "Commands related to users",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Usage() },
}

func init() {
	UserCmd.AddCommand(lockCmd)
	UserCmd.AddCommand(unlockCmd)
	UserCmd.AddCommand(setUIDCmd)
	UserCmd.AddCommand(setShellCmd)
	UserCmd.AddCommand(setHomeDirCmd)
	UserCmd.AddCommand(deleteCmd)
}

// resolveName resolves a username, which can be either the Unix username or
// provider username, to the canonical Unix username.
func resolveName(ctx context.Context, service authd.UserServiceClient, username string) (string, error) {
	user, err := service.GetUserByName(ctx, &authd.GetUserByNameRequest{Name: username})
	if err != nil {
		return "", err
	}

	return user.GetName(), nil
}
