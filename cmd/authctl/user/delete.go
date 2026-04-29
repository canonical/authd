package user

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/authd/cmd/authctl/internal/client"
	"github.com/canonical/authd/cmd/authctl/internal/completion"
	"github.com/canonical/authd/cmd/authctl/internal/log"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

const warningMessage = `
WARNING: Deleting a user that still owns files on the filesystem can lead to
security issues. Any existing files owned by this user's UID may become
accessible to a different user that is later assigned the same UID. If the
user is later re-created, they may be assigned a new UID, breaking ownership
of their existing home directory and files.

If you only want to prevent the user from logging in, consider using
'authctl user lock' instead. A locked user retains their UID, ensuring
no other user can be assigned the same UID.
`

// deleteCmd is a command to delete a user from the authd database.
var deleteCmd = &cobra.Command{
	Use:   "delete <user>",
	Short: "Delete a user managed by authd",
	Long:  "Delete a user from the authd database.\n\n" + warningMessage + "\n\nThe command must be run as root.",
	Example: `  # Delete user "alice" from the authd database
  authctl user delete alice

  # Delete user "alice" without confirmation prompt
  authctl user delete --yes alice

  # Delete user "alice" and remove their home directory
  authctl user delete --remove alice`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completion.Users,
	RunE:              runDeleteUser,
}

var deleteUserYes bool
var deleteUserRemoveHome bool

func init() {
	deleteCmd.Flags().BoolVarP(&deleteUserYes, "yes", "y", false, "Skip confirmation prompt")
	deleteCmd.Flags().BoolVarP(&deleteUserRemoveHome, "remove", "r", false, "Remove the user's home directory")
}

func runDeleteUser(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !deleteUserYes {
		log.Warning(warningMessage)
		fmt.Fprintf(os.Stderr, "Are you sure you want to delete user %q? [y/N] ", name)

		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read confirmation: %w", err)
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			log.Info("Aborted.")
			return nil
		}
	}

	c, err := client.NewUserServiceClient()
	if err != nil {
		return err
	}

	_, err = c.DeleteUser(context.Background(), &authd.DeleteUserRequest{Name: name, RemoveHome: deleteUserRemoveHome})
	if err != nil {
		return err
	}

	log.Infof("User %q has been deleted from the authd database.", name)
	return nil
}
