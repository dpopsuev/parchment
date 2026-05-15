package parchment

// BackwardCompat tests for v0.4.3.
//
// These tests answer the question: "does upgrading to v0.4.3 destroy or alter
// an existing v0.4.2 database?" They must pass forever — never delete them.
//
// Coverage:
//  1. All existing artifacts survive OpenSQLite (ALTER TABLE, reseed, FTS5 rebuild).
//  2. IDs, titles, edges, depends_on, links, parent are byte-identical after open.
//  3. reseedScopedSequences skips UUID-shaped IDs without misparsing them.
//  4. Without id_format=uuid, new artifact creation still produces scope-derived IDs.
//  5. Sequence counters are bumped correctly (no collision with existing IDs).

import (
	"context"
	"strconv"
	"strings"
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
		{UID: "u1", ID: "SCR-GOL-1", Kind: "goal", Scope: "scribe", Status: "active", Title: "Ship v1"},
		{UID: "u2", ID: "SCR-CAM-1", Kind: "campaign", Scope: "scribe", Status: "active",
			Title: "Q2 Campaign",
			Links: map[string][]string{RelJustifies: {"SCR-GOL-1"}},
			Sections: []Section{
				{Name: "mission", Text: "ship the thing"},
			},
		},
		{UID: "u3", ID: "SCR-TSK-1", Kind: "task", Scope: "scribe", Status: "draft",
			Title:  "Implement feature A",
			Parent: "SCR-CAM-1",
			Sections: []Section{
				{Name: "context", Text: "needs doing"},
			},
		},
		{UID: "u4", ID: "SCR-TSK-2", Kind: "task", Scope: "scribe", Status: "active",
			Title:     "Implement feature B",
			Parent:    "SCR-CAM-1",
			DependsOn: []string{"SCR-TSK-1"},
			Sections: []Section{
				{Name: "context", Text: "blocked on A"},
			},
		},
		{UID: "u5", ID: "SCR-BUG-1", Kind: "bug", Scope: "scribe", Status: "open",
			Title: "Crash on startup",
			Links: map[string][]string{RelDependsOn: {"SCR-TSK-1"}},
		},
	}
	for _, a := range artifacts {
		if err := s.Put(ctx, a); err != nil {
			t.Fatalf("seed %s: %v", a.ID, err)
		}
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-CAM-1", To: "SCR-TSK-1", Relation: RelParentOf}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, Edge{From: "SCR-CAM-1", To: "SCR-TSK-2", Relation: RelParentOf}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScopeKey(ctx, "scribe", "SCR", false); err != nil {
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

	// Re-open: triggers ALTER TABLE, reseedScopedSequences, FTS5 rebuild.
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
	tsk1, err := s.Get(ctx, "SCR-TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if tsk1.Parent != "SCR-CAM-1" {
		t.Errorf("SCR-TSK-1.Parent = %q, want SCR-CAM-1", tsk1.Parent)
	}

	// depends_on
	tsk2, err := s.Get(ctx, "SCR-TSK-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(tsk2.DependsOn) != 1 || tsk2.DependsOn[0] != "SCR-TSK-1" {
		t.Errorf("SCR-TSK-2.DependsOn = %v, want [SCR-TSK-1]", tsk2.DependsOn)
	}

	// links
	cam, err := s.Get(ctx, "SCR-CAM-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cam.Links[RelJustifies]) != 1 || cam.Links[RelJustifies][0] != "SCR-GOL-1" {
		t.Errorf("SCR-CAM-1.Links[justifies] = %v, want [SCR-GOL-1]", cam.Links[RelJustifies])
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

// TestV043_ReseedSkipsUUIDs verifies that reseedScopedSequences does not
// misparse UUID-shaped IDs as scope-key/kind-code/seq triples.
// If it did, it could corrupt sequence counters.
func TestV043_ReseedSkipsUUIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/uuid_reseed.sqlite"

	// Seed a DB that has a mix: scope-derived IDs and UUID-shaped IDs.
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetScopeKey(ctx, "scribe", "SCR", false); err != nil {
		t.Fatal(err)
	}
	_ = s.Put(ctx, &Artifact{UID: "u1", ID: "SCR-TSK-91", Kind: "task", Scope: "scribe", Status: "draft", Title: "A"})
	_ = s.Put(ctx, &Artifact{UID: "u2", ID: GenerateUUID(), Kind: "task", Scope: "scribe", Status: "draft", Title: "B"})
	_ = s.Put(ctx, &Artifact{UID: "u3", ID: GenerateUUID(), Kind: "task", Scope: "scribe", Status: "draft", Title: "C"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open triggers reseedScopedSequences.
	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close() //nolint:errcheck // deferred close in test

	// Next scoped ID must be >= 92 (one past the highest known seq=91).
	// It must NOT be something derived from misparsing a UUID.
	nextID, err := s2.NextScopedID(ctx, "SCR", "TSK")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(nextID, "SCR-TSK-") {
		t.Fatalf("unexpected ID format: %q", nextID)
	}
	// Parse the sequence number out and verify it's >= 92.
	seqStr := strings.TrimPrefix(nextID, "SCR-TSK-")
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		t.Fatalf("NextScopedID produced non-numeric sequence in %q: %v", nextID, err)
	}
	if seq < 92 {
		t.Errorf("next seq = %d, want >= 92 (reseed did not advance past existing SCR-TSK-91)", seq)
	}
}

// TestV043_DefaultFormatUnchanged verifies that without id_format=uuid the
// Protocol still generates scope-derived IDs for new artifacts.
// Uses PresetScoped() as the IDTemplate, matching how scribe.config always
// configures the Protocol in production.
func TestV043_DefaultFormatUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/default.sqlite"

	buildV042DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	// Mirror the real scribe config: IDTemplate=PresetScoped(), no IDFormat.
	tpl := PresetScoped()
	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{IDTemplate: &tpl})

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Kind:     "task",
		Title:    "New task after upgrade",
		Scope:    "scribe",
		Sections: []Section{{Name: "context", Text: "ctx"}},
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if IsUUIDShaped(art.ID) {
		t.Errorf("default format produced UUID %q — expected scope-derived ID", art.ID)
	}
	if !strings.HasPrefix(art.ID, "SCR-") {
		t.Errorf("new artifact ID %q does not have SCR- prefix", art.ID)
	}
}

// TestV043_NoCollisionWithExistingIDs verifies that the sequence counter is
// advanced past the highest existing ID so new artifacts never collide.
func TestV043_NoCollisionWithExistingIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/nocollide.sqlite"

	buildV042DB(t, path) // seeds SCR-TSK-1 and SCR-TSK-2

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	tpl := PresetScoped()
	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{IDTemplate: &tpl})

	seen := map[string]bool{
		"SCR-TSK-1": true,
		"SCR-TSK-2": true,
	}
	for range 5 {
		art, err := proto.CreateArtifact(ctx, CreateInput{
			Kind:     "task",
			Title:    "Collision check",
			Scope:    "scribe",
			Sections: []Section{{Name: "context", Text: "ctx"}},
		})
		if err != nil {
			t.Fatalf("CreateArtifact: %v", err)
		}
		if seen[art.ID] {
			t.Errorf("ID collision: %q already exists", art.ID)
		}
		seen[art.ID] = true
	}
}
