package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestTransition_DraftToActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title: "implement feature X",
		Goal:  "add the feature",
		Sections: []parchment.Section{
			{Name: "context", Text: "context here"},
		},
		Labels: []string{"kind:effort.task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := proto.SetField(ctx, []string{art.ID}, "status", "work.active", parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("set work.active: %v", err)
	}

	got, _ := store.Get(ctx, art.ID)
	if parchment.StatusFromLabels(got.Labels) != "work.active" {
		t.Errorf("status = %q, want work.active", parchment.StatusFromLabels(got.Labels))
	}
}

func TestTransition_ActiveToComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title: "implement feature Y",
		Goal:  "add the feature",
		Sections: []parchment.Section{
			{Name: "context", Text: "context here"},
		},
		Labels: []string{"kind:effort.task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	proto.SetField(ctx, []string{art.ID}, "status", "work.active", parchment.SetFieldOptions{Force: true})

	if _, err := proto.SetField(ctx, []string{art.ID}, "status", "work.complete", parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("set work.complete: %v", err)
	}

	got, _ := store.Get(ctx, art.ID)
	if parchment.StatusFromLabels(got.Labels) != "work.complete" {
		t.Errorf("status = %q, want work.complete", parchment.StatusFromLabels(got.Labels))
	}
}

func TestTransition_FullLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Title: "full lifecycle task",
		Goal:  "test all transitions",
		Sections: []parchment.Section{
			{Name: "context", Text: "context"},
		},
		Labels: []string{"kind:effort.task"},})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	transitions := []string{"work.active", "work.complete"}
	for _, status := range transitions {
		if _, err := proto.SetField(ctx, []string{art.ID}, "status", status, parchment.SetFieldOptions{Force: true}); err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
		got, _ := store.Get(ctx, art.ID)
		if parchment.StatusFromLabels(got.Labels) != status {
			t.Errorf("after transition: status = %q, want %q", parchment.StatusFromLabels(got.Labels), status)
		}
	}
}

func TestTransition_InvalidTransitionBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "blocked",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})

	// work.draft → retired should be blocked (not in transition map).
	results, err := proto.SetField(ctx, []string{art.ID}, "status", "retired", parchment.SetFieldOptions{})
	if err != nil {
		t.Fatalf("SetField error: %v", err)
	}
	if len(results) == 0 || results[0].OK {
		t.Fatal("expected work.draft→retired to be blocked by transition map")
	}

	got, _ := store.Get(ctx, art.ID)
	if parchment.StatusFromLabels(got.Labels) != "work.draft" {
		t.Errorf("status = %q, want work.draft (unchanged)", parchment.StatusFromLabels(got.Labels))
	}
}

func TestTransition_WorkerIDRequiredForAllocation(t *testing.T) {
	t.Parallel()
	// In the new simplified lifecycle there is no allocation step.
	// Force-set to work.active and verify.
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "simple task",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})

	results, err := proto.SetField(ctx, []string{art.ID}, "status", "work.active", parchment.SetFieldOptions{Force: true})
	if err != nil || !results[0].OK {
		t.Fatalf("set work.active failed: %v %v", err, results)
	}

	got, _ := store.Get(ctx, art.ID)
	if parchment.StatusFromLabels(got.Labels) != "work.active" {
		t.Errorf("status = %q, want work.active", parchment.StatusFromLabels(got.Labels))
	}
}

func TestTransition_StampsRequiredForReview(t *testing.T) {
	t.Parallel()
	// In the new lifecycle, in_review is removed. Verify work.active → work.complete transitions.
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "review task",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})

	proto.SetField(ctx, []string{art.ID}, "status", "work.active", parchment.SetFieldOptions{Force: true})

	results, err := proto.SetField(ctx, []string{art.ID}, "status", "work.complete", parchment.SetFieldOptions{Force: true})
	if err != nil || !results[0].OK {
		t.Fatalf("work.active→work.complete should succeed: %v %v", err, results)
	}

	got, _ := store.Get(ctx, art.ID)
	if parchment.StatusFromLabels(got.Labels) != "work.complete" {
		t.Errorf("status = %q, want work.complete", parchment.StatusFromLabels(got.Labels))
	}
}
