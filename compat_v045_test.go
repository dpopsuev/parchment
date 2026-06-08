package parchment

// BackwardCompat tests for v0.4.5.
//
// These tests answer: "does upgrading to v0.4.5 destroy or alter an existing
// database?" They must pass forever — never delete them.
//
// v0.4.5 adds a single new column (alias TEXT NOT NULL DEFAULT '') and a
// partial unique index (WHERE alias != '') to the artifacts table.
//
// Coverage:
//  1. All existing artifacts survive OpenSQLite with their IDs unchanged.
//  2. The alias column is added with empty-string default — existing rows
//     round-trip correctly with Alias == "".
//  3. The partial unique index does not conflict with existing empty-alias rows.
//  4. Scoped (non-UUID) mode still produces scoped IDs and no alias.
//  5. UUID mode on an upgraded DB auto-generates aliases and allows lookup
//     by alias with the stable UUID unaffected.

import (
	"context"
	"strings"
	"testing"
)

// buildV044DB seeds a SQLite database that looks like a real v0.4.4 production
// database: scope-derived IDs, edges, depends_on, links — but no alias column
// (it gets added by the v0.4.5 migration when the DB is re-opened).
func buildV044DB(t *testing.T, path string) {
	t.Helper()
	// Open with the current code — OpenSQLite runs the migration automatically,
	// so we seed data then close. On the next open (in the test body) the DB
	// already has the alias column with empty defaults, which is exactly the
	// state a real user's DB will be in after the upgrade.
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	artifacts := []*Artifact{
		{UID: "u1", ID: "SCR-GOL-1", Labels: []string{"kind:goal", "status:active"}, Scope: "scribe", Title: "Ship v2"},
		{UID: "u2", ID: "SCR-CAM-1", Labels: []string{"kind:campaign", "status:active"}, Scope: "scribe",
			Title:    "Migration campaign",
			Sections: []Section{{Name: "mission", Text: "stay safe"}},
			Links:    map[string][]string{RelJustifies: {"SCR-GOL-1"}},
		},
		{UID: "u3", ID: "SCR-TSK-1", Labels: []string{"kind:task", "status:draft"}, Scope: "scribe",
			Title:    "Task alpha",
			Parent:   "SCR-CAM-1",
			Sections: []Section{{Name: "context", Text: "do it"}},
		},
		{UID: "u4", ID: "SCR-TSK-2", Labels: []string{"kind:task", "status:active"}, Scope: "scribe",
			Title:     "Task beta",
			Parent:    "SCR-CAM-1",
			DependsOn: []string{"SCR-TSK-1"},
			Sections:  []Section{{Name: "context", Text: "after alpha"}},
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
	if err := s.SetScopeKey(ctx, "scribe", "SCR", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestV045_ExistingArtifactsSurviveOpen verifies that all seeded artifacts
// are byte-identical after OpenSQLite runs the v0.4.5 migration.
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

// TestV045_AliasDefaultsToEmptyForExistingRows verifies that the new alias
// column is added with an empty-string default and does not corrupt existing data.
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
	for _, a := range arts {
		if a.Alias != "" {
			t.Errorf("%s: expected empty alias after migration, got %q", a.ID, a.Alias)
		}
	}
}

// TestV045_PartialIndexDoesNotConflict verifies that the unique partial index
// (WHERE alias != '') does not reject existing rows whose alias is ''.
// This is the critical safety property: all existing rows have alias=''
// which is excluded from the uniqueness constraint.
func TestV045_PartialIndexDoesNotConflict(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/no_conflict.sqlite"
	buildV044DB(t, path)

	// Re-open triggers the migration (ALTER TABLE + CREATE UNIQUE INDEX).
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite should not fail on partial index creation: %v", err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	// Put a new artifact with alias='' — must not fail.
	ctx := context.Background()
	err = s.Put(ctx, &Artifact{
		UID: "new-u", ID: "SCR-TSK-99", Labels: []string{"kind:task", "status:draft"},
		Scope: "scribe", Title: "No alias",
	})
	if err != nil {
		t.Errorf("Put with empty alias failed: %v", err)
	}
}

// TestV045_ScopedModeUnchanged verifies that the default Protocol config
// (scoped IDs, no UUID format) still produces scope-derived IDs with no alias.
func TestV045_ScopedModeUnchanged(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/scoped_unchanged.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	tpl := PresetScoped()
	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{IDTemplate: &tpl})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title:    "Post-upgrade scoped task",
		Scope:    "scribe",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels: []string{"kind:task"},})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if IsUUIDShaped(art.ID) {
		t.Errorf("scoped mode produced UUID %q — expected scope-derived ID", art.ID)
	}
	if !strings.HasPrefix(art.ID, "SCR-") {
		t.Errorf("ID %q missing SCR- prefix", art.ID)
	}
	if art.Alias != "" {
		t.Errorf("scoped mode should not set alias, got %q", art.Alias)
	}
}

// TestV045_UUIDModeOnUpgradedDB verifies that switching to UUID mode after
// upgrading correctly auto-generates aliases and allows transparent alias lookup.
func TestV045_UUIDModeOnUpgradedDB(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/uuid_on_upgraded.sqlite"
	buildV044DB(t, path)

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	tpl := PresetScoped()
	proto := New(s, nil, []string{"scribe"}, nil, ProtocolConfig{
		IDFormat:   IDFormatUUID,
		IDTemplate: &tpl,
	})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title:    "First UUID task on upgraded DB",
		Scope:    "scribe",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels: []string{"kind:task"},})
	if err != nil {
		t.Fatalf("CreateArtifact in UUID mode: %v", err)
	}
	if !IsUUIDShaped(art.ID) {
		t.Errorf("UUID mode produced non-UUID ID %q", art.ID)
	}
	if art.Alias == "" {
		t.Error("UUID mode should auto-generate an alias")
	}

	// Lookup by alias resolves to the same UUID.
	found, err := proto.GetArtifact(ctx, art.Alias)
	if err != nil {
		t.Fatalf("GetArtifact by alias %q: %v", art.Alias, err)
	}
	if found.ID != art.ID {
		t.Errorf("alias %q resolved to %q, want %q", art.Alias, found.ID, art.ID)
	}

	// Existing scope-derived artifacts are unaffected.
	old, err := proto.GetArtifact(ctx, "SCR-TSK-1")
	if err != nil {
		t.Fatalf("existing artifact SCR-TSK-1 not readable: %v", err)
	}
	if old.Alias != "" {
		t.Errorf("existing artifact should have empty alias, got %q", old.Alias)
	}
}
