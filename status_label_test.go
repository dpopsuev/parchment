package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedStatus_FromField(t *testing.T) {
	art := &parchment.Artifact{Status: "active"}
	if got := art.ResolvedStatus(); got != "active" {
		t.Errorf("expected active, got %q", got)
	}
}

func TestResolvedStatus_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"priority:high", "status:draft"}}
	if got := art.ResolvedStatus(); got != "draft" {
		t.Errorf("expected draft, got %q", got)
	}
}

func TestResolvedStatus_FieldWinsOverLabel(t *testing.T) {
	art := &parchment.Artifact{Status: "active", Labels: []string{"status:draft"}}
	if got := art.ResolvedStatus(); got != "active" {
		t.Errorf("expected active (field wins), got %q", got)
	}
}

func TestResolvedStatus_EmptyWhenNeither(t *testing.T) {
	art := &parchment.Artifact{}
	if got := art.ResolvedStatus(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFilter_StatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"status:active"}}
	f := parchment.Filter{Status: "active"}
	if !f.Matches(art) {
		t.Error("Filter.Status=active should match artifact with labels[status:active]")
	}
}

func TestFilter_ExcludeStatusMatchesLabelStatus(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"status:archived"}}
	f := parchment.Filter{ExcludeStatus: "archived"}
	if f.Matches(art) {
		t.Error("Filter.ExcludeStatus=archived should exclude artifact with labels[status:archived]")
	}
}

func TestSetField_StatusWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Kind: "note", Title: "status test"})
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
	if updated.Status != "evergreen" {
		t.Errorf("Status field: expected evergreen, got %q", updated.Status)
	}
	var hasStatusLabel bool
	for _, l := range updated.Labels {
		if l == "status:evergreen" {
			hasStatusLabel = true
		}
	}
	if !hasStatusLabel {
		t.Errorf("expected status:evergreen in labels, got %v", updated.Labels)
	}
}
