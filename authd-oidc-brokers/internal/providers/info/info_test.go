package info_test

import (
	"errors"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/stretchr/testify/require"
)

func TestNewUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name   string
		home   string
		uuid   string
		shell  string
		gecos  string
		groups []info.Group
	}{
		"Create_a_new_user": {
			name:   "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},

		// Default values
		"Create_a_new_user_with_default_home": {
			name:   "test-user",
			home:   "",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},
		"Create_a_new_user_with_default_shell": {
			name:   "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "",
			gecos:  "Test User",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}},
		},
		"Create_a_new_user_with_default_gecos": {name: "test-user",
			home:   "/home/test-user",
			uuid:   "some-uuid",
			shell:  "/usr/bin/zsh",
			gecos:  "",
			groups: []info.Group{{Name: "test-group", UGID: "12345"}}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantHome := tc.home
			if tc.home == "" {
				wantHome = tc.name
			}

			wantShell := tc.shell
			if tc.shell == "" {
				wantShell = "/usr/bin/bash"
			}

			wantGecos := tc.gecos
			if tc.gecos == "" {
				wantGecos = tc.name
			}

			got := info.NewUser(tc.name, tc.home, tc.uuid, tc.shell, tc.gecos, tc.groups)
			require.Equal(t, tc.name, got.Name, "Name does not match the expected value")
			require.Equal(t, wantHome, got.Home, "Home does not match the expected value")
			require.Equal(t, tc.uuid, got.UUID, "UUID does not match the expected value")
			require.Equal(t, wantShell, got.Shell, "Shell does not match the expected value")
			require.Equal(t, wantGecos, got.Gecos, "Gecos does not match the expected value")
			require.Equal(t, tc.groups, got.Groups, "Groups do not match the expected value")
		})
	}
}

// mockClaimer implements info.Claimer for testing.
type mockClaimer struct {
	data map[string]interface{}
	err  error
}

func (m *mockClaimer) Claims(v any) error {
	if m.err != nil {
		return m.err
	}
	p, ok := v.(*map[string]interface{})
	if !ok {
		return errors.New("mockClaimer: expected *map[string]interface{}")
	}
	*p = m.data
	return nil
}

func TestNewMergedClaimer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		claimers    []info.Claimer
		wantErr     bool
		wantMissing string
		wantMerged  map[string]interface{}
	}{
		"Single_claimer": {
			claimers: []info.Claimer{
				&mockClaimer{data: map[string]interface{}{"foo": "bar"}},
			},
			wantMerged: map[string]interface{}{"foo": "bar"},
		},
		"Later_claimer_overrides_earlier_for_same_key": {
			claimers: []info.Claimer{
				&mockClaimer{data: map[string]interface{}{"key": "first", "only_first": "v1"}},
				&mockClaimer{data: map[string]interface{}{"key": "second", "only_second": "v2"}},
			},
			wantMerged: map[string]interface{}{"key": "second", "only_first": "v1", "only_second": "v2"},
		},
		"No_claimers_produces_empty_merged_map": {
			claimers:   []info.Claimer{},
			wantMerged: map[string]interface{}{},
		},
		"Error_from_any_claimer_propagates": {
			claimers: []info.Claimer{
				&mockClaimer{data: map[string]interface{}{"foo": "bar"}},
				&mockClaimer{err: errors.New("claimer failure")},
			},
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mc, err := info.NewMergedClaimer(tc.claimers...)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, mc)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, mc)

			// Verify merged content by decoding into a plain map.
			var got map[string]interface{}
			err = mc.Claims(&got)
			require.NoError(t, err)
			require.Equal(t, tc.wantMerged, got)
		})
	}
}

func TestMergedClaimerClaims(t *testing.T) {
	t.Parallel()

	type dest struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	tests := map[string]struct {
		data    map[string]interface{}
		want    dest
		wantErr bool
	}{
		"Decodes_known_fields_into_struct": {
			data: map[string]interface{}{"name": "alice", "value": 42},
			want: dest{Name: "alice", Value: 42},
		},
		"Unknown_fields_are_ignored": {
			data: map[string]interface{}{"name": "bob", "value": 7, "extra": "ignored"},
			want: dest{Name: "bob", Value: 7},
		},
		"Missing_fields_are_zeroed": {
			data: map[string]interface{}{"name": "carol"},
			want: dest{Name: "carol", Value: 0},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mc, err := info.NewMergedClaimer(&mockClaimer{data: tc.data})
			require.NoError(t, err)

			var got dest
			err = mc.Claims(&got)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
