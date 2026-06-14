package parchment_test

import (
	"slices"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedStatus_FromDomainLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"priority:high", "work.draft"}}
	if got := parchment.StatusFromLabels(art.Labels); got != "work.draft" {
		t.Errorf("expected work.draft, got %q", got)
	}
}

func TestResolvedStatus_FromSystemLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"priority:high", "status:retired"}}
	if got := parchment.StatusFromLabels(art.Labels); got != "retired" {
		t.Errorf("expected retired, got %q", got)
	}
}

func TestResolvedStatus_EmptyWhenNoLabel(t *testing.T) {
	art := &parchment.Artifact{}
	if got := parchment.StatusFromLabels(art.Labels); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFilter_LabelsStatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"work.active"}}
	f := parchment.Filter{Labels: []string{"work.active"}}
	if !f.Matches(art) {
		t.Error("Filter.Labels=[work.active] should match artifact with labels[work.active]")
	}
}

func TestFilter_ExcludeStatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"status:archived"}}
	f := parchment.Filter{ExcludeLabels: []string{"status:archived"}}
	if f.Matches(art) {
		t.Error("ExcludeLabels=status:archived should exclude artifact with labels[status:archived]")
	}
}

func TestSetField_StatusWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "status test",
		Labels: []string{"kind:knowledge.note"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	results, err := proto.SetField(t.Context(), []string{art.ID}, "status", "note.evergreen")
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
	if parchment.StatusFromLabels(updated.Labels) != "note.evergreen" {
		t.Errorf("StatusFromLabels(): expected note.evergreen, got %q", parchment.StatusFromLabels(updated.Labels))
	}
	if !slices.Contains(updated.Labels, "note.evergreen") {
		t.Errorf("expected note.evergreen in labels, got %v", updated.Labels)
	}
}
