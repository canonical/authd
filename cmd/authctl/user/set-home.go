package user

import (
	"context"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// setHomeDirCmd is a command to set the home directory of a user managed by authd.
var setHomeDirCmd = &cobra.Command{
	Use:   "set-home <user> <path>",
	Short: "Set the home directory of a user managed by authd",
	Long: `Set the home directory of a user managed by authd to the specified path.

The path must be absolute. The command must be run as root.

If the user's current home directory exists, its contents are moved to the new
location. The move is performed with rename(2), so the new path must be on the
same filesystem as the current home directory; otherwise the command fails and
the directory must be moved manually.

The command fails without making any change if the new path already exists. If
the user's current home directory does not exist, only the database record is
updated and no directory is created.

The command refuses to run while the user has active processes.`,
	Example: `  # Set the home directory of user "alice"
  authctl user set-home alice /home/alice-new`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: setHomeDirCompletionFunc,
	RunE:              runSetHomeDir,
}

func runSetHomeDir(cmd *cobra.Command, args []string) error {
	name := args[0]
	home := args[1]

	svc, err := client.NewUserServiceClient()
	if err != nil {
		return err
	}

	resp, err := svc.SetHomeDir(context.Background(), &authd.SetHomeDirRequest{
		Name: name,
		Home: home,
	})
	if resp == nil {
		return err
	}

	if resp.HomeDirChanged {
		log.Infof("Home directory of user '%s' set to '%s'.", name, home)
		if resp.HomeDirMoved {
			log.Info("Moved the user's home directory to the new location.")
		}
	}

	// Print any warnings returned by the server.
	for _, warning := range resp.Warnings {
		log.Warning(warning)
	}

	return err
}

func setHomeDirCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completion.Users(cmd, args, toComplete)
	}

	// The second argument is a path: offer directory completion.
	return nil, cobra.ShellCompDirectiveFilterDirs
}
