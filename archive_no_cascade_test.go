package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// TestArchiveArtifact_NoCascade verifies that ArchiveArtifact does not accept
// a cascade flag — archive is single-artifact only. Cascade was the footgun
// behind PRC-BUG-10 (1342 artifacts silently archived). Use RetireArtifact
// for whole-tree terminal operations; archive individual artifacts explicitly.
func TestArchiveArtifact_SingleOnly(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	parent := mustCreate(t, proto, parchment.CreateInput{
		Kind: "goal", Title: "parent goal",
	})
	child := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "child task", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})

	// Archive the parent — child must NOT be archived.
	_, err := proto.ArchiveArtifact(ctx, []string{parent.ID}, false)
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, child.ID)
	if got.Status == parchment.StatusArchived {
		t.Error("ArchiveArtifact must not cascade to children — child was archived")
	}
}

// TestRetireArtifact_CascadeStillWorks verifies cascade is preserved on retire,
// which is safe (terminal but writable, never vacuumed).
func TestRetireArtifact_CascadeStillWorks(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	parent := mustCreate(t, proto, parchment.CreateInput{
		Kind: "goal", Title: "parent to retire",
	})
	child := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "child to retire", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})

	_, err := proto.RetireArtifact(ctx, []string{parent.ID}, true)
	if err != nil {
		t.Fatalf("RetireArtifact cascade: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, child.ID)
	if got.Status != parchment.StatusRetired {
		t.Errorf("RetireArtifact cascade: child should be retired, got %s", got.Status)
	}
}
