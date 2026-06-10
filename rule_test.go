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
		ID:     "RULE-001",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindRule, parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "priority_required",
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
	// SeedRules seeds rule artifacts from registry/rules/*.yaml.
	// Currently only the _placeholder exists — real rules are added
	// one at a time as Go guards are deleted (PRC-GOL-18 tasks).
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	parchment.SeedRules(ctx, s)

	arts, err := s.List(ctx, parchment.Filter{
		Labels: []string{parchment.LabelPrefixKind + parchment.KindRule, parchment.LabelPrefixScope + parchment.SchemaScope},
	})
	if err != nil {
		t.Fatal(err)
	}
	// At minimum the _placeholder is seeded; it has trigger=_none so it never fires.
	if len(arts) == 0 {
		t.Fatal("SeedRules seeded nothing — embed may be broken")
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
		ID:     "RULE-test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindRule, parchment.LabelPrefixStatus + "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "test_rule",
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
