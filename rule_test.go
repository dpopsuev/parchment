package parchment_test

import (
	"context"
	"testing"
	"time"

	"github.com/dpopsuev/parchment"
)

func TestRule_ArtifactKindExists(t *testing.T) {
	// kind=rule is in KnowledgeSchema and registerable via registry.
	t.Parallel()
	schema := parchment.KnowledgeSchema()
	if _, ok := schema.Kinds[parchment.KindRule]; !ok {
		t.Fatalf("kind=rule not in schema — registry YAML not loaded")
	}
}

func TestRule_ParsedFromArtifact(t *testing.T) {
	// A rule artifact with trigger/when/action/message sections
	// must be parseable into a RuleDef struct.
	t.Parallel()
	art := &parchment.Artifact{
		ID:    "RULE-001",
		Kind:  parchment.KindRule,
		Scope: parchment.SchemaScope,
		Title: "priority_required",
		Sections: []parchment.Section{
			{Name: "trigger", Text: "status_changed"},
			{Name: "when", Text: "to=active AND kind=task AND priority==\"\""},
			{Name: "action", Text: "block"},
			{Name: "message", Text: "priority is required before activation"},
		},
	}
	rule, err := parchment.ParseRule(art)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if rule.Trigger != "status_changed" {
		t.Errorf("trigger = %q, want status_changed", rule.Trigger)
	}
	if rule.Action != "block" {
		t.Errorf("action = %q, want block", rule.Action)
	}
	if rule.Message == "" {
		t.Error("message must not be empty")
	}
}

func TestRule_SeededInSchema(t *testing.T) {
	// SeedDefinitions creates a RULE-priority_required artifact in _schema.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	parchment.SeedDefinitions(ctx, s)
	parchment.SeedRules(ctx, s)

	arts, err := s.List(ctx, parchment.Filter{
		Kind:  parchment.KindRule,
		Scope: parchment.SchemaScope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) == 0 {
		t.Fatal("no rule artifacts seeded in _schema")
	}
	// priority_required must be seeded
	found := false
	for _, a := range arts {
		if a.Title == "priority_required" {
			found = true
			break
		}
	}
	if !found {
		titles := make([]string, len(arts))
		for i, a := range arts {
			titles[i] = a.Title
		}
		t.Errorf("priority_required rule not seeded; got: %v", titles)
	}
}

func TestRule_LoadedByProtocol(t *testing.T) {
	// Protocol.LoadRules returns RuleDef slice from _schema artifacts.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Manually seed one rule
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID: "RULE-test", Kind: parchment.KindRule, Scope: parchment.SchemaScope,
		Title: "test_rule", Status: parchment.StatusActive,
		Sections: []parchment.Section{
			{Name: "trigger", Text: "status_changed"},
			{Name: "when", Text: "to=active"},
			{Name: "action", Text: "block"},
			{Name: "message", Text: "test block"},
		},
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	rules, err := proto.LoadRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("LoadRules returned no rules")
	}
}
