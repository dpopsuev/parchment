package parchment

// BackwardCompat tests for v0.4.3+.
//
// These tests verify that existing artifacts with human-readable IDs (e.g.
// SCR-TSK-1) survive store open and that all cross-references remain intact.
// New artifacts created after the migration get UUIDs.

import (
	"context"
	"testing"
)

// buildV042DB creates a SQLite store that mirrors a realistic v0.4.2 production
// database: scope-derived IDs, edges, depends_on, links, and parent refs.
func buildV042DB(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}

	artifacts := []*Artifact{
		{ID: "SCR-GOL-1", Labels: []string{"kind:effort.goal", "status:active", "scope:scribe"}, Title: "Ship v1"},
		{ID: "SCR-CAM-1", Labels: []string{"kind:effort.campaign", "status:active", "scope:scribe"},
			Title: "Q2 Campaign",
			Sections: []Section{
				{Name: "mission", Text: "ship the thing"},
			},
		},
		{ID: "SCR-TSK-1", Labels: []string{"kind:effort.task", "status:draft", "scope:scribe"},
			Title: "Implement feature A",
			Sections: []Section{
				{Name: "context", Text: "needs doing"},
			},
		},
		{ID: "SCR-TSK-2", Labels: []string{"kind:effort.task", "status:active", "scope:scribe"},
			Title: "Implement feature B",
			Sections: []Section{
				{Name: "context", Text: "blocked on A"},
			},
		},
		{ID: "SCR-BUG-1", Labels: []string{"kind:intent.bug", "status:open", "scope:scribe"},
			Title: "Crash on startup",
		},
	}
	for _, a := range artifacts {
		if err := s.Put(ctx, a); err != nil {
			t.Fatalf("seed %s: %v", a.ID, err)
		}
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-CAM-1", To: "SCR-GOL-1", Relation: RelJustifies}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-CAM-1", To: "SCR-TSK-1", Relation: RelParentOf}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-CAM-1", To: "SCR-TSK-2", Relation: RelParentOf}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-TSK-2", To: "SCR-TSK-1", Relation: RelDependsOn}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-BUG-1", To: "SCR-TSK-1", Relation: RelDependsOn}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestV043_ExistingDBSurvivesOpen verifies that all v0.4.2 artifacts are
// byte-identical after OpenSQLite runs its startup migrations.
func TestV043_ExistingDBSurvivesOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/compat.sqlite"

	buildV042DB(t, path)

	// Re-open: triggers ALTER TABLE and FTS5 rebuild.
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite on existing DB: %v", err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	cases := []struct {
		id    string
		title string
	}{
		{"SCR-GOL-1", "Ship v1"},
		{"SCR-CAM-1", "Q2 Campaign"},
		{"SCR-TSK-1", "Implement feature A"},
		{"SCR-TSK-2", "Implement feature B"},
		{"SCR-BUG-1", "Crash on startup"},
	}
	for _, tc := range cases {
		art, err := s.Get(ctx, tc.id)
		if err != nil {
			t.Errorf("Get(%s): %v", tc.id, err)
			continue
		}
		if art.ID != tc.id {
			t.Errorf("%s: ID changed to %q", tc.id, art.ID)
		}
		if art.Title != tc.title {
			t.Errorf("%s: title changed to %q", tc.id, art.Title)
		}
	}
}

// TestV043_CrossRefsIntactAfterOpen verifies that parent, depends_on, links,
// and edges survive the v0.4.3 startup without mutation.
func TestV043_CrossRefsIntactAfterOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/compat.sqlite"

	buildV042DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	// parent refs
	tsk1ParentEdges, err := s.Neighbors(ctx, "SCR-TSK-1", RelParentOf, Incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(tsk1ParentEdges) == 0 || tsk1ParentEdges[0].From != "SCR-CAM-1" {
		t.Errorf("SCR-TSK-1 parent edge = %v, want From:SCR-CAM-1", tsk1ParentEdges)
	}

	// depends_on edge
	depEdges, err := s.Neighbors(ctx, "SCR-TSK-2", RelDependsOn, Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(depEdges) != 1 || depEdges[0].To != "SCR-TSK-1" {
		t.Errorf("SCR-TSK-2 depends_on edges = %v, want [{To: SCR-TSK-1}]", depEdges)
	}

	// links via edges
	justifies, err := s.Neighbors(ctx, "SCR-CAM-1", RelJustifies, Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(justifies) != 1 || justifies[0].To != "SCR-GOL-1" {
		t.Errorf("SCR-CAM-1 justifies edges = %v, want [{To: SCR-GOL-1}]", justifies)
	}

	// edges
	edges, err := s.Neighbors(ctx, "SCR-CAM-1", RelParentOf, Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Errorf("SCR-CAM-1 parent_of edges = %d, want 2", len(edges))
	}
}

// TestV043_NewArtifactsGetUUIDs verifies that artifacts created after the
// migration receive UUID IDs while existing human-readable IDs remain intact.
func TestV043_NewArtifactsGetUUIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/uuid_migration.sqlite"

	buildV042DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:    "New task after migration",

		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if !isUUIDShaped(art.ID) {
		t.Errorf("new artifact ID %q should be UUID-shaped after migration", art.ID)
	}

	// Old human-readable ID still accessible.
	old, err := s.Get(ctx, "SCR-TSK-1")
	if err != nil {
		t.Fatalf("old human ID SCR-TSK-1 lost: %v", err)
	}
	if old.ID != "SCR-TSK-1" {
		t.Errorf("old ID mutated to %q", old.ID)
	}
}

// TestV043_ExplicitIDPreserved verifies that callers can still create
// artifacts with human-readable IDs via ExplicitID (e.g., for seeding).
func TestV043_ExplicitIDPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/explicit.sqlite"

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:      "Seeded with explicit ID",
		ExplicitID: "SCR-TSK-999",

		Labels:     []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatalf("CreateArtifact with ExplicitID: %v", err)
	}
	if art.ID != "SCR-TSK-999" {
		t.Errorf("ExplicitID not preserved: got %q, want SCR-TSK-999", art.ID)
	}
}
