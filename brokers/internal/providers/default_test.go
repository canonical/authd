//go:build !withgoogle && !withmsentraid

package providers_test

import (
	"testing"

	"github.com/canonical/authd/brokers/internal/providers"
	"github.com/stretchr/testify/require"
)

// The broker tests use their own providers, so this test checks that the
// provider of the generic broker can read groups from the ID token claim.
func TestCurrentProviderReadsGroupsFromIDTokenClaim(t *testing.T) {
	t.Parallel()

	p := providers.CurrentProvider()

	_, ok := providers.ProviderAs[providers.GroupsClaimConfigurer](p)
	require.True(t, ok, "CurrentProvider should accept the groups claim configuration")
	_, ok = providers.ProviderAs[providers.GroupFetcher](p)
	require.True(t, ok, "CurrentProvider should fetch groups")
}
