package user

import (
	"context"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// unlockCmd is a command to unlock (enable) a user.
var unlockCmd = &cobra.Command{
	Use:               "unlock <user>",
	Short:             "Unlock (enable) a user managed by authd",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completion.Users,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := client.NewUserServiceClient()
		if err != nil {
			return err
		}

		_, err = client.UnlockUser(context.Background(), &authd.UnlockUserRequest{Name: args[0]})
		if err != nil {
			return err
		}

		return nil
	},
}
