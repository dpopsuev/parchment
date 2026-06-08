package parchment_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestArtifact_ScanHydration_KindFromLabel(t *testing.T) {
	// Given: an artifact stored in SQLite with Kind="" but label kind:task.
	// When:  Get reads it back.
	// Then:  art.Kind == "task" (hydrated from label by scan).
	// This is the seam that protects all callers reading art.Kind directly.
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "hydrate.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	ctx := context.Background()
	art := &parchment.Artifact{
		ID: "TEST-1", Kind: "", Scope: "test", Status: "active",
		Title: "hydration test",
		Labels: []string{"kind:task", "scope:test", "status:active"},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "TEST-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.Kind != "task" {
		t.Errorf("scan hydration failed: Kind=%q, want 'task' from label", got.Kind)
	}
	if got.Scope != "test" {
		t.Errorf("scan hydration failed: Scope=%q, want 'test' from label", got.Scope)
	}
	if got.Status != "active" {
		t.Errorf("scan hydration failed: Status=%q, want 'active' from label", got.Status)
	}
}

func TestArtifact_ScanHydration_ListAlsoHydrates(t *testing.T) {
	// Given: three artifacts with Kind="" but kind: labels.
	// When:  List reads them back.
	// Then:  all have Kind populated from labels.
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
			Kind:   "",
			Scope:  "test",
			Status: "draft",
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
		if art.Kind == "" {
			t.Errorf("artifact %s: Kind not hydrated from label (labels=%v)", art.ID, art.Labels)
		}
		expectedKind := ""
		for _, l := range art.Labels {
			if len(l) > 5 && l[:5] == "kind:" {
				expectedKind = l[5:]
				break
			}
		}
		if art.Kind != expectedKind {
			t.Errorf("artifact %s: Kind=%q, want %q from label", art.ID, art.Kind, expectedKind)
		}
	}
}

func TestCreateArtifact_KindFromLabelIsCanonical(t *testing.T) {
	// Given: CreateArtifact called with Kind="task".
	// When:  the returned artifact is inspected.
	// Then:  Labels contains "kind:task" AND art.Kind == art.ResolvedKind().
	// Verifies that Kind is label-derived, not set as a standalone field.
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "label canonical test",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(art.Labels, "kind:task") {
		t.Errorf("kind:task missing from labels: %v", art.Labels)
	}
	if art.Kind != art.ResolvedKind() {
		t.Errorf("Kind field %q != ResolvedKind() %q — field not derived from label", art.Kind, art.ResolvedKind())
	}
}
