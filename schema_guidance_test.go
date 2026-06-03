package parchment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestKindDef_HasAgentGuidance(t *testing.T) {
	// KindDef carries WhenToCreate and AgentNote for agent self-orientation.
	t.Parallel()
	schema := parchment.KnowledgeSchema()
	task, ok := schema.Kinds[parchment.KindTask]
	if !ok {
		t.Fatal("task kind not in schema")
	}
	if task.WhenToCreate == "" {
		t.Error("KindDef.WhenToCreate should be populated for task")
	}
	if task.AgentNote == "" {
		t.Error("KindDef.AgentNote should be populated for task")
	}
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

	// Find the task kind_definition artifact
	arts, err := s.List(ctx, parchment.Filter{
		Kind:  parchment.KindDefinition,
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
