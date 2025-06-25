package localentries

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetGroupEntries(t *testing.T) {
	t.Parallel()

	// Ensure the requests can be parallelized
	for idx := range 10 {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			got, err := GetGroupEntries()
			require.NoError(t, err, "GetGroupEntries should never return an error")
			require.NotEmpty(t, got, "GetGroupEntries should never return an empty list")
			require.True(t, slices.ContainsFunc(got, func(g Group) bool {
				return g.Name == "root" && g.GID == 0
			}), "GetGroupEntries should return root")
		})
	}
}

func TestGetGroupByName(t *testing.T) {
	t.Parallel()

	for idx := range 10 {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			got, err := GetGroupByName("root")
			require.NoError(t, err, "GetGroupByName should not return an error")
			require.Equal(t, got.Name, "root")
			require.Equal(t, got.GID, uint32(0))
			require.Equal(t, got.Passwd, "x")
		})
	}
}

func TestGetGroupByName_NotFound(t *testing.T) {
	t.Parallel()

	for idx := range 10 {
		t.Run(fmt.Sprintf("iteration_%d", idx), func(t *testing.T) {
			t.Parallel()

			got, err := GetGroupByName(fmt.Sprintf("nonexistent-really-%d", idx))
			require.ErrorIs(t, err, ErrGroupNotFound)
			require.Equal(t, got.Name, "")
		})
	}
}
