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
