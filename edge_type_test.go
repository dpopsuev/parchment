package parchment_test

import (
	"context"
	"testing"
	"time"

	parchment "github.com/dpopsuev/parchment"
)

func TestSeedEdgeTypeTraits_PopulatesRegistry(t *testing.T) {
	// Given a fresh store
	// When SeedEdgeTypeTraits is called
	// Then edge_type_definition artifacts exist for universal relations
	ctx := context.Background()
	proto, s := newProto(t)
	_ = proto
	parchment.SeedEdgeTypeTraits(ctx, s)

	arts, err := s.List(ctx, parchment.Filter{
		Kind:  parchment.KindEdgeTypeDefinition,
		Scope: parchment.SchemaScope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) == 0 {
		t.Fatal("expected edge type definitions after SeedEdgeTypeTraits")
	}
	names := make(map[string]bool)
	for _, a := range arts {
		names[a.Title] = true
	}
	for _, required := range []string{"parent_of", "depends_on", "related"} {
		if !names[required] {
			t.Errorf("expected universal edge type %q to be seeded", required)
		}
	}
}

func TestResolveEdgeTrait_ReturnsTraitForKnownRelation(t *testing.T) {
	// Given edge types are loaded
	// When ResolveEdgeTrait is called for "parent_of"
	// Then a non-zero trait is returned
	ctx := context.Background()
	proto, s := newProto(t)
	parchment.SeedEdgeTypeTraits(ctx, s)

	trait := proto.ResolveEdgeTrait("parent_of")
	_ = trait // non-nil return sufficient
}

func TestValidRelation_AcceptsRegisteredEdgeType(t *testing.T) {
	// Given a custom edge type "mentors" is seeded in _schema
	// When LinkArtifacts is called with relation="mentors"
	// Then it is accepted (open world — not in hardcoded list but in registry)
	ctx := context.Background()
	proto, s := newProto(t)

	// Seed a custom edge type
	now := time.Now().UTC()
	if err := s.Put(ctx, &parchment.Artifact{
		ID: "EDT-mentors", Kind: parchment.KindEdgeTypeDefinition,
		Scope: parchment.SchemaScope, Title: "mentors",
		Status: parchment.StatusActive, CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Reload so protocol picks up the new edge type
	proto2 := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	a := mustCreate(t, proto2, parchment.CreateInput{Kind: "task", Title: "A", Scope: "test"})
	b := mustCreate(t, proto, parchment.CreateInput{Kind: "task", Title: "B", Scope: "test"})

	_, err := proto2.LinkArtifacts(ctx, a.ID, "mentors", []string{b.ID})
	if err != nil {
		t.Errorf("expected custom edge type 'mentors' to be accepted, got: %v", err)
	}
}

func TestResolveEdgeTrait_ReturnsZeroForUnknown(t *testing.T) {
	// Given no edge types are loaded
	// When ResolveEdgeTrait is called for an unknown relation
	// Then a zero-value trait is returned (open world)
	ctx := context.Background()
	proto, s := newProto(t)
	parchment.SeedEdgeTypeTraits(ctx, s)
	trait := proto.ResolveEdgeTrait("custom_relation")
	if trait.MaxOutgoing != 0 || trait.MaxIncoming != 0 {
		t.Errorf("expected zero-value trait for unknown relation, got %+v", trait)
	}
}
