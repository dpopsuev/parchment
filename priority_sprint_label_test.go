package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// --- Priority label ---

func TestCreateArtifact_SeedsPriorityLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "urgent work", Priority: "high",
		Labels: []string{"kind:task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, l := range art.Labels {
		if l == "priority:high" {
			return
		}
	}
	t.Errorf("expected priority:high in labels, got %v", art.Labels)
}

func TestSetField_PriorityWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, _ := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "t", Priority: "low",
		Labels: []string{"kind:task"},})
	results, err := proto.SetField(t.Context(), []string{art.ID}, "priority", "high")
	if err != nil || !results[0].OK {
		t.Fatalf("SetField: %v / %v", err, results)
	}
	updated, _ := store.Get(t.Context(), art.ID)
	for _, l := range updated.Labels {
		if l == "priority:high" {
			return
		}
	}
	t.Errorf("expected priority:high in labels, got %v", updated.Labels)
}

// --- Sprint label ---

func TestSetField_SprintWritesLabel(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	art, _ := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title: "t",
		Labels: []string{"kind:task"},})
	// Set initial sprint
	proto.SetField(t.Context(), []string{art.ID}, "sprint", "2026-Q1") //nolint:errcheck // test setup
	results, err := proto.SetField(t.Context(), []string{art.ID}, "sprint", "2026-Q2")
	if err != nil || !results[0].OK {
		t.Fatalf("SetField: %v / %v", err, results)
	}
	updated, _ := store.Get(t.Context(), art.ID)
	for _, l := range updated.Labels {
		if l == "sprint:2026-Q2" {
			return
		}
	}
	t.Errorf("expected sprint:2026-Q2 in labels, got %v", updated.Labels)
}

func TestFilter_SprintMatchesLabelSprint(t *testing.T) {
	art := &parchment.Artifact{Labels: []string{"sprint:2026-Q2"}}
	f := parchment.Filter{Labels: []string{"sprint:2026-Q2"}}
	if !f.Matches(art) {
		t.Error("Filter with sprint label should match artifact with labels[sprint:2026-Q2]")
	}
}
