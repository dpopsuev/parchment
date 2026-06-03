package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestProtocol_ListPage_PaginatesCorrectly(t *testing.T) {
	// Given: 5 artifacts, page size 2
	// When: ListPage is called repeatedly following NextCursor
	// Then: all 5 artifacts are returned in stable order, no duplicates
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	for i := range 5 {
		_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
			Kind: parchment.KindTask, Title: "artifact", Scope: "test",
			Goal: string(rune('a' + i)), // distinct enough to create
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	var all []string
	cursor := ""
	for {
		page, err := proto.ListPage(ctx, parchment.ListInput{Scope: "test", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListPage: %v", err)
		}
		for _, a := range page.Artifacts {
			all = append(all, a.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 5 {
		t.Errorf("expected 5 total artifacts across pages, got %d: %v", len(all), all)
	}
	// No duplicates
	seen := make(map[string]bool)
	for _, id := range all {
		if seen[id] {
			t.Errorf("duplicate artifact in pagination: %s", id)
		}
		seen[id] = true
	}
}

func TestProtocol_ListPage_ZeroLimitReturnsAll(t *testing.T) {
	// Given: 3 artifacts, Limit=0 (default)
	// When: ListPage is called with no cursor
	// Then: all 3 are returned in one page, NextCursor is empty
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	for range 3 {
		proto.CreateArtifact(ctx, parchment.CreateInput{ //nolint:errcheck // test setup
			Kind: parchment.KindTask, Title: "item", Scope: "test",
		})
	}

	page, err := proto.ListPage(ctx, parchment.ListInput{Scope: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Artifacts) != 3 {
		t.Errorf("expected 3 artifacts, got %d", len(page.Artifacts))
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor should be empty when Limit=0, got %q", page.NextCursor)
	}
}

func TestProtocol_ListPage_SQLite_Paginates(t *testing.T) {
	// Given: 4 artifacts in SQLite, page size 2
	// When: ListPage traverses all pages
	// Then: all 4 returned, no duplicates
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	for range 4 {
		proto.CreateArtifact(ctx, parchment.CreateInput{ //nolint:errcheck // test setup
			Kind: parchment.KindTask, Title: "item", Scope: "test",
		})
	}

	var all []string
	cursor := ""
	for {
		page, err := proto.ListPage(ctx, parchment.ListInput{Scope: "test", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListPage: %v", err)
		}
		for _, a := range page.Artifacts {
			all = append(all, a.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 4 {
		t.Errorf("expected 4 artifacts, got %d", len(all))
	}
	seen := make(map[string]bool)
	for _, id := range all {
		if seen[id] {
			t.Errorf("duplicate artifact: %s", id)
		}
		seen[id] = true
	}
}
