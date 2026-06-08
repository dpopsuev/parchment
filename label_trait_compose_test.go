package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestLoadLabelTraitsWithComposition_InheritsFromComposedLabel(t *testing.T) {
	// Given: two label_definition artifacts where child composes parent via edge.
	// When:  loadLabelTraitsWithComposition reads the store.
	// Then:  child inherits parent's eviction_policy.
	s := parchment.NewMemoryStore()
	ctx := context.Background()
	now := func() string { return "2026-01-01T00:00:00Z" }
	_ = now

	parent := &parchment.Artifact{
		ID:     "LDEF-rule",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope,
		Title:  "rule",
		Extra:  map[string]any{"eviction_policy": "protected"},
	}
	child := &parchment.Artifact{
		ID:     "LDEF-rule.security",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope,
		Title:  "rule.security",
		Extra:  map[string]any{},
	}
	if err := s.Put(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, child); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(ctx, parchment.Edge{From: child.ID, To: parent.ID, Relation: "composes"}); err != nil {
		t.Fatal(err)
	}

	traits := parchment.LoadLabelTraitsWithComposition(ctx, s)

	lt := traits["rule.security"]
	if lt.EvictionPolicy != "protected" {
		t.Errorf("rule.security should inherit eviction_policy=protected from rule, got %q", lt.EvictionPolicy)
	}
}

func TestLoadLabelTraitsWithComposition_OwnTraitOverridesParent(t *testing.T) {
	// Given: child has its own eviction_policy.
	// Then:  child's own value is kept (own wins over composed).
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	parent := &parchment.Artifact{
		ID:     "LDEF-base",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope,
		Title:  "base",
		Extra:  map[string]any{"eviction_policy": "protected"},
	}
	child := &parchment.Artifact{
		ID:     "LDEF-base.override",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope,
		Title:  "base.override",
		Extra:  map[string]any{"eviction_policy": "aggressive"},
	}
	_ = s.Put(ctx, parent)
	_ = s.Put(ctx, child)
	_ = s.AddEdge(ctx, parchment.Edge{From: child.ID, To: parent.ID, Relation: "composes"})

	traits := parchment.LoadLabelTraitsWithComposition(ctx, s)

	lt := traits["base.override"]
	if lt.EvictionPolicy != "aggressive" {
		t.Errorf("own trait should win over composed: want aggressive, got %q", lt.EvictionPolicy)
	}
}

func TestLoadLabelTraitsWithComposition_NoComposesEdge_SameAsLoadLabelTraits(t *testing.T) {
	// Given: label_definition with no composes edges.
	// Then:  result is identical to loadLabelTraits behavior.
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{
		ID:     "LDEF-session",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindLabelDefinition, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope:  parchment.SchemaScope,
		Title:  "session",
		Extra:  map[string]any{"world": "session"},
	}
	_ = s.Put(ctx, art)

	traits := parchment.LoadLabelTraitsWithComposition(ctx, s)

	lt := traits["session"]
	if lt.World != "session" {
		t.Errorf("want world=session, got %q", lt.World)
	}
}
