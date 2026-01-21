package localentries

import (
	"fmt"
	"slices"
	"testing"

	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestGetPasswdEntries(t *testing.T) {
	t.Parallel()

	for idx := range 10 {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			got, err := getUserEntries()
			require.NoError(t, err, "GetPasswdEntries should never return an error")
			require.NotEmpty(t, got, "GetPasswdEntries should never return an empty list")

			// Check if the root user is present in the list
			rootFound := slices.ContainsFunc(got, func(entry types.UserEntry) bool {
				return entry.Name == "root" && entry.UID == 0 && entry.GID == 0
			})
			require.True(t, rootFound, "GetPasswdEntries should always return root")
		})
	}
}
