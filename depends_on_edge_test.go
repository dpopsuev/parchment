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
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	ctx := t.Context()

	// Create a dependency task and leave it draft (non-terminal).
	dep, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: "task", Title: "dep"})

	// Create a task that depends on dep.
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Title: "work", DependsOn: []string{dep.ID},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Priority: "medium",
	})

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

// DependsOn field populated from edges: GetArtifact returns DependsOn
// derived from the depends_on edge even when the field was not explicitly set.
func TestGetArtifact_DependsOnFromEdge(t *testing.T) {
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{IDFormat: "sequential"})
	ctx := t.Context()

	dep, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: "task", Title: "dep"})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Title: "work", DependsOn: []string{dep.ID},
	})

	fetched, err := proto.GetArtifact(ctx, art.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fetched.DependsOn) != 1 || fetched.DependsOn[0] != dep.ID {
		t.Errorf("expected DependsOn=[%s], got %v", dep.ID, fetched.DependsOn)
	}
}
