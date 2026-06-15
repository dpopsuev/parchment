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

		art := &Artifact{ID: "TST-TSK-1", Labels: []string{"kind:effort.task", "status:draft"}, Title: "test"}
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

		s.Put(ctx, &Artifact{ID: "T-1", Labels: []string{"kind:effort.task", "status:draft", "scope:a"}, Title: "one"})    //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "T-2", Labels: []string{"kind:intent.spec", "status:draft", "scope:a"}, Title: "two"})    //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "T-3", Labels: []string{"kind:effort.task", "status:active", "scope:b"}, Title: "three"}) //nolint:errcheck // test seeding

		arts, err := s.List(ctx, Filter{Labels: []string{"kind:effort.task"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(arts) != 2 {
			t.Errorf("expected 2 tasks, got %d", len(arts))
		}

		arts, err = s.List(ctx, Filter{Labels: []string{"scope:a"}})
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

		s.Put(ctx, &Artifact{ID: "A", Labels: []string{"kind:effort.goal", "status:draft"}, Title: "a"}) //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "B", Labels: []string{"kind:effort.task", "status:draft"}, Title: "b"}) //nolint:errcheck // test seeding

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

	t.Run("GenerateUUID_Unique", func(t *testing.T) {
		t.Parallel()
		id1 := GenerateUUID()
		id2 := GenerateUUID()
		if !isUUIDShaped(id1) {
			t.Errorf("id1 %q not UUID-shaped", id1)
		}
		if id1 == id2 {
			t.Errorf("GenerateUUID produced duplicate: %s", id1)
		}
	})

	t.Run("DeleteArtifact", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()

		s.Put(ctx, &Artifact{ID: "DEL-1", Labels: []string{"kind:effort.task", "status:draft"}, Title: "delete me"}) //nolint:errcheck // test seeding

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

		s.Put(ctx, &Artifact{ID: "S-1", Labels: []string{"kind:effort.task", "status:draft"}, Title: "uniquesearchterm"}) //nolint:errcheck // test seeding

		ids, err := s.Search(ctx, "uniquesearchterm")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			t.Error("expected search results")
		}
	})

	t.Run("AddEdgeSource_RemoveEdgeSource", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "ES-A", Labels: []string{"kind:effort.task"}, Title: "a"}) //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "ES-B", Labels: []string{"kind:effort.task"}, Title: "b"}) //nolint:errcheck // test seeding

		if err := s.AddEdgeSource(ctx, "ES-A", "mentions", "ES-B", "wikilink"); err != nil {
			t.Fatal(err)
		}
		edges, _ := s.Neighbors(ctx, "ES-A", "mentions", Outgoing)
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		if err := s.AddEdgeSource(ctx, "ES-A", "mentions", "ES-B", "manual"); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveEdgeSource(ctx, "ES-A", "mentions", "ES-B", "wikilink"); err != nil {
			t.Fatal(err)
		}
		edges, _ = s.Neighbors(ctx, "ES-A", "mentions", Outgoing)
		if len(edges) != 1 {
			t.Fatal("edge should survive when one source remains")
		}
		if err := s.RemoveEdgeSource(ctx, "ES-A", "mentions", "ES-B", "manual"); err != nil {
			t.Fatal(err)
		}
		edges, _ = s.Neighbors(ctx, "ES-A", "mentions", Outgoing)
		if len(edges) != 0 {
			t.Error("edge should be deleted when all sources removed")
		}
	})

	t.Run("GetByAlias", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "ALIAS-1", Alias: "my-alias", Labels: []string{"kind:effort.task"}, Title: "aliased"}) //nolint:errcheck // test seeding

		got, err := s.GetByAlias(ctx, "my-alias")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "ALIAS-1" {
			t.Errorf("got ID %q, want ALIAS-1", got.ID)
		}
	})

	t.Run("Children", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "P-1", Labels: []string{"kind:effort.goal"}, Title: "parent"}) //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "C-1", Labels: []string{"kind:effort.task"}, Title: "child"})  //nolint:errcheck // test seeding
		s.AddEdge(ctx, Edge{From: "P-1", To: "C-1", Relation: RelParentOf})                     //nolint:errcheck // test seeding

		children, err := s.Children(ctx, "P-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(children) != 1 || children[0].ID != "C-1" {
			t.Errorf("expected [C-1], got %v", children)
		}
	})

	t.Run("ListByLabel", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "LBL-1", Labels: []string{"kind:effort.task", "priority:high"}, Title: "high"})  //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "LBL-2", Labels: []string{"kind:effort.task", "priority:low"}, Title: "low"})    //nolint:errcheck // test seeding

		arts, err := s.ListByLabel(ctx, "priority:high")
		if err != nil {
			t.Fatal(err)
		}
		if len(arts) != 1 || arts[0].ID != "LBL-1" {
			t.Errorf("expected [LBL-1], got %v", arts)
		}
	})

	t.Run("Attachment_PutGetDelete", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "ATT-1", Labels: []string{"kind:effort.task"}, Title: "with attachment"}) //nolint:errcheck // test seeding

		data := []byte("hello world")
		if err := s.PutAttachment(ctx, "ATT-1", "test.txt", "text/plain", data); err != nil {
			t.Fatal(err)
		}
		atts, err := s.GetAttachments(ctx, "ATT-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 1 || string(atts[0].Data) != "hello world" {
			t.Errorf("expected attachment data 'hello world', got %v", atts)
		}
		if err := s.DeleteAttachment(ctx, "ATT-1", "test.txt"); err != nil {
			t.Fatal(err)
		}
		atts, _ = s.GetAttachments(ctx, "ATT-1")
		if len(atts) != 0 {
			t.Error("attachment should be deleted")
		}
	})

	t.Run("Embedding_PutSearchSemantic", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		s.Put(ctx, &Artifact{ID: "EMB-1", Labels: []string{"kind:knowledge.note"}, Title: "close"}) //nolint:errcheck // test seeding
		s.Put(ctx, &Artifact{ID: "EMB-2", Labels: []string{"kind:knowledge.note"}, Title: "far"})   //nolint:errcheck // test seeding

		s.PutEmbedding(ctx, "EMB-1", "test", "", []float32{0.9, 0.1, 0.0}) //nolint:errcheck // test seeding
		s.PutEmbedding(ctx, "EMB-2", "test", "", []float32{0.0, 0.0, 1.0}) //nolint:errcheck // test seeding

		results, err := s.SearchSemantic(ctx, "test", []float32{1.0, 0.0, 0.0}, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) < 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].ID != "EMB-1" {
			t.Errorf("closest should be EMB-1, got %s", results[0].ID)
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
		ID: "NEW-TSK-1", Labels: []string{"kind:effort.task", "status:draft"},
		Title:       "new artifact",
		Annotations: []Annotation{{Kind: "+", Comment: "good"}},
		CreatedAt:   art.CreatedAt, UpdatedAt: art.CreatedAt,
	})
	if err != nil {
		t.Fatalf("failed to write new artifact: %v", err)
	}

	// Read it back — verify annotations round-trip.
	got, err := s.Get(ctx, "NEW-TSK-1")
	if err != nil {
		t.Fatal(err)
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
			// uid generated internally
			ID:     fmt.Sprintf("ND-TSK-%d", i),
			Labels: []string{"kind:effort.task", "status:draft"},
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

