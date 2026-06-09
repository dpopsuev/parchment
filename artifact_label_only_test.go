package parchment_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestArtifact_ScanHydration_KindFromLabel(t *testing.T) {
	// Given: an artifact stored in SQLite with labels kind:task and status:active.
	// When:  Get reads it back.
	// Then:  ResolvedKind() == "task", ResolvedStatus() == "active".
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "hydrate.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	ctx := context.Background()
	art := &parchment.Artifact{
		ID:     "TEST-1",
		Title:  "hydration test",
		Labels: []string{"kind:task", "scope:test", "status:active"},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "TEST-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.ResolvedKind() != "task" {
		t.Errorf("scan hydration failed: ResolvedKind()=%q, want 'task' from label", got.ResolvedKind())
	}
	if got.Scope() != "test" {
		t.Errorf("scan hydration failed: Scope()=%q, want 'test' from label", got.Scope())
	}
	if got.ResolvedStatus() != "active" {
		t.Errorf("scan hydration failed: ResolvedStatus()=%q, want 'active' from label", got.ResolvedStatus())
	}
}

func TestArtifact_ScanHydration_ListAlsoHydrates(t *testing.T) {
	// Given: three artifacts with kind: labels.
	// When:  List reads them back.
	// Then:  all have ResolvedKind() populated from labels.
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "hydrate2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	ctx := context.Background()
	for i, kind := range []string{"task", "spec", "bug"} {
		art := &parchment.Artifact{
			ID:     "TEST-" + kind,
			Title:  "test " + kind,
			Labels: []string{"kind:" + kind, "scope:test", "status:draft"},
		}
		_ = i
		if err := s.Put(ctx, art); err != nil {
			t.Fatal(err)
		}
	}

	arts, err := s.List(ctx, parchment.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 3 {
		t.Fatalf("expected 3 artifacts, got %d", len(arts))
	}
	for _, art := range arts {
		if art.ResolvedKind() == "" {
			t.Errorf("artifact %s: ResolvedKind() empty (labels=%v)", art.ID, art.Labels)
		}
		expectedKind := ""
		for _, l := range art.Labels {
			if len(l) > 5 && l[:5] == "kind:" {
				expectedKind = l[5:]
				break
			}
		}
		if art.ResolvedKind() != expectedKind {
			t.Errorf("artifact %s: ResolvedKind()=%q, want %q from label", art.ID, art.ResolvedKind(), expectedKind)
		}
	}
}

func TestCreateArtifact_KindFromLabelIsCanonical(t *testing.T) {
	// Given: CreateArtifact called with Kind="task".
	// When:  the returned artifact is inspected.
	// Then:  Labels contains "kind:task" and ResolvedKind() == "task".
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Scope: "test", Title: "label canonical test",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(art.Labels, "kind:task") {
		t.Errorf("kind:task missing from labels: %v", art.Labels)
	}
	if art.ResolvedKind() != "task" {
		t.Errorf("ResolvedKind() = %q, want 'task'", art.ResolvedKind())
	}
}
