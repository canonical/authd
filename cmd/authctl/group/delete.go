package group

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
WARNING: Deleting a group that still owns files on the filesystem can lead to
security issues. Any existing files owned by this group's GID may become
accessible to a different group that is later assigned the same GID.
`

// deleteGroupCmd is a command to delete a group from the authd database.
var deleteGroupCmd = &cobra.Command{
	Use:   "delete <group>",
	Short: "Delete a group managed by authd",
	Long:  "Delete a group from the authd database.\n\n" + warningMessage + "\n\nThe command must be run as root.",
	Example: `  # Delete group "staff" from the authd database
  authctl group delete staff

  # Delete group "staff" without confirmation prompt
  authctl group delete --yes staff`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completion.Groups,
	RunE:              runDeleteGroup,
}

var deleteGroupYes bool

func init() {
	deleteGroupCmd.Flags().BoolVarP(&deleteGroupYes, "yes", "y", false, "Skip confirmation prompt")
}

func runDeleteGroup(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !deleteGroupYes {
		log.Warning(warningMessage)
		fmt.Fprintf(os.Stderr, "Are you sure you want to delete group %q? [y/N] ", name)

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

	_, err = c.DeleteGroup(context.Background(), &authd.DeleteGroupRequest{Name: name})
	if err != nil {
		return err
	}

	log.Infof("Group %q has been deleted from the authd database.", name)
	return nil
}
