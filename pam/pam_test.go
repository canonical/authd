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

// moduleDataTransaction is a pam.ModuleTransaction that only supports the module data calls. The
// embedded interface is nil, so any other call panics rather than silently misbehaving.
type moduleDataTransaction struct {
	pam.ModuleTransaction

	data map[string]any
}

func (t *moduleDataTransaction) SetData(key string, data any) error {
	if t.data == nil {
		t.data = make(map[string]any)
	}
	t.data[key] = data
	return nil
}

func (t *moduleDataTransaction) GetData(key string) (any, error) {
	data, ok := t.data[key]
	if !ok {
		return nil, pam.ErrNoModuleData
	}
	return data, nil
}

func TestPendingUserAliasIDs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		stored any

		want    []string
		wantErr bool
	}{
		"No_data_returns_no_leases":       {stored: nil},
		"Empty_list_returns_no_leases":    {stored: []string{}, want: []string{}},
		"Single_lease_is_returned":        {stored: []string{"lease-1"}, want: []string{"lease-1"}},
		"Every_lease_is_returned":         {stored: []string{"lease-1", "lease-2"}, want: []string{"lease-1", "lease-2"}},
		"Error_on_unexpected_data_type":   {stored: "lease-1", wantErr: true},
		"Error_on_empty_lease_in_a_list":  {stored: []string{"lease-1", ""}, wantErr: true},
		"Error_on_a_list_of_wrong_values": {stored: []int{1}, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mTx := &moduleDataTransaction{}
			if tc.stored != nil {
				require.NoError(t, mTx.SetData(pendingUserAliasKey, tc.stored))
			}

			got, err := pendingUserAliasIDs(mTx)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAddPendingUserAliasID checks that a PAM transaction authenticating more than once, as
// force_reauth allows, keeps a handle on every alias it leased. A dropped lease would pin the
// aliased name for the whole lease TTL and keep other users from claiming it.
func TestAddPendingUserAliasID(t *testing.T) {
	t.Parallel()

	mTx := &moduleDataTransaction{}

	require.NoError(t, addPendingUserAliasID(mTx, "lease-1"))
	leases, err := pendingUserAliasIDs(mTx)
	require.NoError(t, err)
	require.Equal(t, []string{"lease-1"}, leases)

	require.NoError(t, addPendingUserAliasID(mTx, "lease-2"))
	leases, err = pendingUserAliasIDs(mTx)
	require.NoError(t, err)
	require.Equal(t, []string{"lease-1", "lease-2"}, leases,
		"a second authentication must not drop the lease of the first one")

	require.NoError(t, addPendingUserAliasID(mTx, "lease-1"))
	leases, err = pendingUserAliasIDs(mTx)
	require.NoError(t, err)
	require.Equal(t, []string{"lease-1", "lease-2"}, leases,
		"re-adding a known lease should not duplicate it")

	require.NoError(t, mTx.SetData(pendingUserAliasKey, "not-a-list"))
	require.ErrorIs(t, addPendingUserAliasID(mTx, "lease-3"), pam.ErrSystem)
}
