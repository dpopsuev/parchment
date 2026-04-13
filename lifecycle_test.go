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

func TestTransition_InvalidTransitionBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "blocked", Priority: "medium",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})
	proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{Force: true})

	// active → complete should be blocked (must go through lifecycle).
	results, err := proto.SetField(ctx, []string{art.ID}, "status", "complete", parchment.SetFieldOptions{})
	if err != nil {
		t.Fatalf("SetField error: %v", err)
	}
	if len(results) == 0 || results[0].OK {
		t.Fatal("expected active→complete to be blocked by transition map")
	}

	got, _ := store.Get(ctx, art.ID)
	if got.Status != "active" {
		t.Errorf("status = %q, want active (unchanged)", got.Status)
	}
}

func TestTransition_WorkerIDRequiredForAllocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "needs worker", Priority: "medium",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})
	proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{Force: true})
	proto.SetField(ctx, []string{art.ID}, "status", "mature", parchment.SetFieldOptions{Force: true})

	// Allocate without worker_id — should be blocked (guard is forceable).
	results, _ := proto.SetField(ctx, []string{art.ID}, "status", "allocated", parchment.SetFieldOptions{})
	if len(results) == 0 || results[0].OK {
		t.Fatal("expected allocation without worker_id to be blocked")
	}

	// Now set worker_id via Extra and retry.
	got, _ := store.Get(ctx, art.ID)
	if got.Extra == nil {
		got.Extra = make(map[string]any)
	}
	got.Extra["worker_id"] = "agent-1"
	store.Put(ctx, got)

	results, _ = proto.SetField(ctx, []string{art.ID}, "status", "allocated", parchment.SetFieldOptions{})
	if len(results) == 0 || !results[0].OK {
		errMsg := ""
		if len(results) > 0 {
			errMsg = results[0].Error
		}
		t.Fatalf("allocation with worker_id should succeed: %s", errMsg)
	}
}

func TestAttachSection_StampsPopulatesComponentFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "code link test", Priority: "medium",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})

	// Attach stamps with evidence containing file:line references.
	stamps := `[{"field":"title","status":"verified","evidence":"main.go:42"},{"field":"goal","status":"verified","evidence":"handler.go:10"}]`
	proto.AttachSection(ctx, art.ID, "stamps", stamps)

	got, _ := store.Get(ctx, art.ID)
	if len(got.Components.Files) != 2 {
		t.Fatalf("Components.Files = %v, want 2 entries", got.Components.Files)
	}

	// Verify file paths were extracted (without line numbers).
	files := got.Components.Files
	hasMain := false
	hasHandler := false
	for _, f := range files {
		if f == "main.go" {
			hasMain = true
		}
		if f == "handler.go" {
			hasHandler = true
		}
	}
	if !hasMain || !hasHandler {
		t.Errorf("Components.Files = %v, want [main.go, handler.go]", files)
	}
}

func TestAttachSection_StampsDeduplicatesFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "dedup test", Priority: "medium",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})

	// Two stamps referencing the same file.
	stamps := `[{"field":"a","status":"verified","evidence":"main.go:1"},{"field":"b","status":"verified","evidence":"main.go:99"}]`
	proto.AttachSection(ctx, art.ID, "stamps", stamps)

	got, _ := store.Get(ctx, art.ID)
	if len(got.Components.Files) != 1 {
		t.Fatalf("Components.Files = %v, want 1 (deduplicated)", got.Components.Files)
	}
}

func TestTransition_StampsRequiredForReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "task", Scope: "test", Title: "needs stamps", Priority: "medium",
		Sections: []parchment.Section{{Name: "context", Text: "c"}},
	})
	// Walk to in_progress.
	proto.SetField(ctx, []string{art.ID}, "status", "active", parchment.SetFieldOptions{Force: true})
	proto.SetField(ctx, []string{art.ID}, "status", "mature", parchment.SetFieldOptions{Force: true})
	proto.SetField(ctx, []string{art.ID}, "status", "allocated", parchment.SetFieldOptions{Force: true})
	proto.SetField(ctx, []string{art.ID}, "status", "in_progress", parchment.SetFieldOptions{Force: true})

	// in_progress → in_review without stamps — should be blocked.
	results, _ := proto.SetField(ctx, []string{art.ID}, "status", "in_review", parchment.SetFieldOptions{})
	if len(results) == 0 || results[0].OK {
		t.Fatal("expected in_review without stamps section to be blocked")
	}

	// Attach stamps section and retry.
	proto.AttachSection(ctx, art.ID, "stamps", `[{"field":"title","status":"verified","evidence":"main.go:1"}]`)

	results, _ = proto.SetField(ctx, []string{art.ID}, "status", "in_review", parchment.SetFieldOptions{})
	if len(results) == 0 || !results[0].OK {
		errMsg := ""
		if len(results) > 0 {
			errMsg = results[0].Error
		}
		t.Fatalf("in_review with stamps should succeed: %s", errMsg)
	}
}
