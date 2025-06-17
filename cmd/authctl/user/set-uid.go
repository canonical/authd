package user

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/spf13/cobra"
)

// setUIDCmd is a command to set the UID of a user managed by authd.
var setUIDCmd = &cobra.Command{
	Use:   "set-uid <name> <uid>",
	Short: "Set the UID of a user managed by authd",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		uidStr := args[1]
		uid, err := strconv.ParseUint(uidStr, 10, 32)
		if err != nil {
			// Remove the "strconv.ParseUint: parsing ..." part from the error message
			// because it doesn't add any useful information.
			if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
				err = unwrappedErr
			}
			return fmt.Errorf("failed to parse UID %q: %w", uidStr, err)
		}

		client, err := NewUserServiceClient()
		if err != nil {
			return err
		}

		_, err = client.SetUserID(context.Background(), &authd.SetUserIDRequest{
			Name: name,
			Id:   uint32(uid),
		})
		if err != nil {
			return err
		}

		return nil
	},
}
