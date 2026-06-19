package main

import (
	"testing"

	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

func TestUnimplementedActions(t *testing.T) {
	module := &pamModule{}

	// If these gets changed, go-exec module should be also adapted accordingly
	// together with TestExecModuleUnimplementedActions
	require.Error(t, module.SetCred(nil, pam.Flags(0), nil), pam.ErrIgnore)
	require.Error(t, module.OpenSession(nil, pam.Flags(0), nil), pam.ErrIgnore)
	require.Error(t, module.CloseSession(nil, pam.Flags(0), nil), pam.ErrIgnore)
}
