package parchment_test

import (
	"slices"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedStatus_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"priority:high", "status:draft"}}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus); got != "draft" {
		t.Errorf("expected draft, got %q", got)
	}
}

func TestResolvedStatus_EmptyWhenNoLabel(t *testing.T) {
	art := &parchment.Artifact{}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFilter_LabelsStatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"status:active"}}
	f := parchment.Filter{Labels: []string{"status:active"}}
	if !f.Matches(art) {
		t.Error("Filter.Labels=[status:active] should match artifact with labels[status:active]")
	}
}

func TestFilter_ExcludeStatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"status:archived"}}
	f := parchment.Filter{ExcludeLabels: []string{"status:archived"}}
	if f.Matches(art) {
		t.Error("Filter.ExcludeStatus=archived should exclude artifact with labels[status:archived]")
	}
}

func TestSetField_StatusWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "status test",
		Labels: []string{"kind:note"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	results, err := proto.SetField(t.Context(), []string{art.ID}, "status", "evergreen")
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
	if updated.Label(parchment.LabelPrefixStatus) != "evergreen" {
		t.Errorf("ResolvedStatus(): expected evergreen, got %q", updated.Label(parchment.LabelPrefixStatus))
	}
	if !slices.Contains(updated.Labels, "status:evergreen") {
		t.Errorf("expected status:evergreen in labels, got %v", updated.Labels)
	}
}
