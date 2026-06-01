package parchment_test

import (
	"sort"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestExpandLabels_Hierarchy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input []string
		want  []string
	}{
		{
			input: []string{"lang.go"},
			want:  []string{"lang", "lang.go"},
		},
		{
			input: []string{"lang.go.test"},
			want:  []string{"lang", "lang.go", "lang.go.test"},
		},
		{
			input: []string{"refactoring"},
			want:  []string{"refactoring"},
		},
		{
			input: []string{"always"},
			want:  []string{"always"},
		},
		{
			// Colon is not a separator — source:github.com is atomic.
			input: []string{"source:github.com"},
			want:  []string{"source:github.com"},
		},
		{
			// Multiple inputs, deduplication.
			input: []string{"lang.go", "lang.ts"},
			want:  []string{"lang", "lang.go", "lang.ts"},
		},
		{
			// Overlapping ancestors are deduplicated.
			input: []string{"lang.go.test", "lang.go"},
			want:  []string{"lang", "lang.go", "lang.go.test"},
		},
		{
			input: []string{},
			want:  []string{},
		},
	}

	for _, tc := range cases {
		got := parchment.ExpandLabels(tc.input)
		sort.Strings(got)
		sort.Strings(tc.want)
		if len(got) != len(tc.want) {
			t.Errorf("ExpandLabels(%v) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("ExpandLabels(%v)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}
