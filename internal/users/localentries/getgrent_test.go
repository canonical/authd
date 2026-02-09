package localentries

import (
	"fmt"
	"slices"
	"testing"

	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestGetGroupEntries(t *testing.T) {
	t.Parallel()

	// Ensure the requests can be parallelized
	for idx := range 10 {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			got, err := getGroupEntries()
			require.NoError(t, err, "GetGroupEntries should never return an error")
			require.NotEmpty(t, got, "GetGroupEntries should never return an empty list")
			require.True(t, slices.ContainsFunc(got, func(g types.GroupEntry) bool {
				return g.Name == "root" && g.GID == 0
			}), "GetGroupEntries should return root")

			require.True(t, slices.ContainsFunc(got, func(g types.GroupEntry) bool {
				return len(g.Users) > 0
			}), "GetGroupEntries should returns at least one group that have users")
		})
	}
}
