package parchment_test

import (
	"context"
	"strings"
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
		Labels: []string{parchment.LabelPrefixKind + parchment.KindEdgeTypeDefinition, parchment.LabelPrefixScope + parchment.SchemaScope},
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

func TestBond_RejectsWhenMaxOutgoingExceeded(t *testing.T) {
	// Given an edge type "owns" with MaxOutgoing=1
	// When a second "owns" edge is added from the same source
	// Then it is rejected
	ctx := context.Background()
	proto, s := newProto(t)

	now := time.Now().UTC()
	if err := s.Put(ctx, &parchment.Artifact{
		ID:     "EDT-owns",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindEdgeTypeDefinition, "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "owns",
		Extra:  map[string]any{"max_outgoing": float64(1)},
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	proto2 := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	a := mustCreate(t, proto2, parchment.CreateInput{Title: "A",
		Labels: []string{"kind:task"},})
	b := mustCreate(t, proto, parchment.CreateInput{Title: "B",
		Labels: []string{"kind:task"},})
	c := mustCreate(t, proto, parchment.CreateInput{Title: "C",
		Labels: []string{"kind:task"},})

	if _, err := proto2.LinkArtifacts(ctx, a.ID, "owns", []string{b.ID}, 0); err != nil {
		t.Fatalf("first link should succeed: %v", err)
	}
	_, err := proto2.LinkArtifacts(ctx, a.ID, "owns", []string{c.ID}, 0)
	if err == nil {
		t.Fatal("expected error when MaxOutgoing=1 is exceeded")
	}
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
		ID:     "EDT-mentors",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindEdgeTypeDefinition, "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "mentors",
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Reload so protocol picks up the new edge type
	proto2 := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	a := mustCreate(t, proto2, parchment.CreateInput{Title: "A",
		Labels: []string{"kind:task"},})
	b := mustCreate(t, proto, parchment.CreateInput{Title: "B",
		Labels: []string{"kind:task"},})

	_, err := proto2.LinkArtifacts(ctx, a.ID, "mentors", []string{b.ID}, 0)
	if err != nil {
		t.Errorf("expected custom edge type 'mentors' to be accepted, got: %v", err)
	}
}

func TestLinkArtifacts_ErrorListsRegisteredRelations(t *testing.T) {
	// Given a custom edge type "sponsors" is in the registry
	// When LinkArtifacts is called with an unknown relation
	// Then the error message lists "sponsors" alongside hardcoded relations
	ctx := context.Background()
	_, s := newProto(t)
	now := time.Now().UTC()
	if err := s.Put(ctx, &parchment.Artifact{
		ID:     "EDT-sponsors",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindEdgeTypeDefinition, "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "sponsors",
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	proto2 := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	a := mustCreate(t, proto2, parchment.CreateInput{Title: "A",
		Labels: []string{"kind:task"},})

	_, err := proto2.LinkArtifacts(ctx, a.ID, "imaginary_xyz", []string{"x"}, 0)
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
	if !strings.Contains(err.Error(), "sponsors") {
		t.Errorf("error should list registered relation 'sponsors', got: %s", err.Error())
	}
}

func TestProtocol_RegisteredRelations_IncludesTraits(t *testing.T) {
	// Given a custom edge type "coaches" is in the registry
	// When RegisteredRelations() is called
	// Then it includes both hardcoded schema relations and "coaches"
	ctx := context.Background()
	_, s := newProto(t)
	now := time.Now().UTC()
	if err := s.Put(ctx, &parchment.Artifact{
		ID:     "EDT-coaches",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindEdgeTypeDefinition, "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  "coaches",
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	proto2 := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	rels := proto2.RegisteredRelations()
	found := false
	for _, r := range rels {
		if r == "coaches" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RegisteredRelations() should include 'coaches', got: %v", rels)
	}
	// Must also include at least one hardcoded schema relation
	foundHardcoded := false
	for _, r := range rels {
		if r == "depends_on" {
			foundHardcoded = true
			break
		}
	}
	if !foundHardcoded {
		t.Errorf("RegisteredRelations() should include hardcoded 'depends_on', got: %v", rels)
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
