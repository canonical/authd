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

The user's private group is renamed along with the user, because authd names
private groups after the user they belong to.

The new username is kept across logins: authd remembers that it was set
locally, so the broker does not restore the username reported by the identity
provider. The user must log in with the new username from now on.

Only users that authd identifies by a stable provider ID can be renamed,
because that ID is what ties the new name to the account. Users stored before
authd started recording it get one on their next login with a broker that
provides it.

The user must not have any active processes when renaming.`,
	Example: `  # Rename user "alice" to "alice-new"
  authctl user set-name alice alice-new`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: setNameCompletionFunc,
	RunE:              runSetName,
}

func runSetName(cmd *cobra.Command, args []string) error {
	svc, err := client.NewUserServiceClient()
	if err != nil {
		return err
	}

	resp, err := svc.SetUserName(context.Background(), &authd.SetUserNameRequest{
		OldName: args[0],
		NewName: args[1],
	})
	if err != nil {
		return err
	}

	// Report the names authd actually used rather than the ones typed, because authd lowercases
	// usernames before storing them.
	log.Infof("User '%s' renamed to '%s'.", resp.GetOldName(), resp.GetNewName())
	if resp.GetPrivateGroupRenamed() {
		log.Infof("The user's private group was renamed to '%s' as well.", resp.GetNewName())
	}

	// Print any warnings returned by the server.
	for _, warning := range resp.GetWarnings() {
		log.Warning(warning)
	}

	return nil
}

func setNameCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completion.Users(cmd, args, toComplete)
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}
