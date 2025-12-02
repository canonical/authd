package group

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/canonical/authd/cmd/authctl/user"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// setGIDCmd is a command to set the GID of a group managed by authd.
var setGIDCmd = &cobra.Command{
	Use:   "set-gid <name> <gid>",
	Short: "Set the GID of a group managed by authd",
	Args:  cobra.ExactArgs(2),
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

		client, err := user.NewUserServiceClient()
		if err != nil {
			return err
		}

		resp, err := client.SetGroupID(context.Background(), &authd.SetGroupIDRequest{
			Name: name,
			Id:   uint32(gid),
		})
		if err != nil {
			return err
		}

		// Print any warnings returned by the server.
		for _, warning := range resp.Warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		}

		return nil
	},
}
