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
