package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// guardDependsOnComplete must read from edges, not art.DependsOn field.
// This verifies that if DependsOn field is empty but an edge exists,
// the guard still fires.
func TestGuardDependsOnComplete_ReadsEdge(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	ctx := t.Context()

	// Create a dependency task and leave it draft (non-terminal).
	dep, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "dep",
		Labels: []string{"kind:effort.task"},})

	// Create a task that depends on dep.
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "work", DependsOn: []string{dep.ID},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})

	// Advance to in_review (prerequisite for complete).
	for _, s := range []string{"active", "mature", "allocated", "in_progress", "in_review"} {
		proto.SetField(ctx, []string{art.ID}, "status", s) //nolint:errcheck // test setup; errors surface in the final SetField assertion
	}

	// Try to complete — should be blocked because dep is still draft.
	results, _ := proto.SetField(ctx, []string{art.ID}, "status", "complete")
	if results[0].OK {
		t.Error("expected block: dep is not terminal")
	}
}

// depends_on edge persisted: after CreateArtifact with DependsOn, the edge
// is queryable via Neighbors even though Artifact no longer carries the field.
func TestGetArtifact_DependsOnFromEdge(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	ctx := t.Context()

	dep, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "dep",
		Labels: []string{"kind:effort.task"},})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "work", DependsOn: []string{dep.ID},
		Labels: []string{"kind:effort.task"},})

	edges, err := store.Neighbors(ctx, art.ID, parchment.RelDependsOn, parchment.Outgoing)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(edges) != 1 || edges[0].To != dep.ID {
		t.Errorf("expected one depends_on edge to %s, got %v", dep.ID, edges)
	}
}
