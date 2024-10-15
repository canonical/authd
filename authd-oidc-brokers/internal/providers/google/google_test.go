package google_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/providers/google"
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
