package parchment_test

import (
	"context"
	"strings"
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
		_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "artifact", Scope: "test",
			Goal: string(rune('a' + i)), // distinct enough to create,
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	var all []string
	pages := 0
	cursor := ""
	for {
		page, err := proto.ListPage(ctx, parchment.ListInput{Scope: "test", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListPage: %v", err)
		}
		if len(page.Artifacts) > 2 {
			t.Errorf("page %d has %d artifacts, want ≤2 (Limit=2)", pages+1, len(page.Artifacts))
		}
		for _, a := range page.Artifacts {
			all = append(all, a.ID)
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 5 {
		t.Errorf("expected 5 total artifacts across pages, got %d: %v", len(all), all)
	}
	if pages < 3 {
		t.Errorf("expected ≥3 pages for 5 artifacts with Limit=2, got %d", pages)
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
			Title:  "item",
			Scope:  "test",
			Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},
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

func TestProtocol_ListPage_TitleContains_Filters(t *testing.T) {
	// Given: artifacts with different titles
	// When: ListPage is called with TitleContains
	// Then: only matching artifacts are returned — not silently dropped
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	proto.CreateArtifact(ctx, parchment.CreateInput{Title: "fix authentication bug", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})     //nolint:errcheck // test setup
	proto.CreateArtifact(ctx, parchment.CreateInput{Title: "implement caching layer", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},}) //nolint:errcheck // test setup
	proto.CreateArtifact(ctx, parchment.CreateInput{Title: "auth token refresh", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})      //nolint:errcheck // test setup

	page, err := proto.ListPage(ctx, parchment.ListInput{Scope: "test", TitleContains: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Artifacts) != 2 {
		t.Errorf("expected 2 artifacts matching 'auth', got %d", len(page.Artifacts))
	}
	for _, a := range page.Artifacts {
		if !strings.Contains(strings.ToLower(a.Title), "auth") {
			t.Errorf("artifact %q does not contain 'auth'", a.Title)
		}
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
			Title:  "item",
			Scope:  "test",
			Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},
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
