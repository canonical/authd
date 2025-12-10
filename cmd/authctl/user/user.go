// Package user provides utilities for managing user operations.
package user

import (
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
}
