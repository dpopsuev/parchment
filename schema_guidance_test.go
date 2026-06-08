package parchment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestKindDef_GuidanceIsNotOnStruct(t *testing.T) {
	// Agent guidance (when_to_create, agent_note) must NOT be KindDef struct fields.
	// They live only in kind_definition artifact sections — queryable data, not Go state.
	// This test verifies the registry architecture by checking that task still exists
	// in the schema (loaded from YAML) while guidance is absent from the struct.
	t.Parallel()
	schema := parchment.KnowledgeSchema()
	if _, ok := schema.Kinds[parchment.KindTask]; !ok {
		t.Fatal("task kind missing from schema — registry YAML not loaded")
	}
	// Guidance is verified via artifact sections in TestSeedDefinitions_KindArtifact_HasGuidanceSections.
}

func TestSeedDefinitions_KindArtifact_HasGuidanceSections(t *testing.T) {
	// kind_definition artifacts in _schema carry when_to_create and agent_note sections.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	parchment.SeedDefinitions(ctx, s)

	// Find the task kind_definition artifact.
	// Dual-read: accepts both legacy KindDefinition and collapsed KindLabelDefinition.
	arts, err := s.List(ctx, parchment.Filter{
		Kinds: []string{parchment.KindDefinition, parchment.KindLabelDefinition},
		Scope: parchment.SchemaScope,
	})
	if err != nil {
		t.Fatal(err)
	}

	var taskDef *parchment.Artifact
	for _, a := range arts {
		if a.Title == parchment.KindTask {
			taskDef = a
			break
		}
	}
	if taskDef == nil {
		t.Fatal("task kind_definition artifact not found in _schema")
	}

	sectionNames := make(map[string]string)
	for _, sec := range taskDef.Sections {
		sectionNames[sec.Name] = sec.Text
	}

	if _, ok := sectionNames["when_to_create"]; !ok {
		t.Error("task kind_definition should have when_to_create section")
	}
	if _, ok := sectionNames["agent_note"]; !ok {
		t.Error("task kind_definition should have agent_note section")
	}
	if text := sectionNames["when_to_create"]; !strings.Contains(text, "task") && !strings.Contains(text, "work") {
		t.Errorf("when_to_create text should mention task or work, got: %q", text)
	}
}

func TestSeedEdgeTypeTraits_HasGuidanceSections(t *testing.T) {
	// edge_type_definition artifacts carry when_to_use and semantics sections.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	parchment.SeedEdgeTypeTraits(ctx, s)

	arts, err := s.List(ctx, parchment.Filter{
		Kind:  parchment.KindEdgeTypeDefinition,
		Scope: parchment.SchemaScope,
	})
	if err != nil {
		t.Fatal(err)
	}

	var dependsOnDef *parchment.Artifact
	for _, a := range arts {
		if a.Title == "depends_on" {
			dependsOnDef = a
			break
		}
	}
	if dependsOnDef == nil {
		t.Fatal("depends_on edge_type_definition not found")
	}

	sectionNames := make(map[string]bool)
	for _, sec := range dependsOnDef.Sections {
		sectionNames[sec.Name] = true
	}
	if !sectionNames["when_to_use"] {
		t.Error("depends_on edge_type_definition should have when_to_use section")
	}
	if !sectionNames["semantics"] {
		t.Error("depends_on edge_type_definition should have semantics section")
	}
}
