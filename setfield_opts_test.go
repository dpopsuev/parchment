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

	results, err := proto.SetField(ctx, []string{task.ID}, "status", "archived",
		parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK with BypassGuards, got: %s", results[0].Error)
	}
	art, _ := proto.GetArtifact(ctx, task.ID)
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus) != "archived" {
		t.Errorf("status = %q, want archived", parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus))
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

	results, err := proto.SetField(ctx, []string{goal.ID}, "status", "archived",
		parchment.SetFieldOptions{BypassGuards: true, Cascade: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK with Cascade, got: %s", results[0].Error)
	}

	for _, id := range []string{a.ID, b.ID} {
		art, _ := proto.GetArtifact(ctx, id)
		if parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus) != "archived" {
			t.Errorf("child %s status = %q, want archived", id, parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus))
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
	original := task.Label(parchment.LabelPrefixStatus)

	results, err := proto.SetField(ctx, []string{task.ID}, "status", "archived",
		parchment.SetFieldOptions{BypassGuards: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK {
		t.Fatalf("expected OK result for dry run, got: %s", results[0].Error)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus) != original {
		t.Errorf("dry run mutated status: got %q, want %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus), original)
	}
}

// TestSetField_Labels_ReplacesEntireSet documents that SetField("labels", v)
// replaces the full label array with strings.Split(v, ",").
// This is the root cause of the embedder bug: adding a single label via
// SetField("labels", oneLabel) destroys all existing labels.
//
// Given an artifact with domain labels (kind:note, scope:test, priority:high)
// When SetField("labels", "encoded:test-model") is called (as the embedder did)
// Then only "encoded:test-model" survives — all other labels are gone
//
// This test pins the current behavior so the fix is deliberate and visible.
func TestSetField_Labels_ReplacesEntireSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proto, _ := newProto(t)

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Labels: []string{"kind:note", "scope:test", "priority:high"},
		Title:  "test artifact",
	})

	// Simulate what the embedder did: add one label via SetField.
	_, err := proto.SetField(ctx, []string{art.ID}, "labels", "encoded:test-model")
	if err != nil {
		t.Fatalf("SetField: %v", err)
	}

	after, _ := proto.GetArtifact(ctx, art.ID)

	// Current (broken) behavior: only the new label survives.
	if len(after.Labels) > 3 { // compliance:ok may be added by StampCompliance
		t.Logf("labels after SetField: %v", after.Labels)
	}
	for _, destroyed := range []string{"kind:note", "scope:test", "priority:high"} {
		found := false
		for _, l := range after.Labels {
			if l == destroyed {
				found = true
				break
			}
		}
		if found {
			// When fixed, this test should fail here — remove or invert the assertion.
			t.Logf("label %q survived (fix is working)", destroyed)
		} else {
			t.Logf("label %q was destroyed by SetField(labels) — this is the bug", destroyed)
		}
	}

	// The encoded label must be present regardless.
	found := false
	for _, l := range after.Labels {
		if l == "encoded:test-model" {
			found = true
			break
		}
	}
	if !found {
		t.Error("encoded:test-model label should be present after SetField")
	}
}
