package parchment

import (
	"context"
	"errors"
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// GenerateUUID / IsUUIDShaped unit tests
// ---------------------------------------------------------------------------

func TestGenerateUUID_Format(t *testing.T) {
	t.Parallel()
	for range 50 {
		id := GenerateUUID()
		if !IsUUIDShaped(id) {
			t.Errorf("GenerateUUID() = %q is not UUID-shaped", id)
		}
	}
}

func TestGenerateUUID_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := GenerateUUID()
		if seen[id] {
			t.Fatalf("GenerateUUID() collision: %q", id)
		}
		seen[id] = true
	}
}

func TestIsUUIDShaped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  bool
	}{
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{GenerateUUID(), true},
		{"SCR-TSK-309", false},
		{"TSK-2026-001", false},
		{"", false},
		{"not-a-uuid-at-all", false},
		{"550e8400-e29b-41d4-a716-44665544000", false},  // one char short
		{"550e840-0e29b-41d4-a716-446655440000", false},  // dash in wrong place
		{"550e8400-e29b-41d4-a716-44665544000g", false},  // non-hex char
	}
	for _, tc := range cases {
		got := IsUUIDShaped(tc.input)
		if got != tc.want {
			t.Errorf("IsUUIDShaped(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Protocol UUID format
// ---------------------------------------------------------------------------

func TestCreateArtifact_UUIDFormat(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	proto := New(store, nil, []string{"test"}, nil, ProtocolConfig{IDFormat: IDFormatUUID})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Kind:     "task",
		Title:    "UUID-keyed artifact",
		Scope:    "test",
		Sections: []Section{{Name: "context", Text: "context text"}},
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if !IsUUIDShaped(art.ID) {
		t.Errorf("ID %q is not UUID-shaped", art.ID)
	}
}

func TestCreateArtifact_UUIDFormat_ExplicitIDOverrides(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	proto := New(store, nil, []string{"test"}, nil, ProtocolConfig{IDFormat: IDFormatUUID})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Kind:       "task",
		Title:      "explicit ID wins",
		Scope:      "test",
		ExplicitID: "MY-CUSTOM-ID",
		Sections:   []Section{{Name: "context", Text: "context text"}},
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if art.ID != "MY-CUSTOM-ID" {
		t.Errorf("ID = %q, want MY-CUSTOM-ID", art.ID)
	}
}

// ---------------------------------------------------------------------------
// MigrateToUUID integration tests (real SQLite)
// ---------------------------------------------------------------------------

// seedMigrationSource creates a source SQLite DB with three cross-linked
// scope-derived artifacts and one explicit edge, then closes it.
func seedMigrationSource(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	src, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	arts := []*Artifact{
		{UID: "uid-1", ID: "SCR-TSK-1", Kind: "task", Scope: "scribe", Status: "draft", Title: "Task One"},
		{
			UID: "uid-2", ID: "SCR-TSK-2", Kind: "task", Scope: "scribe", Status: "active",
			Title: "Task Two", DependsOn: []string{"SCR-TSK-1"},
		},
		{
			UID: "uid-3", ID: "SCR-SPC-1", Kind: "spec", Scope: "scribe", Status: "draft",
			Title:  "Spec One",
			Parent: "SCR-TSK-1",
			Links:  map[string][]string{RelImplements: {"SCR-TSK-2"}},
		},
	}
	for _, a := range arts {
		if err := src.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := src.AddEdge(ctx, Edge{From: "SCR-TSK-1", To: "SCR-SPC-1", Relation: RelParentOf}); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
}

// verifyAllUUIDShaped asserts every artifact in dst has a UUID-shaped ID.
func verifyAllUUIDShaped(t *testing.T, dst Store, wantCount int) {
	t.Helper()
	arts, err := dst.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != wantCount {
		t.Fatalf("want %d artifacts in dst, got %d", wantCount, len(arts))
	}
	for _, a := range arts {
		if !IsUUIDShaped(a.ID) {
			t.Errorf("dst artifact ID %q is not UUID-shaped", a.ID)
		}
	}
}

func TestMigrateToUUID_RemapsAllIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath, dstPath := dir+"/src.sqlite", dir+"/dst.sqlite"

	seedMigrationSource(t, srcPath)

	result, err := MigrateToUUID(srcPath, dstPath)
	if err != nil {
		t.Fatalf("MigrateToUUID: %v", err)
	}
	if result.Remapped != 3 {
		t.Errorf("remapped = %d, want 3", result.Remapped)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", result.Skipped)
	}

	dst, err := OpenSQLite(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close() //nolint:errcheck // deferred close in test

	verifyAllUUIDShaped(t, dst, 3)
	verifyRemappedRefs(t, dst, result)
}

// verifyRemappedRefs checks that depends_on, parent, links, and edges all
// point to the new UUID IDs after migration.
func verifyRemappedRefs(t *testing.T, dst Store, result *UUIDMigrateResult) {
	t.Helper()
	ctx := context.Background()
	newID1 := result.IDMap["SCR-TSK-1"]
	newID2 := result.IDMap["SCR-TSK-2"]
	newID3 := result.IDMap["SCR-SPC-1"]

	task2, err := dst.Get(ctx, newID2)
	if err != nil {
		t.Fatalf("get %s: %v", newID2, err)
	}
	if len(task2.DependsOn) != 1 || task2.DependsOn[0] != newID1 {
		t.Errorf("depends_on = %v, want [%s]", task2.DependsOn, newID1)
	}

	spec, err := dst.Get(ctx, newID3)
	if err != nil {
		t.Fatalf("get %s: %v", newID3, err)
	}
	if spec.Parent != newID1 {
		t.Errorf("parent = %q, want %q", spec.Parent, newID1)
	}
	if len(spec.Links[RelImplements]) != 1 || spec.Links[RelImplements][0] != newID2 {
		t.Errorf("links[implements] = %v, want [%s]", spec.Links[RelImplements], newID2)
	}

	edges, err := dst.Neighbors(ctx, newID1, RelParentOf, Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].To != newID3 {
		t.Errorf("parent_of edge: got %+v, want %s→%s", edges, newID1, newID3)
	}
}

func TestMigrateToUUID_SkipsAlreadyUUIDIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	srcPath, dstPath := dir+"/src.sqlite", dir+"/dst.sqlite"

	src, err := OpenSQLite(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	uuid1, uuid2 := GenerateUUID(), GenerateUUID()
	_ = src.Put(ctx, &Artifact{UID: "u1", ID: uuid1, Kind: "task", Status: "draft", Title: "already uuid"})
	_ = src.Put(ctx, &Artifact{UID: "u2", ID: "SCR-TSK-1", Kind: "task", Status: "draft", Title: "scope-derived"})
	_ = src.Put(ctx, &Artifact{UID: "u3", ID: uuid2, Kind: "spec", Status: "draft", Title: "also uuid"})
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := MigrateToUUID(srcPath, dstPath)
	if err != nil {
		t.Fatalf("MigrateToUUID: %v", err)
	}
	if result.Remapped != 1 {
		t.Errorf("remapped = %d, want 1", result.Remapped)
	}
	if result.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", result.Skipped)
	}
	if result.IDMap[uuid1] != uuid1 {
		t.Errorf("uuid1 IDMap = %q, want identity", result.IDMap[uuid1])
	}
	if result.IDMap[uuid2] != uuid2 {
		t.Errorf("uuid2 IDMap = %q, want identity", result.IDMap[uuid2])
	}
}

func TestMigrateToUUID_SourceUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	srcPath, dstPath := dir+"/src.sqlite", dir+"/dst.sqlite"

	src, err := OpenSQLite(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Put(ctx, &Artifact{UID: "u1", ID: "SCR-TSK-1", Kind: "task", Status: "draft", Title: "Task"})
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateToUUID(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}

	srcAgain, err := OpenSQLite(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer srcAgain.Close() //nolint:errcheck // deferred close in test

	art, err := srcAgain.Get(ctx, "SCR-TSK-1")
	if err != nil {
		t.Fatalf("source should still have SCR-TSK-1: %v", err)
	}
	if art.ID != "SCR-TSK-1" {
		t.Errorf("source ID = %q, want SCR-TSK-1", art.ID)
	}
}

func TestMigrateToUUID_DestMustNotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath, dstPath := dir+"/src.sqlite", dir+"/dst.sqlite"

	src, err := OpenSQLite(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(dstPath) //nolint:gosec // controlled test path
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = MigrateToUUID(srcPath, dstPath)
	if !errors.Is(err, ErrDestExists) {
		t.Errorf("want ErrDestExists, got %v", err)
	}
}

func TestMigrateToUUID_EmptyDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath, dstPath := dir+"/src.sqlite", dir+"/dst.sqlite"

	src, err := OpenSQLite(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := MigrateToUUID(srcPath, dstPath)
	if err != nil {
		t.Fatalf("MigrateToUUID on empty DB: %v", err)
	}
	if result.Remapped != 0 || result.Skipped != 0 {
		t.Errorf("empty DB: remapped=%d skipped=%d, want both 0", result.Remapped, result.Skipped)
	}
}
