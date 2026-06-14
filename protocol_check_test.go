package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- Check ---

func TestCheck_DetectsUnknownKind(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Directly insert an artifact with unknown kind via store
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "BAD-001",
		Labels: []string{"kind:phantom", "scope:test"},
		Title:  "bad kind artifact",
	})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.TotalViolations == 0 {
		t.Fatal("expected at least one violation for unknown kind")
	}

	found := false
	for _, v := range report.Violations {
		if v.Category == "unknown_kind" && v.ID == "BAD-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown_kind violation for BAD-001, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsInvalidParent(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// task cannot be child of task (task Children is empty slice = leaf)
	parentTask := createTask(t, proto, "parent task")
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "CHILD-001",
		Labels: []string{"kind:effort.task", "status:draft", "scope:test"},
		Title:  "child task",
	})
	store.AddEdge(ctx, parchment.Edge{From: parentTask.ID, To: "CHILD-001", Relation: parchment.RelParentOf}) //nolint:errcheck // test seeding

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "invalid_parent" && v.ID == "CHILD-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid_parent violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsEmptyArtifact(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Insert a draft task with no goal, no sections, no parent, no edges
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "EMPTY-001",
		Labels: []string{"kind:effort.task", "work.draft", "scope:test"},
		Title:  "empty task",
	})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "empty_artifact" && v.ID == "EMPTY-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected empty_artifact violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsDuplicateTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "duplicate title")
	createTask(t, proto, "duplicate title")

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "duplicate_title" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate_title violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsCompletableCampaign(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	campaign := createCampaign(t, proto, "completable campaign")
	// Set campaign to work.active
	art, _ := store.Get(ctx, campaign.ID)
	art.Labels = parchment.SetStatusLabel(art.Labels, "work.active")
	store.Put(ctx, art)

	child := createGoal(t, proto, "done child")
	childArt, _ := store.Get(ctx, child.ID)
	childArt.Labels = parchment.SetStatusLabel(childArt.Labels, "work.complete")
	store.Put(ctx, childArt)
	store.AddEdge(ctx, parchment.Edge{From: campaign.ID, To: child.ID, Relation: parchment.RelParentOf})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "completable" && v.ID == campaign.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected completable violation for campaign, got: %+v", report.Violations)
	}
}

func TestCheck_ScopedCheck(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{Title: "alpha goal",
		Labels: []string{"kind:effort.goal", parchment.LabelPrefixScope + "alpha"}})
	mustCreate(t, proto, parchment.CreateInput{Title: "beta goal",
		Labels: []string{"kind:effort.goal", parchment.LabelPrefixScope + "beta"}})

	report, err := proto.Check(ctx, "alpha")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Should only scan alpha-scope artifacts
	if report.TotalScanned != 1 {
		t.Errorf("expected 1 scanned (alpha only), got %d", report.TotalScanned)
	}
}
