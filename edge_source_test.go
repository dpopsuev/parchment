package parchment_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
)

// TestEdgeSource_MemoryStore verifies AddEdgeSource and RemoveEdgeSource on MemoryStore.
func TestEdgeSource_MemoryStore(t *testing.T) {
	runEdgeSourceTests(t, parchment.NewMemoryStore())
}

// TestEdgeSource_SQLiteStore verifies AddEdgeSource and RemoveEdgeSource on SQLiteStore.
func TestEdgeSource_SQLiteStore(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "edge_src.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown
	runEdgeSourceTests(t, s)
}

func runEdgeSourceTests(t *testing.T, s parchment.Store) {
	t.Helper()
	ctx := context.Background()

	const from, rel, to = "A", "depends_on", "B"

	// AddEdgeSource creates the edge with the first source.
	if err := s.AddEdgeSource(ctx, from, rel, to, "wikilink"); err != nil {
		t.Fatalf("AddEdgeSource: %v", err)
	}
	edges, _ := s.Neighbors(ctx, from, rel, parchment.Outgoing)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if !containsSource(edges[0].Sources, "wikilink") {
		t.Errorf("expected wikilink source, got %v", edges[0].Sources)
	}

	// AddEdgeSource with a second source merges rather than replacing.
	if err := s.AddEdgeSource(ctx, from, rel, to, "locus"); err != nil {
		t.Fatalf("AddEdgeSource second: %v", err)
	}
	edges, _ = s.Neighbors(ctx, from, rel, parchment.Outgoing)
	if !containsSource(edges[0].Sources, "wikilink") || !containsSource(edges[0].Sources, "locus") {
		t.Errorf("expected both sources, got %v", edges[0].Sources)
	}

	// AddEdgeSource is idempotent.
	if err := s.AddEdgeSource(ctx, from, rel, to, "locus"); err != nil {
		t.Fatalf("AddEdgeSource idempotent: %v", err)
	}
	edges, _ = s.Neighbors(ctx, from, rel, parchment.Outgoing)
	if len(edges[0].Sources) != 2 {
		t.Errorf("idempotent add should not duplicate, got %v", edges[0].Sources)
	}

	// RemoveEdgeSource removes one source but keeps the edge alive.
	if err := s.RemoveEdgeSource(ctx, from, rel, to, "locus"); err != nil {
		t.Fatalf("RemoveEdgeSource: %v", err)
	}
	edges, _ = s.Neighbors(ctx, from, rel, parchment.Outgoing)
	if len(edges) != 1 {
		t.Fatalf("edge should still exist after partial remove, got %d", len(edges))
	}
	if containsSource(edges[0].Sources, "locus") {
		t.Errorf("locus should have been removed, got %v", edges[0].Sources)
	}

	// RemoveEdgeSource deletes the edge when the source set becomes empty.
	if err := s.RemoveEdgeSource(ctx, from, rel, to, "wikilink"); err != nil {
		t.Fatalf("RemoveEdgeSource last: %v", err)
	}
	edges, _ = s.Neighbors(ctx, from, rel, parchment.Outgoing)
	if len(edges) != 0 {
		t.Errorf("edge should be deleted when sources empty, got %d", len(edges))
	}

	// RemoveEdgeSource on non-existent edge is a no-op.
	if err := s.RemoveEdgeSource(ctx, from, rel, to, "manual"); err != nil {
		t.Errorf("RemoveEdgeSource non-existent should be no-op, got: %v", err)
	}
}

func containsSource(sources []string, s string) bool {
	for _, src := range sources {
		if src == s {
			return true
		}
	}
	return false
}
