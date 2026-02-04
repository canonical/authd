package group

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

// setGIDCmd is a command to set the GID of a group managed by authd.
var setGIDCmd = &cobra.Command{
	Use:   "set-gid <name> <gid>",
	Short: "Set the GID of a group managed by authd",
	Long: `Set the GID of a group managed by authd to the specified value.

The new GID value must be unique and non-negative.

When a group's GID is changed, any users whose primary group is set to this group
will have their primary group GID updated. The home directories of these users and
files within them owned by the group will be updated to the new GID.

Files outside users' home directories are not updated and must be changed
manually. Note that changing a GID can be unsafe if files on the system are
still owned by the original GID: those files may become accessible to a
different group that is later assigned that GID.

This command requires root privileges.

Examples:
  authctl group set-gid staff 30000
  authctl group set-gid developers 40000`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completion.Groups,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		gidStr := args[1]
		gid, err := strconv.ParseUint(gidStr, 10, 32)
		if err != nil {
			// Remove the "strconv.ParseUint: parsing ..." part from the error message
			// because it doesn't add any useful information.
			if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
				err = unwrappedErr
			}
			return fmt.Errorf("failed to parse GID %q: %w", gidStr, err)
		}

		client, err := client.NewUserServiceClient()
		if err != nil {
			return err
		}

		resp, err := client.SetGroupID(context.Background(), &authd.SetGroupIDRequest{
			Name: name,
			Id:   uint32(gid),
			Lang: os.Getenv("LANG"),
		})
		if err != nil {
			return err
		}

		// Print any warnings returned by the server.
		for _, warning := range resp.Warnings {
			log.Warningf("Warning: %s", warning)
		}

		return nil
	},
}
