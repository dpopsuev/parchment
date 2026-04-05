package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// Compile-time interface verification.
var _ Store = (*SQLiteStore)(nil)

// storeContract runs the full Store contract test suite against any Store implementation.
// This enables testing SQLiteStore now and MemoryStore (future) with the same tests.
func storeContract(t *testing.T, newStore func(t *testing.T) Store) { //nolint:gocyclo // contract suite is intentionally comprehensive
	t.Helper()

	t.Run("PutGet", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		art := &Artifact{UID: "u1", ID: "TST-TSK-1", Kind: "task", Status: "draft", Title: "test"}
		if err := s.Put(ctx, art); err != nil {
			t.Fatal(err)
		}

		got, err := s.Get(ctx, "TST-TSK-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "test" {
			t.Errorf("title = %q, want %q", got.Title, "test")
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		_, err := s.Get(ctx, "NONEXISTENT")
		if err == nil {
			t.Fatal("expected error for missing artifact")
		}
	})

	t.Run("ListFilter", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		s.Put(ctx, &Artifact{UID: "u1", ID: "T-1", Kind: "task", Scope: "a", Status: "draft", Title: "one"})    //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{UID: "u2", ID: "T-2", Kind: "spec", Scope: "a", Status: "draft", Title: "two"})    //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{UID: "u3", ID: "T-3", Kind: "task", Scope: "b", Status: "active", Title: "three"}) //nolint:errcheck // test seeding

		arts, err := s.List(ctx, Filter{Kind: "task"})
		if err != nil {
			t.Fatal(err)
		}
		if len(arts) != 2 {
			t.Errorf("expected 2 tasks, got %d", len(arts))
		}

		arts, err = s.List(ctx, Filter{Scope: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if len(arts) != 2 {
			t.Errorf("expected 2 in scope a, got %d", len(arts))
		}
	})

	t.Run("AddEdgeNeighbors", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		s.Put(ctx, &Artifact{UID: "u1", ID: "A", Kind: "goal", Status: "draft", Title: "a"}) //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{UID: "u2", ID: "B", Kind: "task", Status: "draft", Title: "b"}) //nolint:errcheck // test seeding

		if err := s.AddEdge(ctx, Edge{From: "A", To: "B", Relation: RelParentOf}); err != nil {
			t.Fatal(err)
		}

		edges, err := s.Neighbors(ctx, "A", RelParentOf, Outgoing)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].To != "B" {
			t.Errorf("expected edge A→B, got %+v", edges)
		}

		edges, err = s.Neighbors(ctx, "B", RelParentOf, Incoming)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].From != "A" {
			t.Errorf("expected edge A→B (incoming to B), got %+v", edges)
		}
	})

	t.Run("NextScopedID_Monotonic", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		id1, err := s.NextScopedID(ctx, "TST", "TSK")
		if err != nil {
			t.Fatal(err)
		}
		id2, err := s.NextScopedID(ctx, "TST", "TSK")
		if err != nil {
			t.Fatal(err)
		}
		if id1 >= id2 {
			t.Errorf("IDs not monotonic: %s >= %s", id1, id2)
		}
	})

	t.Run("DeleteArtifact", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		s.Put(ctx, &Artifact{UID: "u1", ID: "DEL-1", Kind: "task", Status: "draft", Title: "delete me"}) //nolint:errcheck // test seeding

		if err := s.Delete(ctx, "DEL-1"); err != nil {
			t.Fatal(err)
		}
		_, err := s.Get(ctx, "DEL-1")
		if err == nil {
			t.Fatal("expected error after delete")
		}
	})

	t.Run("SearchFTS", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		s.Put(ctx, &Artifact{UID: "u1", ID: "S-1", Kind: "task", Status: "draft", Title: "uniquesearchterm"}) //nolint:errcheck // test seeding

		ids, err := s.Search(ctx, "uniquesearchterm")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			t.Error("expected search results")
		}
	})
}

