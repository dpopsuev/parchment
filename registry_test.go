package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestRegistry_KindTraits_LoadedFromYAML(t *testing.T) {
	// Kind traits must come from registry YAML via SeedLabelTraits, not Go literals.
	t.Parallel()
	s, err := parchment.OpenSQLite(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	parchment.SeedLabelTraits(ctx, s)

	// LDEF-kind:effort.task must exist and carry guidance sections from YAML.
	art, err := s.Get(ctx, "LDEF-kind:effort.task")
	if err != nil {
		t.Fatal("LDEF-kind:effort.task not seeded")
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if sections["when_to_create"] == "" {
		t.Error("LDEF-kind:effort.task missing when_to_create section from registry YAML")
	}
	if sections["agent_note"] == "" {
		t.Error("LDEF-kind:effort.task missing agent_note section from registry YAML")
	}
}

func TestRegistry_LabelYAML_LoadedBySeedLabelTraits(t *testing.T) {
	// label_definition artifacts carry when_to_apply/implies from registry YAML.
	t.Parallel()
	s, err := parchment.OpenSQLite(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	parchment.SeedLabelTraits(ctx, s)

	art, err := s.Get(ctx, "LDEF-rule")
	if err != nil {
		t.Fatal("LDEF-rule not seeded")
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if sections["when_to_apply"] == "" {
		t.Error("LDEF-rule missing when_to_apply section")
	}
}

func TestRegistry_Protocol_KindTaskKnown(t *testing.T) {
	// Protocol must know about kind:effort.task after seeding.
	t.Parallel()
	p := parchment.New(parchment.NewMemoryStore(), nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	if !p.IsKnownKind("effort.task") {
		t.Fatal("task kind not registered — registry YAML not loaded")
	}
}
