package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedKind_FromField(t *testing.T) {
	art := &parchment.Artifact{Kind: "task"}
	if got := art.ResolvedKind(); got != "task" {
		t.Errorf("expected task, got %q", got)
	}
}

func TestResolvedKind_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"kind:bug", "priority:high"}}
	if got := art.ResolvedKind(); got != "bug" {
		t.Errorf("expected bug, got %q", got)
	}
}

func TestResolvedKind_FieldWinsOverLabel(t *testing.T) {
	art := &parchment.Artifact{Kind: "task", Labels: []string{"kind:bug"}}
	if got := art.ResolvedKind(); got != "task" {
		t.Errorf("expected task (field wins), got %q", got)
	}
}

func TestResolvedKind_EmptyWhenNeither(t *testing.T) {
	art := &parchment.Artifact{}
	if got := art.ResolvedKind(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Filter.Kind with label-based kind ---

func TestFilter_KindMatchesLabelKind(t *testing.T) {
	// Given an artifact with no Kind field but kind:bug label
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"kind:bug"}}
	f := parchment.Filter{Kind: "bug"}
	if !f.Matches(art) {
		t.Error("Filter.Kind=bug should match artifact with labels[kind:bug]")
	}
}

func TestFilter_ExcludeKindMatchesLabelKind(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"kind:bug"}}
	f := parchment.Filter{ExcludeKind: "bug"}
	if f.Matches(art) {
		t.Error("Filter.ExcludeKind=bug should exclude artifact with labels[kind:bug]")
	}
}

// --- ResolvedKindFromLabels for CreateInput ---

func TestCreateArtifact_KindFromLabel(t *testing.T) {
	// Given a protocol and a CreateInput with no Kind field but a kind label
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{
		Title:  "test bug",
		Labels: []string{"kind:bug"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if art.Kind != "bug" {
		t.Errorf("expected Kind=bug, got %q", art.Kind)
	}
}

// --- SetField(kind=X) writes label + mirrors field ---

func TestSetField_KindWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Kind: "task", Title: "original"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	results, err := proto.SetField(t.Context(), []string{art.ID}, "kind", "bug")
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
	if updated.Kind != "bug" {
		t.Errorf("Kind field: expected bug, got %q", updated.Kind)
	}
	var hasKindLabel bool
	for _, l := range updated.Labels {
		if l == "kind:bug" {
			hasKindLabel = true
		}
	}
	if !hasKindLabel {
		t.Errorf("expected kind:bug in labels, got %v", updated.Labels)
	}
}
