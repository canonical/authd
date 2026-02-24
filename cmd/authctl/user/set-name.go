package user

import (
	"context"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// setNameCmd is a command to rename a user managed by authd.
var setNameCmd = &cobra.Command{
	Use:   "set-name <old-name> <new-name>",
	Short: "Rename a user managed by authd",
	Long: `Rename a user managed by authd to a new username.

The new username must be unique. The command must be run as root.

Note: This command does NOT rename the user's home directory. The home
directory path remains unchanged to avoid potential data loss and permission
issues. You will need to manually rename the home directory if desired.

TODO: Should we rename the user private group? usermod --login does not.

The user must not have any active processes when renaming.`,
	Example: `  # Rename user "alice" to "alice-new"
  authctl user set-name alice alice-new`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: setNameCompletionFunc,
	RunE:              runSetName,
}

func runSetName(cmd *cobra.Command, args []string) error {
	oldName := args[0]
	newName := args[1]

	svc, err := client.NewUserServiceClient()
	if err != nil {
		return err
	}

	_, err = svc.SetUserName(context.Background(), &authd.SetUserNameRequest{
		OldName: oldName,
		NewName: newName,
	})
	if err != nil {
		return err
	}

	log.Infof("User '%s' renamed to '%s'.", oldName, newName)
	log.Info("Note: The home directory path has not been changed and must be renamed manually if desired.")

	return nil
}

func setNameCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completion.Users(cmd, args, toComplete)
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}
