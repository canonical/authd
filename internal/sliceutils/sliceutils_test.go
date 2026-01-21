package sliceutils_test

import (
	"slices"
	"testing"

	"github.com/canonical/authd/internal/sliceutils"
	"github.com/stretchr/testify/require"
)

func TestDifference(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b, want []int
	}{
		"test_difference_between_two_slices": {
			a:    []int{1, 2, 3, 4, 5},
			b:    []int{3, 4, 5, 6, 7},
			want: []int{1, 2},
		},
		"test_difference_between_an_empty_slice_and_a_non-empty_slice": {
			a:    []int{},
			b:    []int{3, 4, 5, 6, 7},
			want: []int(nil),
		},
		"test_difference_between_a_non-empty_slice_and_an_empty_slice": {
			a:    []int{1, 2, 3, 4, 5},
			b:    []int{},
			want: []int{1, 2, 3, 4, 5},
		},
		"test_difference_between_two_empty_slices": {
			a:    []int{},
			b:    []int{},
			want: []int(nil),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := sliceutils.Difference(tc.a, tc.b)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDifferenceFunc(t *testing.T) {
	t.Parallel()

	type notComparable struct {
		i  int
		ii []int
	}

	notComparableCompareFunc := func(a notComparable, b notComparable) bool {
		return a.i == b.i && slices.Equal(a.ii, b.ii)
	}

	tests := map[string]struct {
		a, b, want []notComparable
	}{
		"test_difference_between_two_slices": {
			a:    []notComparable{{i: 1}, {i: 2}, {i: 3}, {i: 4}, {i: 5, ii: []int{1, 2}}},
			b:    []notComparable{{i: 3}, {i: 4}, {i: 5}, {i: 6}, {i: 7}},
			want: []notComparable{{i: 1}, {i: 2}, {i: 5, ii: []int{1, 2}}},
		},
		"test_difference_between_an_empty_slice_and_a_non-empty_slice": {
			a:    []notComparable{},
			b:    []notComparable{{i: 3}, {i: 4}, {i: 5}, {i: 6}, {i: 7}},
			want: []notComparable(nil),
		},
		"test_difference_between_a_non-empty_slice_and_an_empty_slice": {
			a:    []notComparable{{i: 1}, {i: 2}, {i: 3}, {i: 4}, {i: 5}},
			b:    []notComparable{},
			want: []notComparable{{i: 1}, {i: 2}, {i: 3}, {i: 4}, {i: 5}},
		},
		"test_difference_between_two_empty_slices": {
			a:    []notComparable{},
			b:    []notComparable{},
			want: []notComparable(nil),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := sliceutils.DifferenceFunc(tc.a, tc.b, notComparableCompareFunc)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestIntersection(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b, want []int
	}{
		"test_intersection_between_two_slices": {
			a:    []int{1, 2, 3, 4, 5},
			b:    []int{3, 4, 5, 6, 7},
			want: []int{3, 4, 5},
		},
		"test_intersection_between_an_empty_slice_and_a_non-empty_slice": {
			a:    []int{},
			b:    []int{3, 4, 5, 6, 7},
			want: []int(nil),
		},
		"test_intersection_between_a_non-empty_slice_and_an_empty_slice": {
			a:    []int{1, 2, 3, 4, 5},
			b:    []int{},
			want: []int(nil),
		},
		"test_intersection_between_two_empty_slices": {
			a:    []int{},
			b:    []int{},
			want: []int(nil),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := sliceutils.Intersection(tc.a, tc.b)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMap(t *testing.T) {
	t.Parallel()

	type intStruct struct {
		i int
	}

	tests := map[string]struct {
		a    []intStruct
		want []int
	}{
		"test_mapping_a_slice": {
			a:    []intStruct{{1}, {2}, {3}, {4}, {5}},
			want: []int{1, 2, 3, 4, 5},
		},
		"test_mapping_an empty_slice": {
			a:    []intStruct{},
			want: []int{},
		},
		"test_mapping_a_nil_slice": {
			a:    []intStruct(nil),
			want: []int(nil),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := sliceutils.Map(tc.a, func(s intStruct) int { return s.i })
			require.Equal(t, tc.want, got)
		})
	}
}

func TestEqualsContentFunc(t *testing.T) {
	t.Parallel()

	type notComparable struct {
		i  int
		ii []int
	}

	notComparableCompareFunc := func(a, b notComparable) bool {
		return a.i == b.i && sliceutils.EqualContent(a.ii, b.ii)
	}

	tests := map[string]struct {
		a, b []notComparable
		want bool
	}{
		"equal_slices_same_order": {
			a:    []notComparable{{i: 1}, {i: 2}, {i: 3}},
			b:    []notComparable{{i: 1}, {i: 2}, {i: 3}},
			want: true,
		},
		"equal_slices_different_order": {
			a:    []notComparable{{i: 1}, {i: 2}, {i: 3}},
			b:    []notComparable{{i: 3}, {i: 1}, {i: 2}},
			want: true,
		},
		"equal_with_nested_slices": {
			a:    []notComparable{{i: 1, ii: []int{1, 2}}, {i: 2, ii: []int{3}}},
			b:    []notComparable{{i: 2, ii: []int{3}}, {i: 1, ii: []int{1, 2}}},
			want: true,
		},
		"equal_with_nested_slices_with_different_order": {
			a:    []notComparable{{i: 1, ii: []int{1, 2}}, {i: 2, ii: []int{3}}},
			b:    []notComparable{{i: 2, ii: []int{3}}, {i: 1, ii: []int{2, 1}}},
			want: true,
		},
		"both_empty": {
			a:    []notComparable{},
			b:    []notComparable{},
			want: true,
		},
		"both_nil": {
			a:    nil,
			b:    nil,
			want: true,
		},
		"nil_and_empty": {
			a:    nil,
			b:    []notComparable{},
			want: true,
		},
		"not_equal_different_lengths": {
			a:    []notComparable{{i: 1}, {i: 2}},
			b:    []notComparable{{i: 1}, {i: 2}, {i: 3}},
			want: false,
		},
		"not_equal_different_content": {
			a:    []notComparable{{i: 1}, {i: 2}, {i: 3}},
			b:    []notComparable{{i: 1}, {i: 2}, {i: 4}},
			want: false,
		},
		"not_equal_with_nested_slices_with_different_content": {
			a:    []notComparable{{i: 1, ii: []int{1, 2}}, {i: 2, ii: []int{3}}},
			b:    []notComparable{{i: 2, ii: []int{4}}, {i: 1, ii: []int{2, 1}}},
			want: false,
		},
		"one_empty_one_nonempty": {
			a:    []notComparable{},
			b:    []notComparable{{i: 1}},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := sliceutils.EqualContentFunc(tc.a, tc.b, notComparableCompareFunc)
			require.Equal(t, tc.want, got)
		})
	}
}
