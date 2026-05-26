package parchment

import (
	"context"
	"strings"
	"testing"
)

// TestTome_Create bundles a scope's artifacts into a sealed tome.
func TestTome_Create(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	// Seed a few artifacts.
	for _, title := range []string{"A", "B", "C"} {
		a, err := p.CreateArtifact(ctx, CreateInput{
			Kind: KindTask, Title: title, Scope: "test", Priority: "none",
			Sections: []Section{{Name: "context", Text: "ctx"}},
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		// Archive first so they can be bundled.
		_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusComplete, SetFieldOptions{Force: true})
		_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusArchived, SetFieldOptions{Force: true})
	}

	// Create a tome for scope "test".
	tome, err := p.TomeCreate(ctx, TomeInput{Scope: "test", Title: "Sprint 1"})
	if err != nil {
		t.Fatalf("TomeCreate: %v", err)
	}
	if tome.ID == "" {
		t.Error("TomeCreate returned empty ID")
	}
	if tome.Count == 0 {
		t.Error("TomeCreate bundled 0 artifacts")
	}
}

// TestTome_List returns summaries of all tomes.
func TestTome_List(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	_, err := p.TomeCreate(ctx, TomeInput{Scope: "test", Title: "Sprint 1"})
	if err != nil {
		t.Fatalf("TomeCreate: %v", err)
	}

	tomes, err := p.TomeList(ctx)
	if err != nil {
		t.Fatalf("TomeList: %v", err)
	}
	if len(tomes) == 0 {
		t.Error("TomeList returned empty list after TomeCreate")
	}
	if tomes[0].Title == "" {
		t.Error("TomeList entry has empty title")
	}
}

// TestTome_Open retrieves the artifacts bundled in a tome.
func TestTome_Open(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	// Seed and archive.
	a, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: KindTask, Title: "Done task", Scope: "test", Priority: "none",
		Sections: []Section{{Name: "context", Text: "ctx"}},
	})
	_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusComplete, SetFieldOptions{Force: true})
	_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusArchived, SetFieldOptions{Force: true})

	tome, _ := p.TomeCreate(ctx, TomeInput{Scope: "test", Title: "Sprint 1"})

	arts, err := p.TomeOpen(ctx, tome.ID)
	if err != nil {
		t.Fatalf("TomeOpen: %v", err)
	}
	if len(arts) == 0 {
		t.Error("TomeOpen returned no artifacts")
	}
	found := false
	for _, art := range arts {
		if art.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("TomeOpen did not include artifact %s", a.ID)
	}
}

// TestTome_Search finds artifacts within a tome by keyword.
func TestTome_Search(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	a, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: KindTask, Title: "Searchable task", Scope: "test", Priority: "none",
		Sections: []Section{{Name: "context", Text: "unique-token-xyz"}},
	})
	_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusComplete, SetFieldOptions{Force: true})
	_, _ = p.SetField(ctx, []string{a.ID}, FieldStatus, StatusArchived, SetFieldOptions{Force: true})

	tome, _ := p.TomeCreate(ctx, TomeInput{Scope: "test", Title: "Sprint 1"})

	results, err := p.TomeSearch(ctx, tome.ID, "unique-token-xyz")
	if err != nil {
		t.Fatalf("TomeSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("TomeSearch found nothing for unique token")
	}
	found := false
	for _, art := range results {
		if strings.Contains(art.Title, "Searchable") {
			found = true
		}
	}
	if !found {
		t.Error("TomeSearch did not find the searchable task")
	}
}
