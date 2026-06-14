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

	art, err := s.Get(ctx, "LDEF-kind:effort.task")
	if err != nil {
		t.Fatalf("LDEF-kind:effort.task not found: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixKind) != "label_definition" {
		t.Errorf("LDEF-kind:effort.task Kind=%q, want %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixKind), "label_definition")
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
		Labels:   []string{"kind:effort.task"},
	})
	if err != nil {
		t.Fatalf("CreateArtifact with task kind should work: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixKind) != "effort.task" {
		t.Errorf("expected kind=effort.task, got %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixKind))
	}
}
