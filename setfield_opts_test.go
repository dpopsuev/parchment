package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// Tests for PRC-ADR-7: SetFieldOptions{BypassGuards, Cascade, DryRun}

func TestSetFieldOptions_BypassGuards_ArchivesWithoutGuards(t *testing.T) {
	// Given a task exists
	// When SetField(status=archived, BypassGuards=true) is called
	// Then the artifact is archived even if guards would normally block it
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "T",
	})

	results, err := proto.SetField(ctx, []string{task.ID}, "status", parchment.StatusArchived,
		parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK with BypassGuards, got: %s", results[0].Error)
	}
	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.Status != parchment.StatusArchived {
		t.Errorf("status = %q, want archived", art.Status)
	}
}

func TestSetFieldOptions_Cascade_TransitionsChildren(t *testing.T) {
	// Given a goal with two task children
	// When SetField(status=archived, Cascade=true) is called on the goal
	// Then all children are also archived
	ctx := context.Background()
	proto, _ := newProto(t)

	goal := mustCreate(t, proto, parchment.CreateInput{Kind: "goal", Title: "G"})
	a := mustCreate(t, proto, parchment.CreateInput{Kind: "task", Title: "A", Parent: goal.ID})
	b := mustCreate(t, proto, parchment.CreateInput{Kind: "task", Title: "B", Parent: goal.ID})

	results, err := proto.SetField(ctx, []string{goal.ID}, "status", parchment.StatusArchived,
		parchment.SetFieldOptions{BypassGuards: true, Cascade: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK with Cascade, got: %s", results[0].Error)
	}

	for _, id := range []string{a.ID, b.ID} {
		art, _ := proto.GetArtifact(ctx, id)
		if art.Status != parchment.StatusArchived {
			t.Errorf("child %s status = %q, want archived", id, art.Status)
		}
	}
}

func TestSetFieldOptions_DryRun_NoMutation(t *testing.T) {
	// Given a task exists
	// When SetField(status=archived, DryRun=true) is called
	// Then the artifact status is unchanged
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{Kind: "task", Title: "T"})
	original := task.Status

	results, err := proto.SetField(ctx, []string{task.ID}, "status", parchment.StatusArchived,
		parchment.SetFieldOptions{BypassGuards: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK result for dry run, got: %s", results[0].Error)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.Status != original {
		t.Errorf("dry run mutated status: got %q, want %q", art.Status, original)
	}
}
