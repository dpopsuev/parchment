package parchment_test

import (
	"context"
	"testing"
	"time"

	parchment "github.com/dpopsuev/parchment"
)

// --- Semantic correctness ---

func TestRetired_IsTerminalNotReadonly(t *testing.T) {
	proto, _ := newProto(t)
	schema := proto.Schema()

	if !schema.IsTerminal(parchment.StatusRetired) {
		t.Error("retired must be terminal — recall() should find it")
	}
	if schema.IsReadonly(parchment.StatusRetired) {
		t.Error("retired must NOT be readonly — post-mortems should be writable")
	}
}

func TestArchived_IsTerminalAndReadonly(t *testing.T) {
	proto, _ := newProto(t)
	schema := proto.Schema()

	if !schema.IsTerminal(parchment.StatusArchived) {
		t.Error("archived must be terminal")
	}
	if !schema.IsReadonly(parchment.StatusArchived) {
		t.Error("archived must be readonly — frozen artifacts")
	}
}

// --- Transitions ---

func TestRetireArtifact_TaskFromComplete(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind:     "task",
		Title:    "implement pipes",
		Sections: []parchment.Section{{Name: "context", Text: "add pipe operator"}},
	})

	// Drive to complete via normal lifecycle.
	for _, st := range []string{"active", "in_progress", "in_review", "complete"} {
		results, err := proto.SetField(ctx, []string{task.ID}, "status", st,
			parchment.SetFieldOptions{Force: true})
		if err != nil || len(results) == 0 || !results[0].OK {
			t.Fatalf("transition to %s failed: err=%v results=%v", st, err, results)
		}
	}

	// Retire from complete.
	results, err := proto.RetireArtifact(ctx, []string{task.ID}, false)
	if err != nil {
		t.Fatalf("RetireArtifact: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Fatalf("expected OK, got %+v", results)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.Status != parchment.StatusRetired {
		t.Errorf("expected status=retired, got %s", art.Status)
	}
}

func TestRetireArtifact_RetiredIsWritable(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind:     "task",
		Title:    "write pipes",
		Sections: []parchment.Section{{Name: "context", Text: "original"}},
	})
	if _, err := proto.SetField(ctx, []string{task.ID}, "status", "complete",
		parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("drive to complete: %v", err)
	}
	if _, err := proto.RetireArtifact(ctx, []string{task.ID}, false); err != nil {
		t.Fatalf("retire: %v", err)
	}

	// Should be able to attach a post-mortem section — retired is not readonly.
	_, err := proto.AttachSection(ctx, task.ID, "post_mortem", "what we learned")
	if err != nil {
		t.Errorf("AttachSection on retired artifact should succeed: %v", err)
	}
}

func TestRetireArtifact_ArchivedIsNotWritable(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind:     "task",
		Title:    "write pipes",
		Sections: []parchment.Section{{Name: "context", Text: "original"}},
	})
	if _, err := proto.SetField(ctx, []string{task.ID}, "status", "complete",
		parchment.SetFieldOptions{Force: true}); err != nil {
		t.Fatalf("drive to complete: %v", err)
	}
	if _, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Archived is readonly — attach should fail.
	_, err := proto.AttachSection(ctx, task.ID, "post_mortem", "should not work")
	if err == nil {
		t.Error("expected error attaching section to archived (readonly) artifact")
	}
}

// --- Vacuum behavior ---

func TestVacuum_SkipsRetired(t *testing.T) {
	ctx := context.Background()
	proto, store := newProto(t)

	// Insert an old retired artifact directly.
	old := time.Now().Add(-100 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID: "TSK-RETIRED-1", Kind: "task", Scope: "test",
		Status: parchment.StatusRetired, Title: "old retired",
		UpdatedAt: old,
	})

	result, err := proto.Vacuum(ctx, 30, "", false)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	for _, id := range result.Deleted {
		if id == "TSK-RETIRED-1" {
			t.Error("Vacuum deleted a retired artifact — retired must be permanent")
		}
	}
}

func TestVacuum_DeletesOldArchived(t *testing.T) {
	ctx := context.Background()
	proto, store := newProto(t)

	old := time.Now().Add(-100 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID: "TSK-ARCH-1", Kind: "task", Scope: "test",
		Status: parchment.StatusArchived, Title: "old archived",
		UpdatedAt: old,
	})

	result, err := proto.Vacuum(ctx, 30, "", true) // force=true: task kind is not Protected
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	found := false
	for _, id := range result.Deleted {
		if id == "TSK-ARCH-1" {
			found = true
		}
	}
	if !found {
		t.Error("Vacuum should delete old archived task")
	}
}

func TestVacuum_SkipsNonVacuumableKind(t *testing.T) {
	ctx := context.Background()

	// Use KnowledgeSchema so note kind is available.
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	old := time.Now().Add(-100 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID: "NOT-1", Kind: "note", Scope: "test",
		Status: parchment.StatusArchived, Title: "old note",
		UpdatedAt: old,
	})

	result, err := proto.Vacuum(ctx, 30, "", true)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	for _, id := range result.Deleted {
		if id == "NOT-1" {
			t.Error("Vacuum deleted a knowledge artifact (note) — knowledge kinds must not be vacuumed")
		}
	}
}
