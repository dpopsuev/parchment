package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// TestArchiveArtifact_CascadeViaParentOf verifies that ArchiveArtifact cascades
// to children via parent_of when CascadeArchive=true is set on that trait.
// This replaces the old "single-artifact only" contract; cascade is now trait-driven.
func TestArchiveArtifact_SingleOnly(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	parent := mustCreate(t, proto, parchment.CreateInput{Title: "parent goal",
		Labels: []string{"kind:goal"},})
	child := mustCreate(t, proto, parchment.CreateInput{Title: "child task", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	// Archive the parent — parent_of has CascadeArchive=true, so child is archived first.
	_, err := proto.ArchiveArtifact(ctx, []string{parent.ID}, false)
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, child.ID)
	if got.ResolvedStatus() != parchment.StatusArchived {
		t.Errorf("ArchiveArtifact with CascadeArchive should cascade to child; child status = %s", got.ResolvedStatus())
	}
}

// TestRetireArtifact_CascadeStillWorks verifies cascade is preserved on retire,
// which is safe (terminal but writable, never vacuumed).
func TestRetireArtifact_CascadeStillWorks(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	parent := mustCreate(t, proto, parchment.CreateInput{Title: "parent to retire",
		Labels: []string{"kind:goal"},})
	child := mustCreate(t, proto, parchment.CreateInput{Title: "child to retire", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	_, err := proto.RetireArtifact(ctx, []string{parent.ID}, true)
	if err != nil {
		t.Fatalf("RetireArtifact cascade: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, child.ID)
	if got.ResolvedStatus() != parchment.StatusRetired {
		t.Errorf("RetireArtifact cascade: child should be retired, got %s", got.ResolvedStatus())
	}
}
