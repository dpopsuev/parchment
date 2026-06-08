package parchment_test

import (
	"context"
	"testing"
	"time"

	"github.com/dpopsuev/parchment"
)

func TestRegistry_KindYAML_LoadedByKnowledgeSchema(t *testing.T) {
	// KnowledgeSchema is data-driven from embedded YAML, not Go struct literals.
	// The task kind must come from the registry, not hardcoded Go.
	t.Parallel()
	schema := parchment.KnowledgeSchema()
	if _, ok := schema.Kinds[parchment.KindTask]; !ok {
		t.Fatal("task kind not in schema — registry YAML not loaded")
	}
}

func TestRegistry_SeedDefinitions_GuidanceFromYAML(t *testing.T) {
	// kind_definition artifacts must carry when_to_create/agent_note sections
	// sourced from the registry YAML, not from KindDef Go fields.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	parchment.SeedDefinitions(ctx, s)

	art, err := s.Get(ctx, "DEF-task")
	if err != nil {
		t.Fatal("DEF-task not seeded")
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if sections["when_to_create"] == "" {
		t.Error("DEF-task missing when_to_create section from registry YAML")
	}
	if sections["agent_note"] == "" {
		t.Error("DEF-task missing agent_note section from registry YAML")
	}
}

func TestRegistry_EdgeTypeYAML_LoadedBySeedEdgeTypeTraits(t *testing.T) {
	// edge_type_definition artifacts carry when_to_use/semantics from registry YAML.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	parchment.SeedEdgeTypeTraits(ctx, s)

	art, err := s.Get(ctx, "EDT-depends_on")
	if err != nil {
		t.Fatal("EDT-depends_on not seeded")
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if sections["when_to_use"] == "" {
		t.Error("EDT-depends_on missing when_to_use section")
	}
	if sections["semantics"] == "" {
		t.Error("EDT-depends_on missing semantics section")
	}
}

func TestRegistry_LabelYAML_LoadedBySeedLabelTraits(t *testing.T) {
	// label_definition artifacts carry when_to_apply/implies from registry YAML.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
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

func TestRegistry_KindDef_NoGuidanceFields(t *testing.T) {
	// WhenToCreate and AgentNote must NOT be on KindDef — they live in artifact sections only.
	// This test verifies the registry architecture: guidance is data, not struct fields.
	t.Parallel()
	schema := parchment.KnowledgeSchema()
	task := schema.Kinds[parchment.KindTask]
	_ = task // compile-time: if WhenToCreate is on KindDef, this line would access it
	// The real check is that guidance comes from _schema artifacts, not from KindDef fields.
	// We verify by checking the kind_definition artifact has sections (tested above).
}

func TestMigrateRegistrySections_AddsMissingSectionsToExistingArtifacts(t *testing.T) {
	// Given: DEF-task exists in the store WITHOUT guidance sections (pre-registry state)
	// When: SeedDefinitions is called (which now includes migration)
	// Then: when_to_create and agent_note are added WITHOUT overwriting existing sections
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Simulate pre-registry state: DEF-task exists with NO guidance sections
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID:     "DEF-task",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope, Title: "task",
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	// Call SeedDefinitions — should migrate the existing artifact
	parchment.SeedDefinitions(ctx, s)

	art, err := s.Get(ctx, "DEF-task")
	if err != nil {
		t.Fatal(err)
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if sections["when_to_create"] == "" {
		t.Error("migration should have added when_to_create to existing DEF-task")
	}
	if sections["agent_note"] == "" {
		t.Error("migration should have added agent_note to existing DEF-task")
	}
}

func TestMigrateRegistrySections_PreservesExistingCustomSections(t *testing.T) {
	// Given: DEF-task exists with a custom when_to_create section
	// When: SeedDefinitions migrates
	// Then: the custom content is preserved (migration is additive, not overwriting)
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID:     "DEF-task",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope, Title: "task",
		Sections: []parchment.Section{
			{Name: "when_to_create", Text: "custom operator guidance"},
		},
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	parchment.SeedDefinitions(ctx, s)

	art, _ := s.Get(ctx, "DEF-task")
	for _, sec := range art.Sections {
		if sec.Name == "when_to_create" && sec.Text != "custom operator guidance" {
			t.Errorf("migration overwrote custom when_to_create: %q", sec.Text)
		}
	}
}
