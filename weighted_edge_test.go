package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestLinkArtifacts_WeightStoredOnEdge(t *testing.T) {
	// Given: two artifacts
	// When: LinkArtifacts is called with weight=0.75
	// Then: the stored edge has Weight=0.75
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "A",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "B",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	_, err := proto.LinkArtifacts(ctx, a.ID, "related", []string{b.ID}, 0.75)
	if err != nil {
		t.Fatalf("LinkArtifacts with weight: %v", err)
	}

	edges, _ := store.Neighbors(ctx, a.ID, "related", parchment.Outgoing)
	if len(edges) == 0 {
		t.Fatal("expected edge, got none")
	}
	if edges[0].Weight != 0.75 {
		t.Errorf("edge weight = %v, want 0.75", edges[0].Weight)
	}
}

func TestLinkArtifacts_ZeroWeightDefaultBehavior(t *testing.T) {
	// Zero weight is the default for work-tracking edges — existing callers unaffected.
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "A",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "B",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	_, err := proto.LinkArtifacts(ctx, a.ID, "related", []string{b.ID}, 0)
	if err != nil {
		t.Fatalf("LinkArtifacts with zero weight: %v", err)
	}
	edges, _ := store.Neighbors(ctx, a.ID, "related", parchment.Outgoing)
	if len(edges) == 0 || edges[0].Weight != 0 {
		t.Errorf("zero weight edge not stored correctly: %v", edges)
	}
}
