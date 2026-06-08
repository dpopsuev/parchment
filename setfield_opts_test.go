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

	task := mustCreate(t, proto, parchment.CreateInput{Title: "T",
		Labels: []string{"kind:task"},})

	results, err := proto.SetField(ctx, []string{task.ID}, "status", parchment.StatusArchived,
		parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK with BypassGuards, got: %s", results[0].Error)
	}
	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.ResolvedStatus() != parchment.StatusArchived {
		t.Errorf("status = %q, want archived", art.ResolvedStatus())
	}
}

func TestSetFieldOptions_Cascade_TransitionsChildren(t *testing.T) {
	// Given a goal with two task children
	// When SetField(status=archived, Cascade=true) is called on the goal
	// Then all children are also archived
	ctx := context.Background()
	proto, _ := newProto(t)

	goal := mustCreate(t, proto, parchment.CreateInput{Title: "G",
		Labels: []string{"kind:goal"},})
	a := mustCreate(t, proto, parchment.CreateInput{Title: "A", Parent: goal.ID,
		Labels: []string{"kind:task"},})
	b := mustCreate(t, proto, parchment.CreateInput{Title: "B", Parent: goal.ID,
		Labels: []string{"kind:task"},})

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
		if art.ResolvedStatus() != parchment.StatusArchived {
			t.Errorf("child %s status = %q, want archived", id, art.ResolvedStatus())
		}
	}
}

func TestSetFieldOptions_DryRun_NoMutation(t *testing.T) {
	// Given a task exists
	// When SetField(status=archived, DryRun=true) is called
	// Then the artifact status is unchanged
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{Title: "T",
		Labels: []string{"kind:task"},})
	original := task.ResolvedStatus()

	results, err := proto.SetField(ctx, []string{task.ID}, "status", parchment.StatusArchived,
		parchment.SetFieldOptions{BypassGuards: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK result for dry run, got: %s", results[0].Error)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.ResolvedStatus() != original {
		t.Errorf("dry run mutated status: got %q, want %q", art.ResolvedStatus(), original)
	}
}
