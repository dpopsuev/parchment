package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestDiscusses_RegisteredAndLinkable(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	found := false
	for _, rel := range proto.RegisteredRelations() {
		if rel == parchment.RelDiscusses {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("discusses missing from RegisteredRelations")
	}

	task := createTask(t, proto, "Discussed task")
	note, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Labels:   []string{"kind:knowledge.note", parchment.LabelPrefixScope + "test"},
		Title:    "A comment",
		Sections: []parchment.Section{{Name: "body", Text: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := proto.LinkArtifacts(ctx, note.ID, parchment.RelDiscusses, []string{task.ID}, 0)
	if err != nil {
		t.Fatalf("LinkArtifacts discusses: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("expected OK, got %+v", results)
	}
	edges, err := store.Neighbors(ctx, task.ID, parchment.RelDiscusses, parchment.Incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].From != note.ID {
		t.Fatalf("incoming discusses = %+v", edges)
	}
}
