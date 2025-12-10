// Package completion provides completion functions for authctl.
package completion

import (
	"context"
	"time"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/status"
)

const timeout = 5 * time.Second

// Users returns the list of authd users for shell completion.
func Users(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	svc, err := client.NewUserServiceClient()
	if err != nil {
		return showError(err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	resp, err := svc.ListUsers(ctx, &authd.Empty{})
	if err != nil {
		return showError(err)
	}

	var userNames []string
	for _, user := range resp.Users {
		userNames = append(userNames, user.Name)
	}

	return userNames, cobra.ShellCompDirectiveNoFileComp
}

// Groups returns the list of authd groups for shell completion.
func Groups(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, err := client.NewUserServiceClient()
	if err != nil {
		return showError(err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	resp, err := c.ListGroups(ctx, &authd.Empty{})
	if err != nil {
		return showError(err)
	}

	var groupNames []string
	for _, group := range resp.Groups {
		groupNames = append(groupNames, group.Name)
	}

	return groupNames, cobra.ShellCompDirectiveNoFileComp
}

// NoArgs returns no arguments and disables file completion.
func NoArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func showError(err error) ([]string, cobra.ShellCompDirective) {
	if s, ok := status.FromError(err); ok {
		return showMessage(s.Message())
	}

	return showMessage(err.Error())
}

func showMessage(msg string) ([]string, cobra.ShellCompDirective) {
	return cobra.AppendActiveHelp(nil, msg), cobra.ShellCompDirectiveNoFileComp
}
