package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestKindTrait_StoredAsLabelDefinition(t *testing.T) {
	// Kind traits are stored as label_definition artifacts keyed LDEF-kind:name.
	s := parchment.NewMemoryStore()
	ctx := context.Background()
	parchment.SeedLabelTraits(ctx, s)

	art, err := s.Get(ctx, "LDEF-kind:task")
	if err != nil {
		t.Fatalf("LDEF-kind:task not found: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixKind) != parchment.KindLabelDefinition {
		t.Errorf("LDEF-kind:task Kind=%q, want %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixKind), parchment.KindLabelDefinition)
	}
}

func TestProtocol_KindTask_UsableAfterSeedLabelTraits(t *testing.T) {
	// Protocol must be able to create a task artifact after SeedLabelTraits.
	s := parchment.NewMemoryStore()
	ctx := context.Background()
	parchment.SeedLabelTraits(ctx, s)

	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	art, err := p.CreateArtifact(ctx, parchment.CreateInput{
		Title:    "kind trait test",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels:   []string{"kind:task"},
	})
	if err != nil {
		t.Fatalf("CreateArtifact with task kind should work: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixKind) != "task" {
		t.Errorf("expected kind=task, got %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixKind))
	}
}
