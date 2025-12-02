// Package group provides utilities for managing group operations.
package group

import (
	"github.com/spf13/cobra"
)

// GroupCmd is a command to perform group-related operations.
var GroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Commands related to groups",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Usage() },
}

func init() {
	GroupCmd.AddCommand(setGIDCmd)
}