// TestSQLiteStore_Contract runs the full Store contract against SQLiteStore.
func TestSQLiteStore_Contract(t *testing.T) {
	storeContract(t, func(t *testing.T) Store {
		t.Helper()
		path := t.TempDir() + "/contract.db"
		s, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

// TestMemoryStore_Contract runs the full Store contract against MemoryStore.
func TestMemoryStore_Contract(t *testing.T) {
	storeContract(t, func(t *testing.T) Store {
		t.Helper()
		return NewMemoryStore()
	})
}

// TestSQLiteStore_MigrationCompat verifies that a database created with the
// old schema (without components/annotations columns) works after migration.
// Regression test for SELECT * column ordering bug.
func TestSQLiteStore_MigrationCompat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/migrate.db"

	// Create a DB with the OLD schema (no components/annotations columns).
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS artifacts (
		uid TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
		parent TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
		goal TEXT NOT NULL DEFAULT '', depends_on TEXT NOT NULL DEFAULT '[]',
		labels TEXT NOT NULL DEFAULT '[]', priority TEXT NOT NULL DEFAULT '',
		sprint TEXT NOT NULL DEFAULT '', sections TEXT NOT NULL DEFAULT '[]',
		features TEXT NOT NULL DEFAULT '[]', criteria TEXT NOT NULL DEFAULT '[]',
		links TEXT NOT NULL DEFAULT '{}', extra TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		inserted_at TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS edges (
		from_id TEXT NOT NULL, relation TEXT NOT NULL, to_id TEXT NOT NULL,
		PRIMARY KEY (from_id, relation, to_id))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sequences (
		prefix TEXT PRIMARY KEY, next_val INTEGER NOT NULL DEFAULT 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scope_keys (
		scope TEXT PRIMARY KEY, key TEXT UNIQUE NOT NULL, auto INTEGER NOT NULL DEFAULT 0,
		labels TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scoped_sequences (
		scope_key TEXT NOT NULL, kind_code TEXT NOT NULL, next_val INTEGER NOT NULL DEFAULT 1,
		PRIMARY KEY (scope_key, kind_code))`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert an artifact using the old schema (no components/annotations).
	now := "2026-04-05T12:00:00Z"
	_, err = db.ExecContext(ctx,
		`INSERT INTO artifacts (uid, id, kind, scope, status, parent, title, goal,
		depends_on, labels, priority, sprint, sections, features, criteria, links, extra,
		created_at, updated_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', '[]', '', '', '[]', '[]', '[]', '{}', '{}', ?, ?, ?)`,
		"old-uid", "OLD-TSK-1", "task", "test", "draft", "", "old artifact", "",
		now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Now open with the real OpenSQLite (triggers migration).
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Read the old artifact — must not produce scan errors.
	art, err := s.Get(ctx, "OLD-TSK-1")
	if err != nil {
		t.Fatalf("failed to read migrated artifact: %v", err)
	}
	if art.Title != "old artifact" {
		t.Errorf("title = %q, want %q", art.Title, "old artifact")
	}
	if art.CreatedAt.IsZero() {
		t.Error("created_at should be parsed correctly after migration")
	}

	// Write a new artifact with the new fields.
	err = s.Put(ctx, &Artifact{
		UID: "new-uid", ID: "NEW-TSK-1", Kind: "task", Status: "draft",
		Title:       "new artifact",
		Components:  ComponentMap{Files: []string{"test.go"}},
		Annotations: []Annotation{{Kind: "+", Comment: "good"}},
		CreatedAt:   art.CreatedAt, UpdatedAt: art.CreatedAt,
	})
	if err != nil {
		t.Fatalf("failed to write new artifact: %v", err)
	}

	// Read it back — verify components + annotations round-trip.
	got, err := s.Get(ctx, "NEW-TSK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components.Files) != 1 || got.Components.Files[0] != "test.go" {
		t.Errorf("components = %+v, want [test.go]", got.Components)
	}
	if len(got.Annotations) != 1 || got.Annotations[0].Kind != "+" {
		t.Errorf("annotations = %+v, want [{+ good}]", got.Annotations)
	}
}

// TestSQLiteStore_ListDoesNotSilentlyDropRows verifies that List returns
// all artifacts even when the schema has extra/unexpected columns.
// Regression test for silent row drop bug — List used to `continue` on
// scan errors without logging, causing 4,623 artifacts to vanish.
func TestSQLiteStore_ListDoesNotSilentlyDropRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/nodrop.db"

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert 50 artifacts.
	for i := range 50 {
		err := s.Put(ctx, &Artifact{
			UID:    fmt.Sprintf("u%d", i),
			ID:     fmt.Sprintf("ND-TSK-%d", i),
			Kind:   "task",
			Status: "draft",
			Title:  fmt.Sprintf("task %d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// List all — must get exactly 50 back.
	arts, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 50 {
		t.Errorf("expected 50 artifacts, got %d — rows may be silently dropped", len(arts))
	}
}

// TestMemoryStore_SaveLoad verifies atomic JSON persistence round-trip.
func TestMemoryStore_SaveLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemoryStore()

	m.Put(ctx, &Artifact{UID: "u1", ID: "SL-1", Kind: "task", Status: "draft", Title: "persist me"}) //nolint:errcheck // test seeding
	m.AddEdge(ctx, Edge{From: "SL-1", To: "SL-2", Relation: RelDependsOn})                           //nolint:errcheck // test seeding
	m.SetScopeKey(ctx, "test", "TST", false)                                                         //nolint:errcheck // test seeding

	path := t.TempDir() + "/store.json"
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded := NewMemoryStore()
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}

	got, err := loaded.Get(ctx, "SL-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "persist me" {
		t.Errorf("title = %q, want %q", got.Title, "persist me")
	}

	edges, _ := loaded.Neighbors(ctx, "SL-1", RelDependsOn, Outgoing)
	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}

	key, _, _ := loaded.GetScopeKey(ctx, "test")
	if key != "TST" {
		t.Errorf("scope key = %q, want TST", key)
	}
}
