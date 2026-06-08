package parchment_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestMigrateSystemLabels_BackfillsScopeLabel(t *testing.T) {
	// Given: artifact with Scope field set but no scope: label.
	// When:  MigrateSystemLabels runs.
	// Then:  scope:X label is added.
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{
		ID:     "TST-1",
		Labels: []string{"kind:task", "status:active", "custom"},
		Scope:  "myproj",
		Title:  "old artifact",
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	if err := parchment.MigrateSystemLabels(ctx, s); err != nil {
		t.Fatalf("MigrateSystemLabels: %v", err)
	}

	got, err := s.Get(ctx, "TST-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind:task", "scope:myproj", "status:active", "custom"} {
		if !slices.Contains(got.Labels, want) {
			t.Errorf("missing label %q in %v", want, got.Labels)
		}
	}
}

func TestMigrateSystemLabels_Idempotent(t *testing.T) {
	// Given: MigrateSystemLabels run twice on the same artifact.
	// Then:  no label is duplicated.
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{
		ID:     "TST-2",
		Labels: []string{"kind:task", "status:active"},
		Scope:  "myproj",
		Title:  "idempotent test",
	}
	_ = s.Put(ctx, art)
	_ = parchment.MigrateSystemLabels(ctx, s)
	_ = parchment.MigrateSystemLabels(ctx, s)

	got, _ := s.Get(ctx, "TST-2")
	count := 0
	for _, l := range got.Labels {
		if l == "scope:myproj" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("scope:myproj duplicated: appears %d times in %v", count, got.Labels)
	}
}

func TestMigrateSystemLabels_SkipsAlreadyLabelled(t *testing.T) {
	// Given: artifact already has all system labels.
	// Then:  no extra labels are added.
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{
		ID:     "TST-3",
		Labels: []string{"kind:note", "scope:wiki", "status:active"},
		Scope:  "wiki",
		Title:  "already labeled",
	}
	_ = s.Put(ctx, art)

	if err := parchment.MigrateSystemLabels(ctx, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := s.Get(ctx, "TST-3")
	if len(got.Labels) != 3 {
		t.Errorf("expected 3 labels unchanged, got %d: %v", len(got.Labels), got.Labels)
	}
}

func TestMigrateSystemLabels_Priority(t *testing.T) {
	// Given: artifact with Priority field set but no priority: label.
	// Then:  priority:X label added.
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{
		ID:       "TST-4",
		Labels:   []string{"kind:task", "status:draft"},
		Scope:    "scribe",
		Priority: "high",
		Title:    "priority test",
	}
	_ = s.Put(ctx, art)
	_ = parchment.MigrateSystemLabels(ctx, s)

	got, _ := s.Get(ctx, "TST-4")
	if !slices.Contains(got.Labels, "priority:high") {
		t.Errorf("missing priority:high in %v", got.Labels)
	}
}

func TestMigrateSystemLabels_SQLite_Idempotent(t *testing.T) {
	// Given: a real SQLite store with artifacts pre-populated via Put.
	// When:  MigrateSystemLabels runs twice.
	// Then:  labels are correct and no duplicates appear.
	t.Parallel()
	s, err := parchment.OpenSQLite(t.TempDir() + "/migrate.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	ctx := context.Background()
	arts := []*parchment.Artifact{
		{ID: "SQL-1", Labels: []string{"kind:task", "status:active"}, Scope: "myproj", Title: "a"},
		{ID: "SQL-2", Labels: []string{"kind:spec", "status:draft"}, Scope: "myproj", Title: "b"},
		{ID: "SQL-3", Labels: []string{"kind:task", "scope:myproj", "status:draft"}, Scope: "myproj", Title: "already"},
	}
	for _, art := range arts {
		if err := s.Put(ctx, art); err != nil {
			t.Fatalf("put %s: %v", art.ID, err)
		}
	}

	if err := parchment.MigrateSystemLabels(ctx, s); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := parchment.MigrateSystemLabels(ctx, s); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, id := range []string{"SQL-1", "SQL-2", "SQL-3"} {
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		counts := map[string]int{}
		for _, l := range got.Labels {
			counts[l]++
		}
		for label, count := range counts {
			if count > 1 {
				t.Errorf("%s: label %q appears %d times (duplicate)", id, label, count)
			}
		}
		if !slices.Contains(got.Labels, "kind:"+got.ResolvedKind()) {
			t.Errorf("%s: missing kind:%s in %v", id, got.ResolvedKind(), got.Labels)
		}
	}
}
