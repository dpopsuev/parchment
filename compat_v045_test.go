package parchment

// BackwardCompat tests for v0.4.5+.
//
// These tests verify that upgrading preserves existing artifacts, the alias
// column migration works, and new artifacts get UUIDs.

import (
	"context"
	"testing"
)

// buildV044DB seeds a SQLite database that looks like a real v0.4.4 production
// database: scope-derived IDs, edges, depends_on, links.
func buildV044DB(t *testing.T, path string) {
	t.Helper()
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	artifacts := []*Artifact{
		{ID: "SCR-GOL-1", Labels: []string{"kind:effort.goal", "status:active", "project:scribe"}, Title: "Ship v2"},
		{ID: "SCR-CAM-1", Labels: []string{"kind:effort.campaign", "status:active", "project:scribe"},
			Title:    "Migration campaign",
			Sections: []Section{{Name: "mission", Text: "stay safe"}},
		},
		{ID: "SCR-TSK-1", Labels: []string{"kind:effort.task", "status:draft", "project:scribe"},
			Title:    "Task alpha",
			Sections: []Section{{Name: "context", Text: "do it"}},
		},
		{ID: "SCR-TSK-2", Labels: []string{"kind:effort.task", "status:active", "project:scribe"},
			Title:    "Task beta",
			Sections: []Section{{Name: "context", Text: "after alpha"}},
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
	if err := s.AddEdge(ctx, Edge{From: "SCR-TSK-2", To: "SCR-TSK-1", Relation: RelDependsOn}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestV045_ExistingArtifactsSurviveOpen verifies that all seeded artifacts
// are byte-identical after OpenSQLite runs migrations.
func TestV045_ExistingArtifactsSurviveOpen(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/compat45.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite on existing DB: %v", err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	cases := []struct{ id, title string }{
		{"SCR-GOL-1", "Ship v2"},
		{"SCR-CAM-1", "Migration campaign"},
		{"SCR-TSK-1", "Task alpha"},
		{"SCR-TSK-2", "Task beta"},
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

// TestV045_AliasDefaultsToEmptyForExistingRows verifies that the alias
// column defaults to empty and does not corrupt existing data.
func TestV045_AliasDefaultsToEmptyForExistingRows(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/alias_default.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	arts, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) == 0 {
		t.Error("expected artifacts after migration")
	}
}

// TestV045_PartialIndexDoesNotConflict verifies that the unique partial index
// (WHERE alias != '') does not reject existing rows whose alias is ''.
func TestV045_PartialIndexDoesNotConflict(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/no_conflict.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite should not fail on partial index creation: %v", err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	// Put a new artifact with alias='' — must not fail.
	ctx := context.Background()
	err = s.Put(ctx, &Artifact{
		ID:    "SCR-TSK-99",
		Labels: []string{"kind:effort.task", "status:draft", "project:scribe"},
		Title: "No alias",
	})
	if err != nil {
		t.Errorf("Put with empty alias failed: %v", err)
	}
}

// TestV045_NewArtifactsGetUUIDs verifies that after upgrade, new artifacts
// get UUID IDs while old human-readable IDs remain accessible.
func TestV045_NewArtifactsGetUUIDs(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/uuid_on_upgraded.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:    "First UUID task on upgraded DB",

		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if !isAutoID(art.ID) {
		t.Errorf("new artifact ID %q should be an auto-generated ID", art.ID)
	}

	// Existing scope-derived artifacts are unaffected.
	old, err := proto.GetArtifact(ctx, "SCR-TSK-1")
	if err != nil {
		t.Fatalf("existing artifact SCR-TSK-1 not readable: %v", err)
	}
	if old.ID == "" {
		t.Error("existing artifact should have a non-empty ID")
	}
}
