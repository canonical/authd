package user

import (
	"context"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

var setShellCmd = &cobra.Command{
	Use:               "set-shell <name> <shell>",
	Short:             "Set the login shell for a user",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: setShellCompletionFunc,
	RunE:              runSetShell,
}

func runSetShell(cmd *cobra.Command, args []string) error {
	name := args[0]
	shell := args[1]

	svc, err := client.NewUserServiceClient()
	if err != nil {
		return err
	}

	resp, err := svc.SetShell(context.Background(), &authd.SetShellRequest{
		Name:  name,
		Shell: shell,
	})
	if resp == nil {
		return err
	}

	// Print any warnings returned by the server.
	for _, warning := range resp.Warnings {
		log.Warning(warning)
	}

	return err
}

func setShellCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completion.Users(cmd, args, toComplete)
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}
