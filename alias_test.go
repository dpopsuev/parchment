package parchment

// Tests for the alias system: UUID id + human-readable mutable alias.

import (
	"context"
	"testing"
)

func uuidProto(t *testing.T) *Protocol {
	t.Helper()
	store := NewMemoryStore()
	return New(store, nil, []string{"test"}, nil, ProtocolConfig{})
}

// TestAlias_CustomAliasOnCreate verifies that a caller-supplied alias is
// honored on create.
func TestAlias_CustomAliasOnCreate(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title: "Login bug",

		Alias:    "login-bug",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"},})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if art.Alias != "login-bug" {
		t.Errorf("alias = %q, want login-bug", art.Alias)
	}
	if !isUUIDShaped(art.ID) {
		t.Errorf("ID %q should be UUID-shaped", art.ID)
	}
}

// TestAlias_GetByAlias verifies that GetArtifact resolves human-readable
// aliases transparently, returning the same artifact as a UUID lookup.
func TestAlias_GetByAlias(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title: "Findable",
		Alias:    "find-me",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"},})
	if err != nil {
		t.Fatal(err)
	}

	// Lookup by UUID.
	byUUID, err := proto.GetArtifact(ctx, art.ID)
	if err != nil {
		t.Fatalf("GetArtifact by UUID: %v", err)
	}

	// Lookup by alias.
	byAlias, err := proto.GetArtifact(ctx, "find-me")
	if err != nil {
		t.Fatalf("GetArtifact by alias: %v", err)
	}

	if byUUID.ID != byAlias.ID {
		t.Errorf("UUID lookup ID %q != alias lookup ID %q", byUUID.ID, byAlias.ID)
	}
	if byAlias.Alias != "find-me" {
		t.Errorf("alias round-trip: got %q, want find-me", byAlias.Alias)
	}
}

// TestAlias_Rename verifies that SetField("alias", …) changes the alias
// and the artifact remains findable by the new name.
func TestAlias_Rename(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title: "Rename me",
		Alias:    "old-name",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"},})
	if err != nil {
		t.Fatal(err)
	}
	uuid := art.ID

	// Rename via SetField.
	results, err := proto.SetField(ctx, []string{uuid}, FieldAlias, "new-name")
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("SetField alias: err=%v results=%+v", err, results)
	}

	// UUID still works.
	got, err := proto.GetArtifact(ctx, uuid)
	if err != nil {
		t.Fatalf("GetArtifact by UUID after rename: %v", err)
	}
	if got.Alias != "new-name" {
		t.Errorf("alias after rename = %q, want new-name", got.Alias)
	}

	// New alias resolves.
	byNew, err := proto.GetArtifact(ctx, "new-name")
	if err != nil {
		t.Fatalf("GetArtifact by new alias: %v", err)
	}
	if byNew.ID != uuid {
		t.Errorf("new alias resolves to %q, want %q", byNew.ID, uuid)
	}
}

// TestAlias_RenameByAlias verifies that SetField accepts an alias as the
// lookup key, so callers never need to know the underlying UUID.
func TestAlias_RenameByAlias(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{Title: "Rename by alias",
		Alias:    "step-one",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"},})
	if err != nil {
		t.Fatal(err)
	}

	// Rename using the alias as the lookup key — not the UUID.
	results, err := proto.SetField(ctx, []string{"step-one"}, FieldAlias, "step-two")
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("SetField by alias: err=%v results=%+v", err, results)
	}

	// Old alias no longer resolves.
	if _, err := proto.GetArtifact(ctx, "step-one"); err == nil {
		t.Error("old alias step-one should no longer resolve")
	}

	// New alias resolves to the same UUID.
	got, err := proto.GetArtifact(ctx, "step-two")
	if err != nil {
		t.Fatalf("GetArtifact by new alias: %v", err)
	}
	if got.ID != art.ID {
		t.Errorf("new alias resolved to %q, want %q", got.ID, art.ID)
	}
}

// TestAlias_SQLite_RoundTrip verifies alias persistence and resolution
// against a real SQLite store (not just MemoryStore).
func TestAlias_SQLite_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/alias.sqlite"

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, CreateInput{Title: "Persistent alias",
		Alias:    "persistent-one",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"},})
	if err != nil {
		t.Fatal(err)
	}

	// Lookup by alias via Protocol (UUID id stored, alias column indexed).
	found, err := proto.GetArtifact(ctx, "persistent-one")
	if err != nil {
		t.Fatalf("GetArtifact by alias: %v", err)
	}
	if found.ID != art.ID {
		t.Errorf("alias resolved to %q, want %q", found.ID, art.ID)
	}

	// Rename and verify.
	_, _ = proto.SetField(ctx, []string{art.ID}, FieldAlias, "persistent-two")
	renamed, err := proto.GetArtifact(ctx, "persistent-two")
	if err != nil {
		t.Fatalf("GetArtifact by new alias: %v", err)
	}
	if renamed.ID != art.ID {
		t.Errorf("renamed alias resolved to %q, want %q", renamed.ID, art.ID)
	}
}
