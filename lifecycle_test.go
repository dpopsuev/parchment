package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestTransition_ActiveToMature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Scope: "test",
		Title: "implement feature X",
		Goal:  "add the feature",
		Sections: []parchment.Section{
			{Name: "context", Text: "context here"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("set active: %v", err)
	}

	// Transition to mature — should succeed (new status).
	if _, err := proto.SetField(ctx, []string{art.ID}, "status", "mature"); err != nil {
		t.Fatalf("set mature: %v", err)
	}

	got, _ := store.Get(ctx, art.ID)
	if got.Status != "mature" {
		t.Errorf("status = %q, want mature", got.Status)
	}
}

func TestTransition_MatureToAllocated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Scope: "test",
		Title: "implement feature Y",
		Goal:  "add the feature",
		Sections: []parchment.Section{
			{Name: "context", Text: "context here"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Activate → mature → allocated.
	proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{Force: true})
	proto.SetField(ctx, []string{art.ID}, "status", "mature", parchment.SetFieldOptions{Force: true})

	if _, err := proto.SetField(ctx, []string{art.ID}, "status", "allocated", parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("set allocated: %v", err)
	}

	got, _ := store.Get(ctx, art.ID)
	if got.Status != "allocated" {
		t.Errorf("status = %q, want allocated", got.Status)
	}
}

func TestTransition_FullLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Scope: "test",
		Title: "full lifecycle task",
		Goal:  "test all transitions",
		Sections: []parchment.Section{
			{Name: "context", Text: "context"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	transitions := []string{"active", "mature", "allocated", "in_progress", "in_review", "complete"}
	for _, status := range transitions {
		if _, err := proto.SetField(ctx, []string{art.ID}, "status", status, parchment.SetFieldOptions{Force: true}); err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
		got, _ := store.Get(ctx, art.ID)
		if got.Status != status {
			t.Errorf("after transition: status = %q, want %q", got.Status, status)
		}
	}
}
