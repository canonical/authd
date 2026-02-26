package layouts_test

import (
	"fmt"
	"testing"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/stretchr/testify/require"
)

func TestOptionalItems(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		items []string

		want string
	}{
		"Optional_empty_item": {want: layouts.Optional + ":"},
		"Optional_with_one_item": {
			items: []string{entries.Chars},
			want:  layouts.Optional + ":" + entries.Chars,
		},
		"Optional_with_multiple_items": {
			items: []string{entries.Chars, entries.DigitsPassword},
			want:  layouts.Optional + ":" + entries.Chars + "," + entries.DigitsPassword,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, layouts.OptionalItems(tc.items...),
				"Unexpected optional entries item")
		})
	}
}

func TestRequiredItems(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		items []string

		want string
	}{
		"Required_empty_item": {want: layouts.Required + ":"},
		"Required_with_one_item": {
			items: []string{entries.Chars},
			want:  layouts.Required + ":" + entries.Chars,
		},
		"Required_with_multiple_items": {
			items: []string{entries.Chars, entries.DigitsPassword},
			want:  layouts.Required + ":" + entries.Chars + "," + entries.DigitsPassword,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, layouts.RequiredItems(tc.items...),
				"Unexpected required entries item")
		})
	}
}

func TestParseItems(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		items string

		wantKind  string
		wantItems []string
	}{
		"Required_empty_item": {},
		"Required_with_one_item": {
			items:     layouts.Required + ":" + entries.Chars,
			wantKind:  layouts.Required,
			wantItems: []string{entries.Chars},
		},
		"Required_with_multiple_items": {
			items:     layouts.Required + ":" + entries.Chars + ", " + entries.DigitsPassword,
			wantKind:  layouts.Required,
			wantItems: []string{entries.Chars, entries.DigitsPassword},
		},
		"Required_with_booleans": {
			items:     layouts.RequiredWithBooleans,
			wantKind:  layouts.Required,
			wantItems: []string{layouts.True, layouts.False},
		},
		"Optional_empty_item": {},
		"Optional_with_one_item": {
			items:     layouts.Optional + ":" + entries.CharsPassword,
			wantKind:  layouts.Optional,
			wantItems: []string{entries.CharsPassword},
		},
		"Optional_with_multiple_items": {
			items:     layouts.Optional + ":" + entries.Digits + " , " + entries.Chars + "," + entries.DigitsPassword,
			wantKind:  layouts.Optional,
			wantItems: []string{entries.Digits, entries.Chars, entries.DigitsPassword},
		},
		"Optional_with_booleans": {
			items:     layouts.OptionalWithBooleans,
			wantKind:  layouts.Optional,
			wantItems: []string{layouts.True, layouts.False},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kind, items := layouts.ParseItems(tc.items)
			require.Equal(t, tc.wantKind, kind, "Unexpected items kind")
			require.Equal(t, tc.wantItems, items, "Unexpected items")
		})
	}
}

func TestOptionalWithBooleans(t *testing.T) {
	t.Parallel()

	require.Equal(t, layouts.OptionalWithBooleans, fmt.Sprintf("%s:%s,%s",
		layouts.Optional, layouts.True, layouts.False), "Unexpected value")
}

func TestRequiredWithBooleans(t *testing.T) {
	t.Parallel()

	require.Equal(t, layouts.RequiredWithBooleans, fmt.Sprintf("%s:%s,%s",
		layouts.Required, layouts.True, layouts.False), "Unexpected value")
}
