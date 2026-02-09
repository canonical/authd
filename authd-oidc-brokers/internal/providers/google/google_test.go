package google_test

import (
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/google"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	p := google.New()

	require.Empty(t, p, "New should return the default provider implementation with no parameters")
}

func TestAdditionalScopes(t *testing.T) {
	t.Parallel()

	p := google.New()

	require.Empty(t, p.AdditionalScopes(), "Google provider should not require additional scopes")
}
