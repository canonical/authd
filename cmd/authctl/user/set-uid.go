package user

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// setUIDCmd is a command to set the UID of a user managed by authd.
var setUIDCmd = &cobra.Command{
	Use:   "set-uid <name> <uid>",
	Short: "Set the UID of a user managed by authd",
	Long: `Set the UID of a user managed by authd to the specified value.

The new UID value must be unique and non-negative.

The user's home directory and any files within it owned by the user will
automatically have their ownership updated to the new UID.

Files outside the user's home directory are not updated and must be changed
manually. Note that changing a UID can be unsafe if files on the system are
still owned by the original UID: those files may become accessible to a
different account that is later assigned that UID.

This command requires root privileges.

Examples:
  authctl user set-uid john 15000
  authctl user set-uid alice 20000`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: setUIDCompletionFunc,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		uidStr := args[1]
		uid, err := strconv.ParseUint(uidStr, 10, 32)
		if err != nil {
			// Remove the "strconv.ParseUint: parsing ..." part from the error message
			// because it doesn't add any useful information.
			if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
				err = unwrappedErr
			}
			return fmt.Errorf("failed to parse UID %q: %w", uidStr, err)
		}

		client, err := client.NewUserServiceClient()
		if err != nil {
			return err
		}

		resp, err := client.SetUserID(context.Background(), &authd.SetUserIDRequest{
			Name: name,
			Id:   uint32(uid),
			Lang: os.Getenv("LANG"),
		})
		if resp == nil {
			return err
		}

		if resp.IdChanged {
			log.Infof("UID of user '%s' set to %d.", name, uid)
			if resp.HomeDirOwnerChanged {
				log.Info("Updated ownership of the user's home directory.")
			}
			log.Info("Note: Ownership of files outside the user's home directory are not updated and must be changed manually.")
		}

		// Print any warnings returned by the server.
		for _, warning := range resp.Warnings {
			log.Warning(warning)
		}

		return err
	},
}

func setUIDCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completion.Users(cmd, args, toComplete)
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}
