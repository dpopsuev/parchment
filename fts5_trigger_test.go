package parchment

// Tests proving FTS5 stays consistent with the artifacts table via triggers.
// The old code updated FTS5 after tx.Commit() — a kill in that window caused
// divergence. Triggers fire inside the artifact transaction, making them atomic.

import (
	"context"
	"testing"
)

// TestFTS5_InsertIsSearchable verifies that a newly written artifact is
// immediately findable via FTS5 search (trigger fired inside the transaction).
func TestFTS5_InsertIsSearchable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := OpenSQLite(t.TempDir() + "/fts_insert.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	err = s.Put(ctx, &Artifact{
		UID: "u1", ID: "TST-1", Kind: "task", Status: "draft",
		Title: "uniquefts5token",
	})
	if err != nil {
		t.Fatal(err)
	}

	ids, err := s.Search(ctx, "uniquefts5token")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "TST-1" {
		t.Errorf("Search after insert: got %v, want [TST-1]", ids)
	}
}

// TestFTS5_UpdateKeepsInSync verifies that updating a title is reflected in
// FTS5: old token no longer matches, new token does.
// Note: FTS5 treats '-' as the NOT operator, so search terms use
// plain words without hyphens to avoid ambiguous query parsing.
func TestFTS5_UpdateKeepsInSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := OpenSQLite(t.TempDir() + "/fts_update.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	_ = s.Put(ctx, &Artifact{
		UID: "u1", ID: "TST-1", Kind: "task", Status: "draft",
		Title: "xbefore uniqueoldword",
	})

	// Update title.
	art, _ := s.Get(ctx, "TST-1")
	art.Title = "xafter uniquenewword"
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	oldHits, _ := s.Search(ctx, "xbefore")
	newHits, _ := s.Search(ctx, "xafter")

	if len(oldHits) != 0 {
		t.Errorf("old token still searchable after update: %v", oldHits)
	}
	if len(newHits) != 1 || newHits[0] != "TST-1" {
		t.Errorf("new token not searchable after update: %v", newHits)
	}
}

// TestFTS5_DeleteRemovesFromIndex verifies that deleting an artifact also
// removes it from the FTS5 index.
func TestFTS5_DeleteRemovesFromIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := OpenSQLite(t.TempDir() + "/fts_delete.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	_ = s.Put(ctx, &Artifact{
		UID: "u1", ID: "TST-1", Kind: "task", Status: "draft",
		Title: "delete-fts-token",
	})

	if err := s.Delete(ctx, "TST-1"); err != nil {
		t.Fatal(err)
	}

	hits, _ := s.Search(ctx, "delete-fts-token")
	if len(hits) != 0 {
		t.Errorf("deleted artifact still searchable: %v", hits)
	}
}


