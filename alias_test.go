package parchment

import (
	"context"
	"testing"
)

func uuidProto(t *testing.T) *Protocol {
	t.Helper()
	store := NewMemoryStore()
	return New(store, nil, []string{"test"}, nil, ProtocolConfig{})
}

func TestAlias_AddAndResolve(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:    "Login bug",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Store().AddAlias(ctx, art.ID, "login-bug"); err != nil {
		t.Fatal(err)
	}
	found, err := proto.GetArtifact(ctx, "login-bug")
	if err != nil {
		t.Fatalf("GetArtifact by alias: %v", err)
	}
	if found.ID != art.ID {
		t.Errorf("alias resolved to %q, want %q", found.ID, art.ID)
	}
}

func TestAlias_SetFieldAddsAlias(t *testing.T) {
	t.Parallel()
	proto := uuidProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:    "Rename me",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := proto.SetField(ctx, []string{art.ID}, FieldAlias, "new-name")
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("SetField alias: err=%v results=%+v", err, results)
	}

	byNew, err := proto.GetArtifact(ctx, "new-name")
	if err != nil {
		t.Fatalf("GetArtifact by new alias: %v", err)
	}
	if byNew.ID != art.ID {
		t.Errorf("alias resolved to %q, want %q", byNew.ID, art.ID)
	}
}

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

	art, err := proto.CreateArtifact(ctx, CreateInput{
		Title:    "Persistent alias",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAlias(ctx, art.ID, "persistent-one"); err != nil {
		t.Fatal(err)
	}

	found, err := proto.GetArtifact(ctx, "persistent-one")
	if err != nil {
		t.Fatalf("GetArtifact by alias: %v", err)
	}
	if found.ID != art.ID {
		t.Errorf("alias resolved to %q, want %q", found.ID, art.ID)
	}
}

// --- Alias Ring (multi-alias via junction table) ---

func TestAddAlias_SingleAlias(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Put(ctx, &Artifact{ID: "art1", Title: "Alpha"})
	if err := store.AddAlias(ctx, "art1", "alpha"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByAlias(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetByAlias(alpha): %v", err)
	}
	if got.ID != "art1" {
		t.Errorf("got ID=%s, want art1", got.ID)
	}
}

func TestAddAlias_MultipleAliases(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Put(ctx, &Artifact{ID: "art1", Title: "Alpha"})
	for _, alias := range []string{"a", "b", "c"} {
		if err := store.AddAlias(ctx, "art1", alias); err != nil {
			t.Fatalf("AddAlias(%s): %v", alias, err)
		}
	}
	for _, alias := range []string{"a", "b", "c"} {
		got, err := store.GetByAlias(ctx, alias)
		if err != nil {
			t.Fatalf("GetByAlias(%s): %v", alias, err)
		}
		if got.ID != "art1" {
			t.Errorf("alias %s resolved to %s, want art1", alias, got.ID)
		}
	}
}

func TestAddAlias_UniqueConstraint(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Put(ctx, &Artifact{ID: "art1", Title: "Alpha"})
	_ = store.Put(ctx, &Artifact{ID: "art2", Title: "Beta"})
	if err := store.AddAlias(ctx, "art1", "shared"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAlias(ctx, "art2", "shared"); err == nil {
		t.Error("expected error for duplicate alias")
	}
}

func TestRemoveAlias(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Put(ctx, &Artifact{ID: "art1", Title: "Alpha"})
	_ = store.AddAlias(ctx, "art1", "gone")
	if err := store.RemoveAlias(ctx, "art1", "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByAlias(ctx, "gone"); err == nil {
		t.Error("expected error after removing alias")
	}
}

func TestAddAlias_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/alias-ring.sqlite"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test

	_ = s.Put(ctx, &Artifact{ID: "art1", Title: "Alpha", Labels: []string{"kind:knowledge.note"}})

	for _, alias := range []string{"x", "y", "z"} {
		if err := s.AddAlias(ctx, "art1", alias); err != nil {
			t.Fatalf("AddAlias(%s): %v", alias, err)
		}
	}
	for _, alias := range []string{"x", "y", "z"} {
		got, err := s.GetByAlias(ctx, alias)
		if err != nil {
			t.Fatalf("GetByAlias(%s): %v", alias, err)
		}
		if got.ID != "art1" {
			t.Errorf("alias %s → %s, want art1", alias, got.ID)
		}
	}

	if err := s.RemoveAlias(ctx, "art1", "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByAlias(ctx, "y"); err == nil {
		t.Error("y should not resolve after removal")
	}
	if _, err := s.GetByAlias(ctx, "x"); err != nil {
		t.Error("x should still resolve")
	}
}
