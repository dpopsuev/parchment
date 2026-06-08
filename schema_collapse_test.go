package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestKindDef_StoredAsLabelDefinition(t *testing.T) {
	// Given: SeedDefinitions has run.
	// When:  we fetch a definition artifact by ID (e.g. DEF-task).
	// Then:  its Kind is KindLabelDefinition (not KindDefinition).
	// This confirms kind_definition has collapsed into label_definition.
	s := parchment.NewMemoryStore()
	ctx := context.Background()
	parchment.SeedDefinitions(ctx, s)

	art, err := s.Get(ctx, "DEF-task")
	if err != nil {
		t.Fatalf("DEF-task not found: %v", err)
	}
	if art.Kind != parchment.KindLabelDefinition {
		t.Errorf("DEF-task Kind=%q, want %q (KindLabelDefinition)", art.Kind, parchment.KindLabelDefinition)
	}
}

func TestLoadSchema_ReadsFromCollapsedLabelDefs(t *testing.T) {
	// Given: SeedDefinitions has run (now stores as label_definition).
	// When:  a Protocol is constructed (which calls loadSchema).
	// Then:  the task kind is present in schema.
	s := parchment.NewMemoryStore()
	ctx := context.Background()
	parchment.SeedDefinitions(ctx, s)

	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	art, err := p.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "kind collapse test",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	if err != nil {
		t.Fatalf("CreateArtifact with task kind should work after schema collapse: %v", err)
	}
	if art.ResolvedKind() != "task" {
		t.Errorf("expected kind=task, got %q", art.ResolvedKind())
	}
}
