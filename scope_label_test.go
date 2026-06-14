package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestResolvedScope_FromLabel(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"scope:scribe"}}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixScope); got != "scribe" {
		t.Errorf("expected scribe, got %q", got)
	}
}

func TestResolvedScope_FromLabelWithKind(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"kind:effort.task", "scope:parchment"}}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixScope); got != "parchment" {
		t.Errorf("expected parchment, got %q", got)
	}
}

func TestResolvedScope_FirstLabelWins(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"scope:scribe", "scope:parchment"}}
	if got := parchment.LabelValue(art.Labels, parchment.LabelPrefixScope); got != "scribe" {
		t.Errorf("expected scribe (first label wins), got %q", got)
	}
}

func TestCreateArtifact_MirrorsScopeToLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"myproject"}, nil, parchment.ProtocolConfig{})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "scoped task",
		Labels: []string{"kind:effort.task"},})
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
