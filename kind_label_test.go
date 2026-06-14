package parchment_test

import (
	"slices"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedKind_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"kind:intent.bug", "priority:high"}}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixKind); got != "intent.bug" {
		t.Errorf("expected intent.bug, got %q", got)
	}
}

func TestResolvedKind_EmptyWhenNoLabel(t *testing.T) {
	art := &parchment.Artifact{}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixKind); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Filter label-based kind matching ---

func TestFilter_LabelsKindMatchesLabelKind(t *testing.T) {
	// Given an artifact with kind:bug label
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"kind:intent.bug"}}
	f := parchment.Filter{Labels: []string{"kind:intent.bug"}}
	if !f.Matches(art) {
		t.Error("Filter.Labels=[kind:bug] should match artifact with labels[kind:bug]")
	}
}

func TestFilter_ExcludeKindMatchesLabelKind(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"kind:intent.bug"}}
	f := parchment.Filter{ExcludeLabels: []string{"kind:intent.bug"}}
	if f.Matches(art) {
		t.Error("Filter.ExcludeKind=bug should exclude artifact with labels[kind:bug]")
	}
}

// --- ResolvedKind for CreateInput ---

func TestCreateArtifact_KindFromLabel(t *testing.T) {
	// Given a protocol and a CreateInput with no Kind field but a kind label
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{
		Title:  "test bug",
		Labels: []string{"kind:intent.bug"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixKind) != "intent.bug" {
		t.Errorf("expected ResolvedKind()=intent.bug, got %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixKind))
	}
}

// --- SetField(kind=X) writes label ---

func TestSetField_KindWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "original",
		Labels: []string{"kind:effort.task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	results, err := proto.SetField(t.Context(), []string{art.ID}, "kind", "intent.bug")
	if err != nil {
		t.Fatalf("SetField: %v", err)
	}
	if !results[0].OK {
		t.Fatalf("SetField failed: %s", results[0].Error)
	}
	updated, err := store.Get(t.Context(), art.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Label(parchment.LabelPrefixKind) != "intent.bug" {
		t.Errorf("ResolvedKind(): expected intent.bug, got %q", updated.Label(parchment.LabelPrefixKind))
	}
	if !slices.Contains(updated.Labels, "kind:intent.bug") {
		t.Errorf("expected kind:intent.bug in labels, got %v", updated.Labels)
	}
}
