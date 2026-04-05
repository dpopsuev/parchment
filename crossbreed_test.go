package parchment

import (
	"context"
	"testing"
)

// --- ComponentMap + Annotations tests ---

func TestArtifact_ComponentMap_RoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/cm.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{
		UID: "u1", ID: "CM-1", Kind: "task", Status: "draft", Title: "with components",
		Components: ComponentMap{
			Directories: []string{"internal/parchment"},
			Files:       []string{"artifact.go", "protocol.go"},
			Symbols:     []string{"Artifact", "Protocol.CreateArtifact"},
		},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "CM-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(got.Components.Files))
	}
	if got.Components.Files[0] != "artifact.go" {
		t.Errorf("expected artifact.go, got %s", got.Components.Files[0])
	}
	if len(got.Components.Symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(got.Components.Symbols))
	}
}

func TestArtifact_Annotations_RoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/ann.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{
		UID: "u1", ID: "ANN-1", Kind: "task", Status: "draft", Title: "with annotations",
		Annotations: []Annotation{
			{Kind: "+", Comment: "good approach"},
			{Kind: "-", Comment: "missing error handling"},
		},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "ANN-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Annotations) != 2 {
		t.Errorf("expected 2 annotations, got %d", len(got.Annotations))
	}
	if got.Annotations[0].Kind != "+" {
		t.Errorf("expected +, got %s", got.Annotations[0].Kind)
	}
}

func TestArtifact_ComponentMap_Empty(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/empty.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{UID: "u1", ID: "E-1", Kind: "task", Status: "draft", Title: "no components"}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "E-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components.Files) != 0 {
		t.Errorf("expected empty files, got %d", len(got.Components.Files))
	}
	if len(got.Annotations) != 0 {
		t.Errorf("expected empty annotations, got %d", len(got.Annotations))
	}
}

// --- Cascade tests ---

func TestCascade_DependencyEdges(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/cascade.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"test": "TST"},
	})
	ctx := context.Background()

	// A → B → C (depends_on chain)
	a, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}}})
	b, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "B", Scope: "test", Priority: "medium", DependsOn: []string{a.ID}, Sections: []Section{{Name: "context", Text: "b"}}})
	c, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "C", Scope: "test", Priority: "medium", DependsOn: []string{b.ID}, Sections: []Section{{Name: "context", Text: "c"}}})

	affected := p.Cascade(ctx, a.ID)
	if len(affected) == 0 {
		t.Fatal("expected cascade to affect B and C")
	}

	// Both B and C should be affected
	affectedSet := make(map[string]bool)
	for _, id := range affected {
		affectedSet[id] = true
	}
	if !affectedSet[b.ID] {
		t.Errorf("B should be affected by cascade from A")
	}
	if !affectedSet[c.ID] {
		t.Errorf("C should be affected by cascade from A")
	}
}

func TestCascade_SpatialOverlap(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/spatial.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"test": "TST"},
	})
	ctx := context.Background()

	// Two tasks touching the same file, no dependency edge
	a, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "A", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "a"}},
	})
	b, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "B", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "b"}},
	})

	// Set ComponentMap on both with overlapping files
	artA, _ := s.Get(ctx, a.ID)
	artA.Components = ComponentMap{Files: []string{"shared.go", "only_a.go"}}
	s.Put(ctx, artA) //nolint:errcheck // test seeding

	artB, _ := s.Get(ctx, b.ID)
	artB.Components = ComponentMap{Files: []string{"shared.go", "only_b.go"}}
	s.Put(ctx, artB) //nolint:errcheck // test seeding

	affected := p.Cascade(ctx, a.ID)
	affectedSet := make(map[string]bool)
	for _, id := range affected {
		affectedSet[id] = true
	}
	if !affectedSet[b.ID] {
		t.Errorf("B should be affected by spatial overlap with A on shared.go")
	}
}
