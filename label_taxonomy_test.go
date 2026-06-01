package parchment_test

import (
	"context"
	"sort"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestExpandLabels_MemoryStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := parchment.NewMemoryStore()

	_ = s.PutLabelParent(ctx, "lang:go", "lang")
	_ = s.PutLabelParent(ctx, "lang:ts", "lang")
	_ = s.PutLabelParent(ctx, "lang", "behavioral")

	got, err := s.ExpandLabels(ctx, []string{"lang:go"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"behavioral", "lang", "lang:go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestExpandLabels_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := parchment.OpenSQLite(t.TempDir() + "/taxonomy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.PutLabelParent(ctx, "lang:go", "lang")
	_ = s.PutLabelParent(ctx, "lang", "behavioral")

	got, err := s.ExpandLabels(ctx, []string{"lang:go", "always"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)

	has := func(label string) bool {
		for _, l := range got {
			if l == label {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"lang:go", "lang", "behavioral", "always"} {
		if !has(want) {
			t.Errorf("expected label %q in expanded set %v", want, got)
		}
	}
}

func TestSeedLabelTaxonomy_SeedsDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := parchment.NewMemoryStore()

	parchment.SeedLabelTaxonomy(ctx, s)

	got, err := s.ExpandLabels(ctx, []string{"lang:go"})
	if err != nil {
		t.Fatal(err)
	}
	has := func(label string) bool {
		for _, l := range got {
			if l == label {
				return true
			}
		}
		return false
	}
	if !has("lang") {
		t.Errorf("lang:go should expand to include 'lang', got %v", got)
	}
}
