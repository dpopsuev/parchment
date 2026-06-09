package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestArchiveArtifact_DryRun_NoMutation(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{Title: "dry run target",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	results, err := proto.ArchiveArtifact(ctx, []string{task.ID}, true) // dry_run=true
	if err != nil {
		t.Fatalf("ArchiveArtifact dry_run: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Fatalf("expected OK result, got %+v", results)
	}

	// Status must be unchanged.
	art, _ := proto.GetArtifact(ctx, task.ID)
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus) == parchment.StatusArchived {
		t.Error("dry_run=true must not mutate status")
	}
}

func TestArchiveArtifact_DryRun_False_DoesArchive(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	task := mustCreate(t, proto, parchment.CreateInput{Title: "real archive",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	_, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false) // dry_run=false
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}

	art, _ := proto.GetArtifact(ctx, task.ID)
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus) != parchment.StatusArchived {
		t.Errorf("expected archived, got %s", parchment.LabelValue(art.Labels, parchment.LabelPrefixStatus))
	}
}
