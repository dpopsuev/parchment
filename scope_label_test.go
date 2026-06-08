package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedScope_FromField(t *testing.T) {
	art := &parchment.Artifact{Scope: "scribe"}
	if got := art.ResolvedScope(); got != "scribe" {
		t.Errorf("expected scribe, got %q", got)
	}
}

func TestResolvedScope_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"kind:task", "scope:parchment"}}
	if got := art.ResolvedScope(); got != "parchment" {
		t.Errorf("expected parchment, got %q", got)
	}
}

func TestResolvedScope_FieldWinsOverLabel(t *testing.T) {
	art := &parchment.Artifact{Scope: "scribe", Labels: []string{"scope:parchment"}}
	if got := art.ResolvedScope(); got != "scribe" {
		t.Errorf("expected scribe (field wins), got %q", got)
	}
}

func TestCreateArtifact_MirrorsScopeToLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"myproject"}, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "scoped task", Scope: "myproject",
		Labels: []string{"kind:task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var hasScopeLabel bool
	for _, l := range art.Labels {
		if l == "scope:myproject" {
			hasScopeLabel = true
		}
	}
	if !hasScopeLabel {
		t.Errorf("expected scope:myproject in labels, got %v", art.Labels)
	}
}
