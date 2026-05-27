package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestArchiveArtifact_DryRun_NoMutation(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "dry run target",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})

	results, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false, true) // dry_run=true
	if err != nil {
		t.Fatalf("ArchiveArtifact dry_run: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Fatalf("expected OK result, got %+v", results)
	}

	// Status must be unchanged.
	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.Status == parchment.StatusArchived {
		t.Error("dry_run=true must not mutate status")
	}
}

func TestArchiveArtifact_DryRun_False_DoesArchive(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "real archive",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})

	_, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false, false) // dry_run=false
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if art.Status != parchment.StatusArchived {
		t.Errorf("expected archived, got %s", art.Status)
	}
}
