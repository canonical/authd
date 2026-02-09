package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupInfoEquals(t *testing.T) {
	t.Parallel()

	gid0 := uint32(0)
	gid1 := uint32(1)
	gid1Again := uint32(1)
	gid2 := uint32(2)

	tests := map[string]struct {
		a    GroupInfo
		b    GroupInfo
		want bool
	}{
		"Empty groups": {
			want: true,
		},
		"All_fields_equal_GID_non-nil_and_equal": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			want: true,
		},
		"Both_GID_nil": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: nil},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: nil},
			want: true,
		},
		"GID_pointers_different_but_values_equal": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1Again},
			want: true,
		},
		"GID_is_zero_value": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid0},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: &gid0},
			want: true,
		},

		// Failing cases.
		"Fails_if_Empty_not_equals_other": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			want: false,
		},
		"Fails_if_Name_differs": {
			a:    GroupInfo{Name: "group1", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group2", UGID: "u1", GID: &gid1},
			want: false,
		},
		"Fails_if_UGID_differs": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group", UGID: "u2", GID: &gid1},
			want: false,
		},
		"Fails_if_GID_one_nil_one_non-nil_a_nil": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: nil},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			want: false,
		},
		"Fails_if_GID_one_nil_one_non-nil_b_nil": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: nil},
			want: false,
		},
		"Fails_if_GID_non-nil_values_differ": {
			a:    GroupInfo{Name: "group", UGID: "u1", GID: &gid1},
			b:    GroupInfo{Name: "group", UGID: "u1", GID: &gid2},
			want: false,
		},
		"Fails_if_All_fields_differ": {
			a:    GroupInfo{Name: "groupA", UGID: "uA", GID: &gid1},
			b:    GroupInfo{Name: "groupB", UGID: "uB", GID: &gid2},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.a.Equals(tc.b)
			require.Equal(t, tc.want, got)
		})
	}
}
